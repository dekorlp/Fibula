package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
)

const (
	refsDir      = "refs"
	lockSuffix   = ".lock"
	lockAttempts = 50
	lockRetry    = 10 * time.Millisecond
	// lockRetryMax caps the backoff so that the total wait stays bounded and
	// predictable. Growing linearly without a cap would put the worst case at
	// around 13 seconds, which is a long time to block a push on a lock that
	// is either free within milliseconds or stale.
	lockRetryMax = 100 * time.Millisecond
	// lockStale is how long a lock file may exist before it is treated as
	// left behind by a crashed process rather than held by a live one.
	lockStale = 30 * time.Second
)

// OpenRefs returns the ref store rooted at dir.
//
// Refs are the only mutable structure in the system and the only answer to
// "what is the current state" (E13). A lost ref update is a lost working
// state, with no second system that still knows it — which is why this is the
// one place in the store that needs locking at all.
func OpenRefs(dir string) (store.RefStore, error) {
	return openRefs(dir, false)
}

// CreateRefs initializes the ref store at dir. Like Create it is idempotent.
func CreateRefs(dir string) (store.RefStore, error) {
	return openRefs(dir, true)
}

func openRefs(dir string, create bool) (store.RefStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", errs.ErrInvalidStore)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve store directory: %w", err)
	}

	if !create {
		// Same reasoning as Open: a vanished store must not come back as an
		// empty one, because "no refs" and "no store" are different answers
		// and only one of them is safe to act on.
		if info, err := os.Stat(filepath.Join(abs, objectsDir)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a store", errs.ErrInvalidStore, abs)
		}
	}
	if err := os.MkdirAll(filepath.Join(abs, refsDir), dirPerm); err != nil {
		return nil, fmt.Errorf("create refs directory: %w", err)
	}
	return &refStore{root: abs}, nil
}

// refStore stores each ref as a file containing its VersionID.
//
// Compare-and-swap is guarded on two levels, because the two kinds of
// concurrency have different answers:
//
//   - Within one process, a mutex per ref name. This is what a sync engine
//     running several pushes at once actually hits.
//   - Across processes, an exclusive lock file created with O_EXCL. Two
//     Fibula binaries against the same NAS share are a real scenario, and
//     O_EXCL create is the one primitive that is atomic on POSIX and Windows
//     alike without cgo.
//
// Read-modify-write without either would silently drop one of two concurrent
// updates, which is precisely the data loss E13 exists to prevent.
type refStore struct {
	root string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (r *refStore) path(name store.RefName) string {
	return filepath.Join(r.root, refsDir, string(name.Scope()), filepath.FromSlash(name.Name()))
}

func (r *refStore) Get(ctx context.Context, name store.RefName) (hash.VersionID, error) {
	if err := ctx.Err(); err != nil {
		return hash.VersionID{}, fmt.Errorf("read ref: %w", err)
	}
	return r.read(name)
}

func (r *refStore) read(name store.RefName) (hash.VersionID, error) {
	data, err := os.ReadFile(r.path(name))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return hash.VersionID{}, fmt.Errorf("%w: %s", errs.ErrRefNotFound, name)
	case err != nil:
		return hash.VersionID{}, fmt.Errorf("read ref %s: %w", name, err)
	}

	id, err := hash.ParseVersionID(strings.TrimSuffix(string(data), "\n"))
	if err != nil {
		return hash.VersionID{}, fmt.Errorf("ref %s: %w", name, err)
	}
	return id, nil
}

