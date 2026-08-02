package fs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
)

func newRefStore(t *testing.T) store.RefStore {
	t.Helper()

	r, err := CreateRefs(t.TempDir())
	if err != nil {
		t.Fatalf("CreateRefs: %v", err)
	}
	return r
}

func versionID(s string) hash.VersionID { return hash.Version([]byte(s)) }

func mainRef(t *testing.T) store.RefName {
	t.Helper()

	name, err := store.LocalRef(store.DefaultRef)
	if err != nil {
		t.Fatalf("LocalRef: %v", err)
	}
	return name
}

func TestRefCreateAndRead(t *testing.T) {
	r := newRefStore(t)
	ctx := context.Background()
	name := mainRef(t)
	first := versionID("first")

	// A zero old value means "must not exist yet".
	if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, first); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != first {
		t.Errorf("Get = %s, want %s", got, first)
	}
}

func TestRefGetOfAMissingRef(t *testing.T) {
	r := newRefStore(t)

	if _, err := r.Get(context.Background(), mainRef(t)); !errors.Is(err, errs.ErrRefNotFound) {
		t.Errorf("err = %v, want errs.ErrRefNotFound", err)
	}
}

func TestRefCompareAndSwapConflicts(t *testing.T) {
	ctx := context.Background()
	first, second := versionID("first"), versionID("second")

	tests := []struct {
		name  string
		setup func(*testing.T, store.RefStore, store.RefName)
		old   hash.VersionID
	}{
		{
			name:  "creating a ref that already exists",
			setup: func(t *testing.T, r store.RefStore, n store.RefName) { mustSwap(t, r, n, hash.VersionID{}, first) },
			old:   hash.VersionID{},
		},
		{
			name:  "updating a ref that does not exist",
			setup: func(*testing.T, store.RefStore, store.RefName) {},
			old:   first,
		},
		{
			name:  "updating from the wrong value",
			setup: func(t *testing.T, r store.RefStore, n store.RefName) { mustSwap(t, r, n, hash.VersionID{}, first) },
			old:   versionID("something else"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRefStore(t)
			name := mainRef(t)
			tc.setup(t, r, name)

			err := r.CompareAndSwap(ctx, name, tc.old, second)
			if !errors.Is(err, errs.ErrRefConflict) {
				t.Errorf("err = %v, want errs.ErrRefConflict", err)
			}
		})
	}
}

// TestConcurrentCompareAndSwapLosesNothing is the test E13 asks for. N
// goroutines race to move the same ref from the same starting value: exactly
// one may win, and the losers must be told rather than silently dropped. A
// lost ref update is a lost working state with no second system that knows it.
func TestConcurrentCompareAndSwapLosesNothing(t *testing.T) {
	r := newRefStore(t)
	ctx := context.Background()
	name := mainRef(t)
	start := versionID("start")

	if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, start); err != nil {
		t.Fatalf("create: %v", err)
	}

	const racers = 12
	var winners atomic.Int32
	var conflicts atomic.Int32
	var unexpected atomic.Int32

	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := r.CompareAndSwap(ctx, name, start, versionID(string(rune('a'+i))))
			switch {
			case err == nil:
				winners.Add(1)
			case errors.Is(err, errs.ErrRefConflict):
				conflicts.Add(1)
			default:
				unexpected.Add(1)
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Errorf("%d goroutines succeeded, want exactly 1", got)
	}
	if got := conflicts.Load(); got != racers-1 {
		t.Errorf("%d goroutines saw a conflict, want %d", got, racers-1)
	}
	if got := unexpected.Load(); got != 0 {
		t.Errorf("%d goroutines failed for another reason", got)
	}

	// The surviving value must be one of the contenders, never the start
	// value and never something torn.
	final, err := r.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final == start {
		t.Error("the ref still holds the start value although a swap reported success")
	}
}

// TestConcurrentCreateLosesNothing is the same race for ref creation, where
// the expected old value is the zero version.
func TestConcurrentCreateLosesNothing(t *testing.T) {
	r := newRefStore(t)
	ctx := context.Background()
	name := mainRef(t)

	const racers = 12
	var winners atomic.Int32

	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, versionID(string(rune('a'+i)))); err == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Errorf("%d goroutines created the ref, want exactly 1", got)
	}
}

