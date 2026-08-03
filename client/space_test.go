package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dekorlp/fibula/errs"
)

// TestSnapshotReusesTheCache is the done-criterion of F-S3-01, in the form a
// test can check: a second snapshot over an unchanged tree must read nothing.
// That is the whole reason the status cache exists — on a 200 GB tree it is
// the difference between a snapshot every few minutes and one nobody can
// afford to take.
func TestSnapshotReusesTheCache(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	for _, p := range []string{"a.bin", "b.bin", "nested/c.bin"} {
		writeFile(t, space.Root(), p, strings.Repeat(p, 200))
	}

	first, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts())
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if first.Hashed != 3 {
		t.Errorf("first snapshot hashed %d files, want all 3", first.Hashed)
	}

	second, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts())
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second.Hashed != 0 {
		t.Errorf("second snapshot re-read %d files, want none: the cache did not do its job", second.Hashed)
	}
	if second.Chunked != 0 {
		t.Errorf("second snapshot re-chunked %d bytes over an unchanged tree", second.Chunked)
	}
	if second.Manifest != first.Manifest {
		t.Errorf("an unchanged tree produced a different manifest: %s then %s", first.Manifest, second.Manifest)
	}
}

// TestSnapshotOnlyReadsWhatChanged: one edited file must not cost a full
// re-read of the tree.
func TestSnapshotOnlyReadsWhatChanged(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	for _, p := range []string{"a.bin", "b.bin", "c.bin"} {
		writeFile(t, space.Root(), p, strings.Repeat(p, 200))
	}
	if _, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts()); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	writeFile(t, space.Root(), "b.bin", "edited")

	second, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts())
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second.Hashed != 1 {
		t.Errorf("hashed %d files after one edit, want 1", second.Hashed)
	}
}

// TestSnapshotsAreATimelineNotAChain pins the addendum to E12. A snapshot has
// no parent: a parent pointer would keep every older snapshot reachable and
// make the thinning schedule of E14 impossible, because the middle of a
// timeline could never be dropped.
func TestSnapshotsAreATimelineNotAChain(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "a.bin", "one")
	first, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts())
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	writeFile(t, space.Root(), "a.bin", "two")
	later := snapshotOpts()
	later.Now = fixedTime().Add(time.Hour)
	second, err := space.Snapshot(ctx, &Ignore{}, later)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	version, err := space.readVersion(ctx, second.Version)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if len(version.Parents) != 0 {
		t.Errorf("parents = %v, want none: a snapshot is a point in time", version.Parents)
	}
	if version.Expiry.IsZero() {
		t.Error("an auto snapshot has no expiry set (E12)")
	}
	if version.Message != "" {
		t.Error("an auto snapshot carries a message (E12)")
	}

	// Both are on the timeline, in order, each reachable on its own.
	timeline, err := space.Timeline(ctx)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) != 2 {
		t.Fatalf("timeline has %d entries, want 2", len(timeline))
	}
	if timeline[0].Version != first.Version || timeline[1].Version != second.Version {
		t.Errorf("timeline = %v, want the two snapshots oldest first", timeline)
	}
}

// TestSnapshotIsIdempotentWithinASecond: an unchanged tree snapshotted twice
// produces the same version, so it is one point on the timeline and not a
// conflict.
func TestSnapshotIsIdempotentWithinASecond(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")

	first, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts())
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	second, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts())
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if first.Version != second.Version {
		t.Errorf("the same tree produced %s and %s", first.Version, second.Version)
	}

	timeline, err := space.Timeline(ctx)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Errorf("timeline has %d entries, want 1", len(timeline))
	}
}

func TestStatusReportsChanges(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "keep.bin", "unchanged")
	writeFile(t, space.Root(), "edit.bin", "before")
	writeFile(t, space.Root(), "gone.bin", "doomed")
	if _, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts()); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	writeFile(t, space.Root(), "edit.bin", "after, and longer")
	writeFile(t, space.Root(), "new.bin", "fresh")
	if err := os.Remove(filepath.Join(space.Root(), "gone.bin")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	status, err := space.Status(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(status.Added) != 1 || status.Added[0] != "new.bin" {
		t.Errorf("Added = %v, want [new.bin]", status.Added)
	}
	if len(status.Modified) != 1 || status.Modified[0] != "edit.bin" {
		t.Errorf("Modified = %v, want [edit.bin]", status.Modified)
	}
	if len(status.Removed) != 1 || status.Removed[0] != "gone.bin" {
		t.Errorf("Removed = %v, want [gone.bin]", status.Removed)
	}
	if status.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1", status.Unchanged)
	}
	if status.IsClean() {
		t.Error("IsClean reported a dirty tree as clean")
	}
}

