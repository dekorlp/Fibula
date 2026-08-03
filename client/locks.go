package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/manifest"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// LockRequest is one lock to take.
type LockRequest struct {
	// Pattern is a path or a gitignore-style pattern; `levels/**` takes one
	// lock per matching file rather than a directory lock (E50).
	Pattern string
	Owner   string
	Reason  string
	Now     time.Time

	// Force takes over a lock somebody else holds, recording who lost it.
	// An expired lock does not need it.
	Force bool
}

// Lock reserves every file matching the request.
//
// Taking a lock never blocks anyone from editing - that would break invariant 9
// by making offline work impossible. What it buys is that the other side is
// refused at commit time and sees the file read-only while working (E49).
func (s *Space) Lock(ctx context.Context, ignore *Ignore, req LockRequest) ([]store.Lock, error) {
	if err := s.requireLocking(ctx); err != nil {
		return nil, err
	}
	paths, err := s.matchPaths(ctx, ignore, req.Pattern)
	if err != nil {
		return nil, err
	}

	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}

	taken := make([]store.Lock, 0, len(paths))
	for _, p := range paths {
		lock, err := s.lockOne(ctx, p, req, settings.Expiry())
		if err != nil {
			// Report what was already taken: undoing them would be worse,
			// because a half-finished lock run the user cannot see is how
			// files end up reserved by accident.
			return taken, err
		}
		taken = append(taken, lock)
	}
	// Remembered after the fact, so a failure partway through records exactly
	// what was actually taken.
	return taken, s.rememberLocks(taken)
}

// lockOne takes a single path, taking over where that is allowed.
func (s *Space) lockOne(ctx context.Context, path string, req LockRequest, expiry time.Duration) (store.Lock, error) {
	want := store.Lock{Path: path, Owner: req.Owner, Since: req.Now, Reason: req.Reason}

	err := s.locks.Acquire(ctx, want)
	if err == nil {
		return want, nil
	}
	if !errors.Is(err, errs.ErrLockHeld) {
		return store.Lock{}, err
	}

	held, getErr := s.locks.Get(ctx, path)
	if getErr != nil {
		return store.Lock{}, err
	}
	// An expired lock may be taken over by anyone, which is the answer to a
	// holder who is ill, on holiday or gone (E51). Force is the same operation
	// applied deliberately to one that has not expired yet.
	if !req.Force && !held.Expired(req.Now, expiry) {
		return store.Lock{}, err
	}
	return s.locks.Transfer(ctx, path, req.Owner, req.Now)
}

// Unlock releases locks. Without force only the caller's own are released;
// with it, anyone's are.
func (s *Space) Unlock(ctx context.Context, ignore *Ignore, pattern, owner string, force bool) (int, error) {
	if err := s.requireLocking(ctx); err != nil {
		return 0, err
	}
	paths, err := s.matchPaths(ctx, ignore, pattern)
	if err != nil {
		return 0, err
	}

	released := 0
	var freed []string
	for _, p := range paths {
		if _, err := s.locks.Get(ctx, p); err != nil {
			continue // not locked; releasing a free path is not an error
		}
		if force {
			owner = s.ownerOf(ctx, p, owner)
		}
		if err := s.locks.Release(ctx, p, owner); err != nil {
			return released, err
		}
		freed = append(freed, p)
		released++
	}
	return released, s.forgetLocks(freed)
}

// ownerOf returns whoever actually holds a lock, which is how force releases
// one that belongs to somebody else without the store having to trust a claim.
// An unreadable lock falls back to the caller, and Release reports it from
// there.
func (s *Space) ownerOf(ctx context.Context, path, fallback string) string {
	held, err := s.locks.Get(ctx, path)
	if err != nil {
		return fallback
	}
	return held.Owner
}

// Locks lists every lock in the store, newest information first read from the
// store rather than cached - a stale "nobody holds this" is the answer that
// must not be given.
func (s *Space) Locks(ctx context.Context) ([]store.Lock, error) {
	return s.locks.List(ctx)
}

