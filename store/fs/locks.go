package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
	fpath "github.com/dekorlp/fibula/path"
	"github.com/dekorlp/fibula/store"
)

const locksDir = "locks"

// OpenLocks returns the lock store for an existing store directory.
func OpenLocks(dir string) (store.LockStore, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve store directory: %w", err)
	}
	if info, err := os.Stat(filepath.Join(abs, objectsDir)); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a store", errs.ErrInvalidStore, abs)
	}
	if err := os.MkdirAll(filepath.Join(abs, locksDir), dirPerm); err != nil {
		return nil, fmt.Errorf("create locks directory: %w", err)
	}
	return &lockStore{root: abs}, nil
}

type lockStore struct{ root string }

// path maps a manifest path to a file under locks/.
//
// The path is validated rather than trusted: it arrives from a client, and a
// lock on "../../etc/passwd" would otherwise write outside the store. Same
// reasoning as restore (CLAUDE.md section 4), in a place where it is easier to
// forget because nothing is being restored.
func (l *lockStore) pathFor(p string) (string, error) {
	if err := fpath.Validate(p); err != nil {
		return "", fmt.Errorf("%w: %w", errs.ErrUnsafePath, err)
	}
	return filepath.Join(l.root, locksDir, filepath.FromSlash(p)), nil
}

// Acquire creates the lock file with O_EXCL, which is the same primitive the
// ref lock uses and is atomic on every filesystem that matters here (E13.2).
func (l *lockStore) Acquire(ctx context.Context, lock store.Lock) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	if lock.Owner == "" {
		return fmt.Errorf("%w: a lock needs an owner", errs.ErrLockHeld)
	}

	target, err := l.pathFor(lock.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return fmt.Errorf("create lock directory: %w", err)
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm) //nolint:gosec // a validated manifest path under the store
	if errors.Is(err, os.ErrExist) {
		held, readErr := l.Get(ctx, lock.Path)
		if readErr != nil {
			return fmt.Errorf("%w: %s", errs.ErrLockHeld, lock.Path)
		}
		return fmt.Errorf("%w: %s is held by %s since %s",
			errs.ErrLockHeld, lock.Path, held.Owner, held.Since.UTC().Format(time.RFC3339))
	}
	if err != nil {
		return fmt.Errorf("acquire lock on %s: %w", lock.Path, err)
	}

	if err := writeAndSync(f, []byte(marshalLock(lock))); err != nil {
		_ = f.Close()         //nolint:errcheck // the write already failed and is being reported
		_ = os.Remove(target) //nolint:errcheck // best effort: an empty lock file would block the path forever
		return fmt.Errorf("write lock on %s: %w", lock.Path, err)
	}
	return f.Close()
}

func (l *lockStore) Release(ctx context.Context, path, owner string) error {
	held, err := l.Get(ctx, path)
	if errors.Is(err, errs.ErrLockNotFound) {
		return nil // releasing what is not held is harmless
	}
	if err != nil {
		return err
	}
	if held.Owner != owner {
		return fmt.Errorf("%w: %s is held by %s, not %s", errs.ErrLockHeld, path, held.Owner, owner)
	}

	target, err := l.pathFor(path)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release lock on %s: %w", path, err)
	}
	return nil
}

// Transfer rewrites the lock for a new owner, keeping who lost it.
//
// It replaces rather than deletes and re-creates, so the path is never briefly
// free for a third party to take.
func (l *lockStore) Transfer(ctx context.Context, path, to string, at time.Time) (store.Lock, error) {
	held, err := l.Get(ctx, path)
	if err != nil {
		return store.Lock{}, err
	}

	next := store.Lock{
		Path:       path,
		Owner:      to,
		Since:      at,
		Reason:     held.Reason,
		BrokenFrom: held.Owner,
		BrokenAt:   at,
	}

	target, err := l.pathFor(path)
	if err != nil {
		return store.Lock{}, err
	}
	if err := replaceFile(target, []byte(marshalLock(next))); err != nil {
		return store.Lock{}, fmt.Errorf("transfer lock on %s: %w", path, err)
	}
	return next, nil
}

func (l *lockStore) Get(ctx context.Context, path string) (store.Lock, error) {
	if err := ctx.Err(); err != nil {
		return store.Lock{}, fmt.Errorf("read lock: %w", err)
	}

	target, err := l.pathFor(path)
	if err != nil {
		return store.Lock{}, err
	}
	data, err := os.ReadFile(target) //nolint:gosec // a validated manifest path under the store
	if errors.Is(err, os.ErrNotExist) {
		return store.Lock{}, fmt.Errorf("%w: %s", errs.ErrLockNotFound, path)
	}
	if err != nil {
		return store.Lock{}, fmt.Errorf("read lock on %s: %w", path, err)
	}
	return parseLock(path, string(data))
}

func (l *lockStore) List(ctx context.Context) ([]store.Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list locks: %w", err)
	}

	base := filepath.Join(l.root, locksDir)
	var locks []store.Lock

	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return err
		case d.IsDir():
			return nil
		}

		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		lock, err := l.Get(ctx, filepath.ToSlash(rel))
		if err != nil {
			// A file that is not a readable lock is not ours; skipping beats
			// failing the whole listing over foreign content.
			return nil //nolint:nilerr // deliberately tolerant, as in RefStore.List
		}
		locks = append(locks, lock)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list locks: %w", err)
	}

	sort.Slice(locks, func(i, j int) bool { return locks[i].Path < locks[j].Path })
	return locks, nil
}

func marshalLock(l store.Lock) string {
	body := "owner\t" + l.Owner + "\n" +
		"since\t" + l.Since.UTC().Format(time.RFC3339) + "\n"
	if l.Reason != "" {
		body += "reason\t" + l.Reason + "\n"
	}
	if l.BrokenFrom != "" {
		body += "broken_from\t" + l.BrokenFrom + "\n" +
			"broken_at\t" + l.BrokenAt.UTC().Format(time.RFC3339) + "\n"
	}
	return body
}

func parseLock(path, body string) (store.Lock, error) {
	lock := store.Lock{Path: path}

	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "\t")
		if !ok {
			return store.Lock{}, fmt.Errorf("lock %s: malformed line %q", path, line)
		}

		var err error
		switch key {
		case "owner":
			lock.Owner = value
		case "reason":
			lock.Reason = value
		case "broken_from":
			lock.BrokenFrom = value
		case "since":
			lock.Since, err = time.Parse(time.RFC3339, value)
		case "broken_at":
			lock.BrokenAt, err = time.Parse(time.RFC3339, value)
		}
		if err != nil {
			return store.Lock{}, fmt.Errorf("lock %s: %s: %w", path, key, err)
		}
	}

	if lock.Owner == "" {
		return store.Lock{}, fmt.Errorf("lock %s: no owner", path)
	}
	return lock, nil
}

var _ store.LockStore = (*lockStore)(nil)