func TestBudget(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", strings.Repeat("x", 1000))

	used, reached, err := space.Budget(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if used != 1000 {
		t.Errorf("used = %d, want 1000", used)
	}
	if reached {
		t.Error("reached is true although no budget is configured")
	}

	// A configured budget is a suggestion trigger, never a deletion trigger.
	space.config.Budget = 500
	_, reached, err = space.Budget(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if !reached {
		t.Error("reached is false although the budget is exceeded")
	}
	if _, err := os.Stat(filepath.Join(space.Root(), "a.bin")); err != nil {
		t.Errorf("the file was touched by a budget query: %v", err)
	}
}

func TestInitAndOpen(t *testing.T) {
	root := t.TempDir()
	storeDir := t.TempDir()

	space, err := Init(root, storeDir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if space.Config().Store == "" {
		t.Error("the store path was not recorded")
	}
	if space.Objects() == nil || space.Refs() == nil {
		t.Error("the space has no store handles")
	}

	// A second init on the same directory is an error, not a silent reset.
	if _, err := Init(root, storeDir); !errors.Is(err, errs.ErrNotASpace) {
		t.Errorf("second Init err = %v, want errs.ErrNotASpace", err)
	}

	// Open finds the space from a subdirectory, as a user running a command
	// from inside their project expects.
	nested := filepath.Join(root, "assets", "char")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	found, err := Open(nested)
	if err != nil {
		t.Fatalf("Open from a subdirectory: %v", err)
	}
	if found.Root() != space.Root() {
		t.Errorf("Open found %q, want %q", found.Root(), space.Root())
	}
}

func TestOpenOutsideASpace(t *testing.T) {
	if _, err := Open(t.TempDir()); !errors.Is(err, errs.ErrNotASpace) {
		t.Errorf("err = %v, want errs.ErrNotASpace", err)
	}
}

func TestInitWithoutAStore(t *testing.T) {
	if _, err := Init(t.TempDir(), ""); !errors.Is(err, errs.ErrNotASpace) {
		t.Errorf("err = %v, want errs.ErrNotASpace", err)
	}
}

// TestCacheSurvivesARestart: the cache is on disk, so a new process sees it.
func TestCacheSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")
	if _, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts()); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	reopened, err := Open(space.Root())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cache, err := reopened.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	entry, ok := cache.Lookup("a.bin")
	if !ok {
		t.Fatal("the cache did not survive reopening the space")
	}

	// And the chunk list is remembered too, which is what E16 asks for.
	file, found, err := reopened.FileObject(ctx, entry.File)
	if err != nil {
		t.Fatalf("FileObject: %v", err)
	}
	if !found || len(file.Chunks) == 0 {
		t.Error("the chunk list was not remembered locally (E16)")
	}
}

// TestADamagedCacheIsDiscardedRatherThanTrusted: the cache is a heuristic, so
// losing it costs a scan; trusting half of it could cost data.
func TestADamagedCacheIsDiscardedRatherThanTrusted(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")
	if _, err := space.Snapshot(ctx, &Ignore{}, snapshotOpts()); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if err := os.WriteFile(filepath.Join(space.Root(), SpaceDir, cacheFile),
		[]byte("this is not a cache\n"), 0o644); err != nil {
		t.Fatalf("damage the cache: %v", err)
	}

	cache, err := space.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(cache.Paths()) != 0 {
		t.Errorf("a damaged cache produced %v", cache.Paths())
	}
}

// TestIgnoredDirectoryIsMeasuredWhole checks the reporting E18 requires: a
// user who expects a cleared space and finds gigabytes still on disk has to be
// told why.
func TestIgnoredDirectoryIsMeasuredWhole(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "assets/hero.fbx", "versioned")
	writeFile(t, space.Root(), "cache/a.tmp", strings.Repeat("x", 100))
	writeFile(t, space.Root(), "cache/deep/b.tmp", strings.Repeat("y", 200))
	writeFile(t, space.Root(), IgnoreFile, "/cache/\n")

	ignore, err := LoadIgnore(space.Root())
	if err != nil {
		t.Fatalf("LoadIgnore: %v", err)
	}
	snapshotWith(ctx, t, space, ignore)

	check, err := space.CheckClear(ctx, ignore)
	if err != nil {
		t.Fatalf("CheckClear: %v", err)
	}
	if check.IgnoredBytes != 300 {
		t.Errorf("IgnoredBytes = %d, want 300 from the whole ignored directory", check.IgnoredBytes)
	}
	if len(check.Ignored) != 1 || check.Ignored[0] != "cache" {
		t.Errorf("Ignored = %v, want the directory reported once", check.Ignored)
	}
}

func TestTotalSize(t *testing.T) {
	files := []ScannedFile{{Size: 10}, {Size: 20}, {Size: 5}}

	if got := TotalSize(files); got != 35 {
		t.Errorf("TotalSize = %d, want 35", got)
	}
}

func snapshotOpts() SnapshotOptions {
	return SnapshotOptions{Author: "dennis", Expiry: fixedTime().AddDate(0, 6, 0), Now: fixedTime()}
}
