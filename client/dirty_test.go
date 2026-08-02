package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dekorlp/fibula/hash"
)

// The five cases F-S3-04 names, each as its own test. They are the reason the
// dirty check exists, so they are asserted individually rather than folded
// into a table where one could quietly stop running.

// TestClearAbortsWhenTheStoreIsUnreachable: a store we cannot read is not a
// store we may trust with the only copy.
func TestClearAbortsWhenTheStoreIsUnreachable(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "content")
	snapshot(ctx, t, space)

	// Take the store away, as an unmounted NAS share would.
	if err := os.RemoveAll(storeDir); err != nil {
		t.Fatalf("remove store: %v", err)
	}

	check, err := space.CheckClear(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("CheckClear: %v", err)
	}
	if check.OK() {
		t.Fatal("the check passed although the store is gone")
	}
	assertNothingDeleted(t, space, "assets/hero.fbx")
}

// TestClearAbortsWhenAChunkIsMissingFromTheStore is check 3 of E17, the one
// people forget: a snapshot created locally and never synchronized sits on the
// very disk about to be cleared.
func TestClearAbortsWhenAChunkIsMissingFromTheStore(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", strings.Repeat("mesh data ", 500))
	snapshot(ctx, t, space)

	removeOneChunk(t, storeDir)

	check, err := space.CheckClear(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("CheckClear: %v", err)
	}
	if check.OK() {
		t.Fatal("the check passed although a chunk is missing from the store")
	}
	if len(check.Safe) != 0 {
		t.Errorf("Safe = %v, want nothing declared safe", check.Safe)
	}
	assertNothingDeleted(t, space, "assets/hero.fbx")
}

// TestClearAbortsWhenAFileChangedSinceTheSnapshot: the working copy holds
// content no version knows, so deleting it would lose it.
func TestClearAbortsWhenAFileChangedSinceTheSnapshot(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "original content")
	snapshot(ctx, t, space)

	writeFile(t, space.Root(), "assets/hero.fbx", "edited content")

	check, err := space.CheckClear(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("CheckClear: %v", err)
	}
	if len(check.Unversioned) != 1 || check.Unversioned[0] != "assets/hero.fbx" {
		t.Errorf("Unversioned = %v, want the edited file", check.Unversioned)
	}
	if len(check.Safe) != 0 {
		t.Errorf("Safe = %v, want the edited file not declared safe", check.Safe)
	}
}

