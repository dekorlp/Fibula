package client

import (
	"os"
	"path/filepath"
	"testing"
)

func permOf(t *testing.T, space *Space, rel string) os.FileMode {
	t.Helper()

	info, err := os.Stat(filepath.Join(space.Root(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	return info.Mode().Perm()
}

func isWritable(t *testing.T, space *Space, rel string) bool {
	t.Helper()
	return permOf(t, space, rel)&0o200 != 0
}

// TestAForeignLockMarksTheFileReadOnly is E49's early warning: the DCC tool
// refuses to save, so the conflict is discovered while working rather than at
// commit time.
func TestAForeignLockMarksTheFileReadOnly(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	marked, err := space.ApplyLockAttributes(ctx, &Ignore{}, "ben", fixedTime())
	if err != nil {
		t.Fatalf("ApplyLockAttributes: %v", err)
	}
	if marked != 1 {
		t.Fatalf("marked %d files, want 1", marked)
	}
	if isWritable(t, space, "hero.blend") {
		t.Error("a file held by someone else is still writable")
	}
	if !isWritable(t, space, "levels/dock.blend") {
		t.Error("an unlocked file was marked read-only")
	}
}

// TestYourOwnLockLeavesTheFileWritable: the point of taking a lock is to work
// on the file.
func TestYourOwnLockLeavesTheFileWritable(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.ApplyLockAttributes(ctx, &Ignore{}, "anna", fixedTime()); err != nil {
		t.Fatalf("ApplyLockAttributes: %v", err)
	}
	if !isWritable(t, space, "hero.blend") {
		t.Error("anna cannot write the file she reserved")
	}
}

// TestReleasingALockRestoresWritability: the attribute has to follow the lock,
// or a released file stays unusable until somebody notices.
func TestReleasingALockRestoresWritability(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.ApplyLockAttributes(ctx, &Ignore{}, "ben", fixedTime()); err != nil {
		t.Fatalf("ApplyLockAttributes: %v", err)
	}
	if isWritable(t, space, "hero.blend") {
		t.Fatal("the setup did not mark the file")
	}

	if _, err := space.Unlock(ctx, &Ignore{}, "hero.blend", "anna", false); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if _, err := space.ApplyLockAttributes(ctx, &Ignore{}, "ben", fixedTime()); err != nil {
		t.Fatalf("ApplyLockAttributes: %v", err)
	}
	if !isWritable(t, space, "hero.blend") {
		t.Error("the file is still read-only after the lock was released")
	}
}

// TestLockingOffTouchesNothing: reaching into a working directory to change
// permissions Fibula never set would be gratuitous.
func TestLockingOffTouchesNothing(t *testing.T) {
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "hero.blend", "hero")

	abs := filepath.Join(space.Root(), "hero.blend")
	if err := os.Chmod(abs, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	marked, err := space.ApplyLockAttributes(t.Context(), &Ignore{}, "anna", fixedTime())
	if err != nil {
		t.Fatalf("ApplyLockAttributes: %v", err)
	}
	if marked != 0 {
		t.Errorf("marked %d files with locking switched off", marked)
	}
	if isWritable(t, space, "hero.blend") {
		t.Error("a file the user made read-only was made writable again")
	}
}

// TestClearDeletesReadOnlyFiles is the Windows case the backlog called out: a
// read-only file cannot be removed there, and this path runs through the dirty
// check - failing would leave the user believing the space was cleared when it
// was not (E17).
func TestClearDeletesReadOnlyFiles(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "anna", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.ApplyLockAttributes(ctx, &Ignore{}, "ben", fixedTime()); err != nil {
		t.Fatalf("ApplyLockAttributes: %v", err)
	}
	if isWritable(t, space, "hero.blend") {
		t.Fatal("the setup did not mark the file read-only")
	}

	if _, err := space.Clear(ctx, &Ignore{}, clearOpts()); err != nil {
		t.Fatalf("Clear with a read-only file present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(space.Root(), "hero.blend")); !os.IsNotExist(err) {
		t.Error("the read-only file survived the clear")
	}
}

// TestApplyingTwiceChangesNothing: a scan over an unchanged tree must not
// rewrite permissions on every command.
func TestApplyingTwiceChangesNothing(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.ApplyLockAttributes(ctx, &Ignore{}, "ben", fixedTime()); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	marked, err := space.ApplyLockAttributes(ctx, &Ignore{}, "ben", fixedTime())
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if marked != 0 {
		t.Errorf("the second apply changed %d files, want none", marked)
	}
}