// CompareAndSwap moves a ref from old to next.
//
// A zero old value means the ref must not exist yet, which is how a ref is
// created without racing another creator. Any mismatch is reported as a
// conflict and never resolved by overwriting: a non-fast-forward is a user
// decision, not something a store may decide (E13.3).
func (r *refStore) CompareAndSwap(ctx context.Context, name store.RefName, old, next hash.VersionID) error {
	if next.IsZero() {
		return fmt.Errorf("%w: %s would be set to the zero version", errs.ErrInvalidRefName, name)
	}

	unlock, err := r.lock(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()

	if err := r.checkCurrent(name, old); err != nil {
		return err
	}
	return r.write(name, next)
}

// checkCurrent compares the stored value against what the caller expected.
func (r *refStore) checkCurrent(name store.RefName, old hash.VersionID) error {
	current, err := r.read(name)
	switch {
	case errors.Is(err, errs.ErrRefNotFound):
		if !old.IsZero() {
			return fmt.Errorf("%w: %s does not exist, expected %s", errs.ErrRefConflict, name, old)
		}
		return nil
	case err != nil:
		return err
	case old.IsZero():
		return fmt.Errorf("%w: %s already exists at %s", errs.ErrRefConflict, name, current)
	case current != old:
		return fmt.Errorf("%w: %s is at %s, expected %s", errs.ErrRefConflict, name, current, old)
	}
	return nil
}

// write replaces the ref file atomically, so that a crash cannot leave a
// half-written VersionID behind — a truncated ref would be unparseable, and an
// unparseable ref is a lost working state.
func (r *refStore) write(name store.RefName, id hash.VersionID) error {
	final := r.path(name)
	if err := os.MkdirAll(filepath.Dir(final), dirPerm); err != nil {
		return fmt.Errorf("create ref directory for %s: %w", name, err)
	}

	f, err := os.CreateTemp(filepath.Dir(final), "ref-*")
	if err != nil {
		return fmt.Errorf("create temporary ref for %s: %w", name, err)
	}
	temp := f.Name()
	//nolint:errcheck // a no-op once the rename succeeded, best effort otherwise
	defer os.Remove(temp)

	if err := writeAndSync(f, []byte(id.String()+"\n")); err != nil {
		_ = f.Close() //nolint:errcheck // the write already failed and is being reported
		return fmt.Errorf("write ref %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close ref %s: %w", name, err)
	}
	if err := os.Rename(temp, final); err != nil {
		return fmt.Errorf("commit ref %s: %w", name, err)
	}
	return nil
}

func (r *refStore) List(ctx context.Context, scope store.Scope) ([]store.RefName, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list refs: %w", err)
	}

	base := filepath.Join(r.root, refsDir, string(scope))
	var names []store.RefName

	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil // an empty scope is not an error
		case err != nil:
			return err
		case d.IsDir(), strings.HasSuffix(p, lockSuffix), isTempRef(d.Name()):
			return nil
		}

		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		name, err := newRefName(scope, filepath.ToSlash(rel))
		if err != nil {
			// A file that is not a valid ref name is not ours; skipping it is
			// better than failing the whole listing over foreign content.
			return nil //nolint:nilerr // deliberately tolerant, see comment
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list refs in %s: %w", scope, err)
	}

	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
	return names, nil
}

func (r *refStore) Delete(ctx context.Context, name store.RefName) error {
	unlock, err := r.lock(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()

	if err := os.Remove(r.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete ref %s: %w", name, err)
	}
	return nil
}

// lock takes the in-process mutex and the on-disk lock for a ref, and returns
// the function that releases both.
func (r *refStore) lock(ctx context.Context, name store.RefName) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("lock ref %s: %w", name, err)
	}

	mu := r.mutexFor(name)
	mu.Lock()

	lockPath, err := r.acquireFileLock(ctx, name)
	if err != nil {
		mu.Unlock()
		return nil, err
	}

	return func() {
		// Dropping this error is deliberate: the update itself has already
		// succeeded or failed, and a lock left behind expires as stale.
		_ = os.Remove(lockPath) //nolint:errcheck // a lock left behind expires as stale
		mu.Unlock()
	}, nil
}

func (r *refStore) mutexFor(name store.RefName) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.locks == nil {
		r.locks = make(map[string]*sync.Mutex)
	}
	key := name.String()
	if _, ok := r.locks[key]; !ok {
		r.locks[key] = &sync.Mutex{}
	}
	return r.locks[key]
}

// acquireFileLock creates <ref>.lock exclusively. O_EXCL create is atomic on
// every filesystem that matters here, which is what makes it usable as a
// cross-process mutex without cgo.
func (r *refStore) acquireFileLock(ctx context.Context, name store.RefName) (string, error) {
	lockPath := r.path(name) + lockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), dirPerm); err != nil {
		return "", fmt.Errorf("create ref directory for %s: %w", name, err)
	}

	for attempt := range lockAttempts {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm) //nolint:gosec // a path built from a validated ref name
		if err == nil {
			// The lock is the file's existence, not its content.
			_ = f.Close() //nolint:errcheck // the lock is the file, not its contents
			return lockPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("lock ref %s: %w", name, err)
		}

		if breakStaleLock(lockPath) {
			continue
		}
		if err := waitBeforeRetry(ctx, attempt); err != nil {
			return "", fmt.Errorf("lock ref %s: %w", name, err)
		}
	}
	return "", fmt.Errorf("%w: %s is locked by another process", errs.ErrRefConflict, name)
}

// breakStaleLock removes a lock left behind by a process that died holding it.
// The age threshold is deliberately generous: taking a lock away from a live
// writer would reintroduce exactly the lost update the lock prevents.
func breakStaleLock(lockPath string) bool {
	info, err := os.Stat(lockPath)
	if err != nil || time.Since(info.ModTime()) < lockStale {
		return false
	}
	return os.Remove(lockPath) == nil
}

func waitBeforeRetry(ctx context.Context, attempt int) error {
	wait := lockRetry * time.Duration(attempt+1)
	if wait > lockRetryMax {
		wait = lockRetryMax
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTempRef(name string) bool { return strings.HasPrefix(name, "ref-") }

// newRefName rebuilds a validated name during listing. It lives here rather
// than being exported from package store, because reconstructing a name from
// the filesystem is a backend concern.
func newRefName(scope store.Scope, name string) (store.RefName, error) {
	switch scope {
	case store.ScopeLocal:
		return store.LocalRef(name)
	case store.ScopeRemote:
		return store.RemoteRef(name)
	default:
		return store.RefName{}, fmt.Errorf("%w: unknown scope %q", errs.ErrInvalidRefName, scope)
	}
}

var _ store.RefStore = (*refStore)(nil)