// TestClearDetectsAChangeTheCacheLiesAbout is the heart of E17. The status
// cache is a heuristic: mtime lies across clock jumps, with tools that
// preserve timestamps, and on network shares. Here the content changes while
// size and mtime are restored to their old values, so anything trusting the
// cache would happily delete content no version holds.
func TestClearDetectsAChangeTheCacheLiesAbout(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "original content")
	snapshot(ctx, t, space)

	abs := filepath.Join(space.Root(), "assets", "hero.fbx")
	before, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Same length, different bytes, and the timestamp put back afterwards.
	if err := os.WriteFile(abs, []byte("EDITED!! content"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := os.Chtimes(abs, before.ModTime(), before.ModTime()); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	// The cache is fooled, which is exactly what makes it unusable here.
	status, err := space.Status(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.IsClean() {
		t.Fatalf("the premise of this test is wrong: the cache noticed the edit: %+v", status)
	}

	// The dirty check re-hashes and is not fooled.
	check, err := space.CheckClear(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("CheckClear: %v", err)
	}
	if len(check.Safe) != 0 {
		t.Errorf("Safe = %v, want nothing safe: the content is not in any version", check.Safe)
	}
	if len(check.Unversioned) != 1 {
		t.Errorf("Unversioned = %v, want the edited file", check.Unversioned)
	}
}

// TestClearSnapshotsUnversionedFilesRatherThanAborting: aborting with "there
// is something unversioned here" is technically correct and teaches users to
// reach for --force, which is how a safety feature stops being one (E17).
func TestClearSnapshotsUnversionedFilesRatherThanAborting(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "versioned content")
	snapshot(ctx, t, space)

	writeFile(t, space.Root(), "assets/new.png", "never versioned")

	result, err := space.Clear(ctx, &Ignore{}, ClearOptions{Author: "dennis", Now: fixedTime()})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if result.Snapshotted != 1 {
		t.Errorf("Snapshotted = %d, want 1", result.Snapshotted)
	}
	if result.Deleted != 2 {
		t.Errorf("Deleted = %d, want both files", result.Deleted)
	}
	assertGone(t, space, "assets/hero.fbx", "assets/new.png")

	// And the previously unversioned file is recoverable.
	if _, err := space.Restore(ctx); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	assertContent(t, space, "assets/new.png", "never versioned")
}

// removeOneChunk deletes a single chunk from the store, simulating an upload
// that never finished or a store that lost an object.
func removeOneChunk(t *testing.T, storeDir string) {
	t.Helper()

	chunkRoot := filepath.Join(storeDir, "objects", "chunk")
	var victim string
	err := filepath.WalkDir(chunkRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && victim == "" {
			victim = p
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk chunks: %v", err)
	}
	if victim == "" {
		t.Fatal("no chunk found in the store to remove")
	}
	if err := os.Remove(victim); err != nil {
		t.Fatalf("remove chunk: %v", err)
	}
}

func assertNothingDeleted(t *testing.T, space *Space, paths ...string) {
	t.Helper()

	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(space.Root(), filepath.FromSlash(p))); err != nil {
			t.Errorf("%s was deleted although the check failed: %v", p, err)
		}
	}
}

func assertGone(t *testing.T, space *Space, paths ...string) {
	t.Helper()

	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(space.Root(), filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Errorf("%s is still on disk after clearing", p)
		}
	}
}

func assertContent(t *testing.T, space *Space, p, want string) {
	t.Helper()

	got, err := os.ReadFile(filepath.Join(space.Root(), filepath.FromSlash(p)))
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", p, got, want)
	}
}

// TestReachableFilesFollowsTheSnapshotChain: an older snapshot's content still
// counts as versioned, which is what makes the chain a safety net rather than
// a single undo step.
func TestReachableFilesFollowsTheSnapshotChain(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "assets/hero.fbx", "first")
	snapshot(ctx, t, space)
	first := readFileID(t, space, "assets/hero.fbx")

	writeFile(t, space.Root(), "assets/hero.fbx", "second")
	snapshot(ctx, t, space)

	reachable, err := space.reachableFiles(ctx)
	if err != nil {
		t.Fatalf("reachableFiles: %v", err)
	}
	if _, ok := reachable[first]; !ok {
		t.Error("the content of the first snapshot is no longer reachable")
	}
}

func readFileID(t *testing.T, space *Space, p string) hash.FileID {
	t.Helper()

	cache, err := space.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	entry, ok := cache.Lookup(p)
	if !ok {
		t.Fatalf("%s is not in the cache", p)
	}
	return entry.File
}

func fixedTime() time.Time { return time.Date(2026, 8, 2, 14, 20, 31, 0, time.UTC) }

// snapshot takes an auto snapshot with no ignore rules.
func snapshot(ctx context.Context, t *testing.T, space *Space) {
	t.Helper()
	snapshotWith(ctx, t, space, &Ignore{})
}

func snapshotWith(ctx context.Context, t *testing.T, space *Space, ignore *Ignore) {
	t.Helper()

	if _, err := space.Snapshot(ctx, ignore, SnapshotOptions{
		Author: "dennis",
		Expiry: fixedTime().AddDate(0, 6, 0),
		Now:    fixedTime(),
	}); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
}

// newSpace creates a space with its own store and returns both roots.
func newSpace(t *testing.T) (*Space, string) {
	t.Helper()

	root := t.TempDir()
	storeDir := t.TempDir()

	space, err := Init(root, storeDir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return space, storeDir
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()

	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
