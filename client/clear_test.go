package client

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/errs"
)

// TestClearLeavesIgnoredFilesAlone pins E18: ignored files stay in place, do
// not block, and are reported. Deleting them silently would be data loss on
// exactly the files that were never in the store.
func TestClearLeavesIgnoredFilesAlone(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "versioned")
	writeFile(t, space.Root(), "assets/hero.blend1", "a backup nobody asked for")
	writeFile(t, space.Root(), IgnoreFile, "*.blend1\n")

	ignore, err := LoadIgnore(space.Root())
	if err != nil {
		t.Fatalf("LoadIgnore: %v", err)
	}
	snapshotWith(ctx, t, space, ignore)

	result, err := space.Clear(ctx, ignore, ClearOptions{Author: "dennis", Now: fixedTime()})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if result.Ignored != 1 || result.IgnoredBytes == 0 {
		t.Errorf("Ignored = %d files / %d bytes, want the backup reported", result.Ignored, result.IgnoredBytes)
	}
	assertContent(t, space, "assets/hero.blend1", "a backup nobody asked for")
	assertGone(t, space, "assets/hero.fbx")
}

func TestClearWithIncludeIgnoredDeletesThemToo(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "versioned")
	writeFile(t, space.Root(), "assets/hero.blend1", "a backup")
	writeFile(t, space.Root(), IgnoreFile, "*.blend1\n")

	ignore, err := LoadIgnore(space.Root())
	if err != nil {
		t.Fatalf("LoadIgnore: %v", err)
	}
	snapshotWith(ctx, t, space, ignore)

	if _, err := space.Clear(ctx, ignore, ClearOptions{
		Author: "dennis", IncludeIgnored: true, Now: fixedTime(),
	}); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	assertGone(t, space, "assets/hero.fbx", "assets/hero.blend1")
}

// TestClearKeepsTheSpaceIdentity: clearing deletes assets and keeps metadata,
// so the space still knows where it stands and restore is a pure download
// (E15).
func TestClearKeepsTheSpaceIdentity(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "content")
	writeFile(t, space.Root(), "levels/level_01.blend", "level")
	snapshot(ctx, t, space)

	headBefore, err := space.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	if _, err := space.Clear(ctx, &Ignore{}, ClearOptions{Author: "dennis", Now: fixedTime()}); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	headAfter, err := space.Head()
	if err != nil {
		t.Fatalf("Head after clear: %v", err)
	}
	if headAfter.Version != headBefore.Version {
		t.Errorf("the head moved during clear: %s then %s", headBefore.Version, headAfter.Version)
	}
	if _, err := space.CheckedOutManifest(); err != nil {
		t.Errorf("the manifest did not survive clearing: %v", err)
	}

	result, err := space.Restore(ctx)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if result.Files != 2 {
		t.Errorf("restored %d files, want 2", result.Files)
	}
	assertContent(t, space, "assets/hero.fbx", "content")
	assertContent(t, space, "levels/level_01.blend", "level")
}

// TestClearRefusesToDeleteWhenTheCheckFails is the belt to the braces: even if
// a caller ignores the check result, Clear runs it again itself.
func TestClearRefusesToDeleteWhenTheCheckFails(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", strings.Repeat("mesh ", 500))
	snapshot(ctx, t, space)

	removeOneChunk(t, storeDir)

	_, err := space.Clear(ctx, &Ignore{}, ClearOptions{Author: "dennis", Now: fixedTime()})
	if !errors.Is(err, errs.ErrDirty) {
		t.Errorf("err = %v, want errs.ErrDirty", err)
	}
	assertNothingDeleted(t, space, "assets/hero.fbx")
}
