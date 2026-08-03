package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
	"github.com/dekorlp/fibula/store/fs"
)

func openGC(t *testing.T, storeDir string) store.GCStore {
	t.Helper()

	gc, err := fs.OpenGC(storeDir)
	if err != nil {
		t.Fatalf("OpenGC: %v", err)
	}
	return gc
}

// noGrace disables the grace window, which only a test may ask for: every
// object it writes is deliberately older than any real upload would be.
func noGrace(now time.Time) GCOptions { return GCOptions{Grace: -1, Now: now} }

// TestExpiringASnapshotKeepsAReachableVersionsChunks is E14's hard coupling,
// and the reason it needs no special case: the deliberate version is a
// reachability root of its own, so the walk keeps its chunks alive whatever
// happens to the snapshot that once shared them.
func TestExpiringASnapshotKeepsAReachableVersionsChunks(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	content := strings.Repeat("shared asset content ", 500)
	writeFile(t, space.Root(), "assets/hero.fbx", content)

	// A snapshot and a deliberate version over the same content.
	snapshot(ctx, t, space)
	commit(ctx, t, space, "keep this state")

	// Drop the snapshot from the timeline, then collect.
	expireEverything(ctx, t, space)

	before := readAll(t, space, "assets/hero.fbx")
	result, err := Collect(ctx, openGC(t, storeDir), space.Refs(), storeDir, noGrace(fixedTime().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if result.Deleted == 0 {
		t.Error("the expired snapshot object was not collected")
	}

	// The content is still recoverable from the deliberate version.
	if _, err := space.Checkout(ctx, &Ignore{}, store.DefaultRef, clearOpts()); err != nil {
		t.Fatalf("checkout after GC: %v", err)
	}
	if got := readAll(t, space, "assets/hero.fbx"); got != before {
		t.Error("the content of a reachable deliberate version did not survive garbage collection")
	}
}

// TestAManifestReachableOnlyThroughASnapshotKeepsItsChunks: the safety net
// only works if the snapshot alone is enough to keep content alive.
func TestAManifestReachableOnlyThroughASnapshotKeepsItsChunks(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "assets/only-in-a-snapshot.bin", strings.Repeat("x", 5000))
	snapshot(ctx, t, space)

	// No commit at all: the snapshot is the only thing holding this content.
	result, err := Collect(ctx, openGC(t, storeDir), space.Refs(), storeDir, noGrace(fixedTime().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("garbage collection deleted %d objects that a snapshot references", result.Deleted)
	}

	if _, err := space.Clear(ctx, &Ignore{}, clearOpts()); err != nil {
		t.Fatalf("Clear after GC: %v", err)
	}
	if _, err := space.Restore(ctx); err != nil {
		t.Fatalf("Restore after GC: %v", err)
	}
	assertContent(t, space, "assets/only-in-a-snapshot.bin", strings.Repeat("x", 5000))
}

// TestAnObjectWrittenDuringACollectionSurvives is the race the grace period
// exists for: a client that has written its chunks but not yet the manifest
// naming them looks exactly like a client that wrote garbage.
func TestAnObjectWrittenDuringACollectionSurvives(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "content")
	commit(ctx, t, space, "the state before the upload")

	// A chunk written but not yet referenced by anything.
	inFlight := []byte(strings.Repeat("mid-upload ", 100))
	gc := openGC(t, storeDir)
	if err := gc.Put(ctx, store.ChunkKey(hash.Chunk(inFlight)), inFlight); err != nil {
		t.Fatalf("Put: %v", err)
	}

	result, err := Collect(ctx, gc, space.Refs(), storeDir, GCOptions{Now: fixedTime()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if result.Spared == 0 {
		t.Error("the in-flight chunk was not spared by the grace window")
	}
	if _, err := gc.Get(ctx, store.ChunkKey(hash.Chunk(inFlight))); err != nil {
		t.Errorf("the in-flight chunk was deleted: %v", err)
	}

	// Once the grace window has passed and nothing references it, it goes.
	later := time.Now().Add(2 * GraceDefault)
	result, err = Collect(ctx, gc, space.Refs(), storeDir, GCOptions{Now: later})
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if result.Deleted == 0 {
		t.Error("the abandoned chunk was never collected")
	}
}

// TestCollectDeletesOnlyWhatNothingReferences is the invariant in its plainest
// form (CLAUDE.md invariant 8).
func TestCollectDeletesOnlyWhatNothingReferences(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)

	writeFile(t, space.Root(), "keep.bin", strings.Repeat("keep ", 400))
	commit(ctx, t, space, "the state to keep")

	// Content that only ever existed in a snapshot which is then expired.
	writeFile(t, space.Root(), "throwaway.bin", strings.Repeat("throwaway ", 400))
	snapshot(ctx, t, space)
	if err := os.Remove(filepath.Join(space.Root(), "throwaway.bin")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	commit(ctx, t, space, "without the throwaway")
	expireEverything(ctx, t, space)

	gc := openGC(t, storeDir)
	result, err := Collect(ctx, gc, space.Refs(), storeDir, noGrace(fixedTime().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if result.Deleted == 0 {
		t.Error("nothing was collected although a snapshot was expired")
	}

	// Everything the deliberate history needs is still there.
	if _, err := space.Clear(ctx, &Ignore{}, clearOpts()); err != nil {
		t.Fatalf("Clear after GC: %v", err)
	}
	if _, err := space.Restore(ctx); err != nil {
		t.Fatalf("Restore after GC: %v", err)
	}
	assertContent(t, space, "keep.bin", strings.Repeat("keep ", 400))
}

// TestDryRunDeletesNothing: the safe way to look at what a collection would do.
func TestDryRunDeletesNothing(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")
	snapshot(ctx, t, space)
	expireEverything(ctx, t, space)

	gc := openGC(t, storeDir)
	dry := noGrace(fixedTime().Add(time.Hour))
	dry.DryRun = true

	first, err := Collect(ctx, gc, space.Refs(), storeDir, dry)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if first.Deleted == 0 {
		t.Fatal("the dry run found nothing to report")
	}

	second, err := Collect(ctx, gc, space.Refs(), storeDir, dry)
	if err != nil {
		t.Fatalf("second dry run: %v", err)
	}
	if second.Deleted != first.Deleted {
		t.Errorf("the dry run changed the store: %d then %d", first.Deleted, second.Deleted)
	}
}

func TestRetentionSchedule(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	r := DefaultRetention()

	var timeline []Snapshot
	add := func(at time.Time) { timeline = append(timeline, Snapshot{Time: at}) }

	// Three within the same hour: only the newest survives.
	add(now.Add(-10 * time.Minute))
	add(now.Add(-20 * time.Minute))
	add(now.Add(-30 * time.Minute))
	// Two on the same day, outside the hourly window: one survives.
	add(now.Add(-30 * time.Hour))
	add(now.Add(-34 * time.Hour))
	// Two in the same ISO week, outside the daily window: one survives.
	// 40 and 41 days back are Tuesday and Monday of ISO week 2026-26; 42 days
	// would already be the Sunday belonging to the week before.
	add(now.Add(-40 * 24 * time.Hour))
	add(now.Add(-41 * 24 * time.Hour))
	// Older than the weekly window: dropped outright.
	add(now.Add(-200 * 24 * time.Hour))

	keep, drop := r.Retain(timeline, now)

	if len(keep) != 3 {
		t.Errorf("kept %d snapshots, want 3 (one per hour, day and week bucket)", len(keep))
	}
	if len(drop) != 5 {
		t.Errorf("dropped %d snapshots, want 5", len(drop))
	}
	for i := 1; i < len(keep); i++ {
		if keep[i].Time.Before(keep[i-1].Time) {
			t.Error("the kept snapshots are not in chronological order")
		}
	}
}

// TestRetentionKeepsTheNewestInABucket: when only one state per day can be
// kept, the most recent one is the most useful.
func TestRetentionKeepsTheNewestInABucket(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	// Both on 2026-08-01, so they compete for the same daily bucket.
	older := now.Add(-35 * time.Hour)
	newer := now.Add(-30 * time.Hour)

	keep, drop := DefaultRetention().Retain([]Snapshot{{Time: older}, {Time: newer}}, now)

	if len(keep) != 1 || !keep[0].Time.Equal(newer) {
		t.Errorf("kept %v, want the newer snapshot %v", keep, newer)
	}
	if len(drop) != 1 || !drop[0].Time.Equal(older) {
		t.Errorf("dropped %v, want the older snapshot", drop)
	}
}

// TestASnapshotFromTheFutureIsNotDeleted: a clock jump is not a reason to
// throw away a safety net.
func TestASnapshotFromTheFutureIsNotDeleted(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	keep, drop := DefaultRetention().Retain([]Snapshot{{Time: now.Add(48 * time.Hour)}}, now)

	if len(keep) != 1 || len(drop) != 0 {
		t.Errorf("kept %d and dropped %d, want the future snapshot kept", len(keep), len(drop))
	}
}

func TestExpireRemovesRefsOnly(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")
	snapshot(ctx, t, space)

	timeline, err := space.Timeline(ctx)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("timeline has %d entries, want 1", len(timeline))
	}
	versionID := timeline[0].Version

	result, err := space.Expire(ctx, DefaultRetention(), fixedTime().AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(result.Expired) != 1 {
		t.Errorf("expired %d snapshots, want 1", len(result.Expired))
	}

	// The ref is gone; the object is not, because removing objects is garbage
	// collection's job and not expiry's.
	after, err := space.Timeline(ctx)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("timeline still has %d entries", len(after))
	}
	objects, err := fs.Open(storeDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := objects.Get(ctx, store.VersionKey(versionID)); err != nil {
		t.Errorf("expiry removed the version object itself: %v", err)
	}
}

func expireEverything(ctx context.Context, t *testing.T, space *Space) {
	t.Helper()

	if _, err := space.Expire(ctx, DefaultRetention(), fixedTime().AddDate(1, 0, 0)); err != nil {
		t.Fatalf("Expire: %v", err)
	}
}

func commit(ctx context.Context, t *testing.T, space *Space, message string) hash.VersionID {
	t.Helper()

	result, err := space.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author: "dennis", Message: message, Now: fixedTime(),
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return result.Version
}

func clearOpts() ClearOptions {
	return ClearOptions{Author: "dennis", Now: fixedTime()}
}

func readAll(t *testing.T, space *Space, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(space.Root(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestPromotionDoesNotMoveTheCurrentRef is the correction a dry run produced.
// E12 says "I'll keep that one" - make permanent, not revert to. Advancing
// main would swap the working state out from under whoever ran the command and
// silently drop newer work from the tip.
func TestPromotionDoesNotMoveTheCurrentRef(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "a.bin", "first")
	snapshot(ctx, t, space)
	timeline, err := space.Timeline(ctx)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	writeFile(t, space.Root(), "b.bin", "second")
	tip := commit(ctx, t, space, "the current tip")

	promoted, err := space.Promote(ctx, timeline[0].Version, SnapshotOptions{
		Author: "dennis", Message: "that state was good", Now: fixedTime(),
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// main is untouched.
	current, exists, err := space.refValue(ctx, store.DefaultRef)
	if err != nil || !exists {
		t.Fatalf("read main: %v", err)
	}
	if current != tip {
		t.Errorf("main moved from %s to %s during promotion", tip, current)
	}

	// The promoted version is a deliberate one and is reachable, so it
	// survives expiry and collection.
	version, err := space.readVersion(ctx, promoted.Version)
	if err != nil {
		t.Fatalf("read promoted version: %v", err)
	}
	if !version.Expiry.IsZero() {
		t.Error("the promoted version still carries an expiry")
	}
	if version.Message == "" {
		t.Error("the promoted version has no message")
	}
}

// TestPromotingADeliberateVersionIsRejected: there is nothing to promote, and
// silently making a second copy would just clutter the ref namespace.
func TestPromotingADeliberateVersionIsRejected(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")
	tip := commit(ctx, t, space, "a deliberate version")

	_, err := space.Promote(ctx, tip, SnapshotOptions{
		Author: "dennis", Message: "again", Now: fixedTime(),
	})
	if err == nil {
		t.Error("promoting a deliberate version was accepted")
	}
}
