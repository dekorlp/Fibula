package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
)

// refPath rebuilds the on-disk location of a ref so that a test can stage the
// states a crashed or competing process would leave behind.
func refPath(dir string, name store.RefName) string {
	return filepath.Join(dir, refsDir, string(name.Scope()), filepath.FromSlash(name.Name()))
}

func openRefsIn(t *testing.T, dir string) store.RefStore {
	t.Helper()

	r, err := OpenRefs(dir)
	if err != nil {
		t.Fatalf("OpenRefs: %v", err)
	}
	return r
}

// TestLockHeldByAnotherProcessBlocksAndGivesUp covers the cross-process half
// of the CAS guard. A lock file that exists and is fresh means another process
// is mid-update: the right behaviour is to wait, and then to fail rather than
// to barge in — barging in is how a ref update gets lost.
func TestLockHeldByAnotherProcessBlocksAndGivesUp(t *testing.T) {
	dir := t.TempDir()
	r := openRefsIn(t, dir)
	name := mainRef(t)

	// Stage a lock as a live competitor would hold it.
	lockPath := refPath(dir, name) + lockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("stage the lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	err := r.CompareAndSwap(ctx, name, hash.VersionID{}, versionID("v"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded while the lock is held", err)
	}

	// Nothing may have been written while the lock was held elsewhere.
	if _, err := r.Get(context.Background(), name); !errors.Is(err, errs.ErrRefNotFound) {
		t.Errorf("a ref was written despite the lock: %v", err)
	}
}

// TestStaleLockIsBroken covers the other side: a process that died holding the
// lock must not block the ref forever. The threshold is generous on purpose,
// so this test has to age the lock explicitly rather than wait it out.
func TestStaleLockIsBroken(t *testing.T) {
	dir := t.TempDir()
	r := openRefsIn(t, dir)
	name := mainRef(t)

	lockPath := refPath(dir, name) + lockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("stage the lock: %v", err)
	}

	aged := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(lockPath, aged, aged); err != nil {
		t.Fatalf("age the lock: %v", err)
	}

	want := versionID("after the crash")
	if err := r.CompareAndSwap(context.Background(), name, hash.VersionID{}, want); err != nil {
		t.Fatalf("CompareAndSwap over a stale lock: %v", err)
	}

	got, err := r.Get(context.Background(), name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("Get = %s, want %s", got, want)
	}
}

// TestLockIsReleasedAfterUse: a lock left behind by our own successful update
// would block every later one.
func TestLockIsReleasedAfterUse(t *testing.T) {
	dir := t.TempDir()
	r := openRefsIn(t, dir)
	name := mainRef(t)
	ctx := context.Background()

	first, second := versionID("first"), versionID("second")
	if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, first); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(refPath(dir, name) + lockSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Error("the lock file survived a successful update")
	}
	if err := r.CompareAndSwap(ctx, name, first, second); err != nil {
		t.Fatalf("second update: %v", err)
	}
}

// TestCorruptRefContentIsReported: a truncated or garbled ref file must be an
// error, never a silently wrong version. A ref is the only answer to "where am
// I", so guessing is not an option.
func TestCorruptRefContentIsReported(t *testing.T) {
	dir := t.TempDir()
	r := openRefsIn(t, dir)
	name := mainRef(t)

	path := refPath(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a version id\n"), 0o644); err != nil {
		t.Fatalf("stage corruption: %v", err)
	}

	if _, err := r.Get(context.Background(), name); !errors.Is(err, errs.ErrMalformedID) {
		t.Errorf("err = %v, want errs.ErrMalformedID", err)
	}
}

// TestListIgnoresForeignFiles: a lock file or an editor backup in the refs
// directory must not break a listing or appear as a ref.
func TestListIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	r := openRefsIn(t, dir)
	ctx := context.Background()
	name := mainRef(t)

	if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, versionID("v")); err != nil {
		t.Fatalf("create: %v", err)
	}

	base := filepath.Join(dir, refsDir, string(store.ScopeLocal))
	foreign := map[string]string{
		"main.lock":     "",
		"notes backup":  "an invalid ref name",
		"ref-123456789": "a leftover temporary file",
	}
	for file, content := range foreign {
		if err := os.WriteFile(filepath.Join(base, file), []byte(content), 0o644); err != nil {
			t.Fatalf("stage %s: %v", file, err)
		}
	}

	got, err := r.List(ctx, store.ScopeLocal)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name() != store.DefaultRef {
		t.Errorf("List = %v, want just %q", got, store.DefaultRef)
	}
}

func TestRefOperationsHonourContextCancellation(t *testing.T) {
	r := openRefs(t)
	name := mainRef(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := r.Get(ctx, name); !errors.Is(err, context.Canceled) {
		t.Errorf("Get err = %v, want context.Canceled", err)
	}
	if _, err := r.List(ctx, store.ScopeLocal); !errors.Is(err, context.Canceled) {
		t.Errorf("List err = %v, want context.Canceled", err)
	}
	if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, versionID("v")); !errors.Is(err, context.Canceled) {
		t.Errorf("CompareAndSwap err = %v, want context.Canceled", err)
	}
	if err := r.Delete(ctx, name); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete err = %v, want context.Canceled", err)
	}
}

func TestOpenRefsRejectsAnEmptyDirectory(t *testing.T) {
	if _, err := OpenRefs(""); !errors.Is(err, errs.ErrInvalidStore) {
		t.Errorf("err = %v, want errs.ErrInvalidStore", err)
	}
}

// TestBackoffIsCapped guards the bound documented on lockRetryMax: without the
// cap the worst-case wait grows to roughly 13 seconds.
func TestBackoffIsCapped(t *testing.T) {
	start := time.Now()

	if err := waitBeforeRetry(context.Background(), lockAttempts); err != nil {
		t.Fatalf("waitBeforeRetry: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 4*lockRetryMax {
		t.Errorf("waited %v on the last attempt, cap is %v", elapsed, lockRetryMax)
	}
}