// TestLocalAndRemoteRefsAreSeparate pins E13.3. Offline work advances a local
// ref; the remote ref records what the server last said. If the two shared a
// namespace, a sync could not tell "my state" from "their state".
func TestLocalAndRemoteRefsAreSeparate(t *testing.T) {
	r := newRefStore(t)
	ctx := context.Background()

	local, err := store.LocalRef(store.DefaultRef)
	if err != nil {
		t.Fatalf("LocalRef: %v", err)
	}
	remote, err := store.RemoteRef(store.DefaultRef)
	if err != nil {
		t.Fatalf("RemoteRef: %v", err)
	}

	mine, theirs := versionID("mine"), versionID("theirs")
	if err := r.CompareAndSwap(ctx, local, hash.VersionID{}, mine); err != nil {
		t.Fatalf("set local: %v", err)
	}
	if err := r.CompareAndSwap(ctx, remote, hash.VersionID{}, theirs); err != nil {
		t.Fatalf("set remote: %v", err)
	}

	gotLocal, err := r.Get(ctx, local)
	if err != nil {
		t.Fatalf("Get local: %v", err)
	}
	gotRemote, err := r.Get(ctx, remote)
	if err != nil {
		t.Fatalf("Get remote: %v", err)
	}

	if gotLocal != mine || gotRemote != theirs {
		t.Errorf("local = %s (want %s), remote = %s (want %s)", gotLocal, mine, gotRemote, theirs)
	}
}

func TestRefList(t *testing.T) {
	r := newRefStore(t)
	ctx := context.Background()

	for _, n := range []string{"main", "feature/rig", "archive/2026"} {
		name, err := store.LocalRef(n)
		if err != nil {
			t.Fatalf("LocalRef(%q): %v", n, err)
		}
		if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, versionID(n)); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}
	remote, err := store.RemoteRef("main")
	if err != nil {
		t.Fatalf("RemoteRef: %v", err)
	}
	if err := r.CompareAndSwap(ctx, remote, hash.VersionID{}, versionID("remote")); err != nil {
		t.Fatalf("create remote: %v", err)
	}

	got, err := r.List(ctx, store.ScopeLocal)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []string{"archive/2026", "feature/rig", "main"}
	if len(got) != len(want) {
		t.Fatalf("List returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Name() != want[i] {
			t.Errorf("List = %v, want %v", got, want)
			break
		}
	}
}

func TestListOfAnEmptyScope(t *testing.T) {
	r := newRefStore(t)

	got, err := r.List(context.Background(), store.ScopeRemote)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want none", got)
	}
}

func TestRefDelete(t *testing.T) {
	r := newRefStore(t)
	ctx := context.Background()
	name := mainRef(t)

	if err := r.CompareAndSwap(ctx, name, hash.VersionID{}, versionID("v")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, name); !errors.Is(err, errs.ErrRefNotFound) {
		t.Errorf("err = %v, want errs.ErrRefNotFound", err)
	}
	if err := r.Delete(ctx, name); err != nil {
		t.Errorf("deleting a missing ref should be a no-op, got %v", err)
	}
}

func TestRefNameValidation(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{name: "plain", ref: "main"},
		{name: "nested", ref: "feature/hero-rig"},
		{name: "dots and underscores", ref: "release_1.2.3"},
		{name: "empty", ref: "", wantErr: true},
		{name: "parent segment", ref: "../../etc/passwd", wantErr: true},
		{name: "current segment", ref: "./main", wantErr: true},
		{name: "leading slash", ref: "/main", wantErr: true},
		{name: "trailing slash", ref: "main/", wantErr: true},
		{name: "empty segment", ref: "a//b", wantErr: true},
		{name: "backslash", ref: `a\b`, wantErr: true},
		{name: "space", ref: "my ref", wantErr: true},
		{name: "control character", ref: "main\n", wantErr: true},
		{name: "lock suffix", ref: "main.lock", wantErr: true},
		{name: "colon", ref: "main:2", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.LocalRef(tc.ref)

			if tc.wantErr && !errors.Is(err, errs.ErrInvalidRefName) {
				t.Errorf("err = %v, want errs.ErrInvalidRefName", err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestSwapToTheZeroVersionIsRejected: a ref pointing at nothing is
// indistinguishable from a deleted ref, and there is a Delete for that.
func TestSwapToTheZeroVersionIsRejected(t *testing.T) {
	r := newRefStore(t)
	name := mainRef(t)

	err := r.CompareAndSwap(context.Background(), name, hash.VersionID{}, hash.VersionID{})
	if !errors.Is(err, errs.ErrInvalidRefName) {
		t.Errorf("err = %v, want errs.ErrInvalidRefName", err)
	}
}

func mustSwap(t *testing.T, r store.RefStore, name store.RefName, old, next hash.VersionID) {
	t.Helper()

	if err := r.CompareAndSwap(context.Background(), name, old, next); err != nil {
		t.Fatalf("CompareAndSwap: %v", err)
	}
}
