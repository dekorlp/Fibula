package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
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
	return taken, nil
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
		released++
	}
	return released, nil
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

// LockedBySomeoneElse returns the locks covering any of paths that belong to
// somebody other than owner and have not expired.
//
// This is what the commit check will use (F-S5a-07) and what decides which
// files are marked read-only (F-S5a-06).
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