// LockedBySomeoneElse returns every live lock held by somebody other than
// owner. Expired ones are left out: they may be taken over by anyone, so they
// stop nothing (E51).
//
// It is what the commit check acts on, and what will decide which files are
// marked read-only (F-S5a-06). A project with locking switched off has none,
// even if locks are still lying in the store.
func (s *Space) LockedBySomeoneElse(ctx context.Context, owner string, now time.Time) ([]store.Lock, error) {
	settings, err := s.Settings(ctx)
	if err != nil || !settings.Locking {
		return nil, err
	}

	all, err := s.locks.List(ctx)
	if err != nil {
		return nil, err
	}

	var foreign []store.Lock
	for _, lock := range all {
		if lock.Owner != owner && !lock.Expired(now, settings.Expiry()) {
			foreign = append(foreign, lock)
		}
	}
	return foreign, nil
}

// checkLocksForCommit refuses a commit that would publish a change to a file
// somebody else holds (E49).
//
// This is the enforcement; the read-only attribute is only a reminder, and a
// tool that saves by delete-and-recreate drops it. Editing is never refused -
// requiring a lock to work would break invariant 9 - so this is the one place
// a lock actually stops something.
//
// Only paths that changed are checked. Holding a lock on a file you are not
// touching has to be free, or a project-wide reservation would block everyone
// from committing anything.
func (s *Space) checkLocksForCommit(ctx context.Context, next object.Manifest, owner string, now time.Time) error {
	foreign, err := s.LockedBySomeoneElse(ctx, owner, now)
	if err != nil || len(foreign) == 0 {
		return err
	}

	current, err := s.CheckedOutManifest()
	if err != nil && !errors.Is(err, errs.ErrRefNotFound) {
		return err
	}

	changes := manifest.Diff(current, next)
	touched := make(map[string]struct{})
	for _, c := range changes.Changed {
		touched[c.Path()] = struct{}{}
	}
	for _, e := range changes.Removed {
		touched[e.Path] = struct{}{}
	}
	// Added counts too: a locked path can be deleted and recreated, and
	// recreating it is exactly what the holder reserved it against.
	for _, e := range changes.Added {
		touched[e.Path] = struct{}{}
	}
	for _, r := range changes.Renamed {
		touched[r.Before.Path] = struct{}{}
		touched[r.After.Path] = struct{}{}
	}

	var blocked []store.Lock
	for _, lock := range foreign {
		if _, hit := touched[lock.Path]; hit {
			blocked = append(blocked, lock)
		}
	}
	if len(blocked) == 0 {
		return nil
	}
	return lockedError(blocked)
}

// lockedError names every blocking lock rather than only the first, so that
// one refusal tells the user everything they have to sort out.
func lockedError(blocked []store.Lock) error {
	lines := make([]string, len(blocked))
	for i, lock := range blocked {
		lines[i] = fmt.Sprintf("%s (held by %s since %s)",
			lock.Path, lock.Owner, lock.Since.UTC().Format(time.RFC3339))
	}
	return fmt.Errorf("%w: %s", errs.ErrLockHeld, strings.Join(lines, ", "))
}

// requireLocking refuses lock operations on a project that has not turned
// locking on, rather than quietly writing locks nobody will honour (E48).
func (s *Space) requireLocking(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Locking {
		return fmt.Errorf("%w; turn it on with: fibula locking on", errs.ErrLockingDisabled)
	}
	return nil
}

// matchPaths expands a pattern against the working directory.
//
// It reuses the ignore matcher rather than inventing a second pattern syntax:
// a pattern is a pattern, and the audience already learned this one for
// .fibulaignore (E18).
func (s *Space) matchPaths(ctx context.Context, ignore *Ignore, pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("%w: empty pattern", errs.ErrInvalidPath)
	}

	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return nil, err
	}
	matcher, err := ParseIgnore(strings.NewReader(pattern + "\n"))
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, f := range files {
		if matcher.Match(f.Path, false) {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: nothing matches %q", errs.ErrInvalidPath, pattern)
	}
	return paths, nil
}
