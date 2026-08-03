package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

// commitAs runs a commit under a given author, which is what decides whose
// locks apply.
func commitAs(ctx context.Context, space *Space, author, message string) error {
	_, err := space.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author: author, Message: message, Now: fixedTime(),
	})
	return err
}

// TestCommitIsRefusedOnAFileSomeoneElseHolds is the enforcement (E49). Without
// it a lock stops nothing but another lock.
func TestCommitIsRefusedOnAFileSomeoneElseHolds(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "ben", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	writeFile(t, space.Root(), "hero.blend", "ben's edit")
	err := commitAs(ctx, space, "ben", "ben edits the hero")
	if !errors.Is(err, errs.ErrLockHeld) {
		t.Fatalf("commit returned %v, want ErrLockHeld", err)
	}
	// The message has to name the file and the holder, or it cannot be acted on.
	for _, want := range []string{"hero.blend", "anna"} {
		if got := err.Error(); !strings.Contains(got, want) {
			t.Errorf("error %q does not mention %q", got, want)
		}
	}
}

// TestCommitIsAllowedOnFilesNobodyHolds: a reservation on one file must not
// block the rest of the project, or nobody could commit anything.
func TestCommitIsAllowedOnFilesNobodyHolds(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "ben", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	writeFile(t, space.Root(), "levels/dock.blend", "ben's edit elsewhere")
	if err := commitAs(ctx, space, "ben", "ben edits an unlocked file"); err != nil {
		t.Fatalf("a commit touching no locked file was refused: %v", err)
	}
}

// TestYourOwnLockDoesNotBlockYou: the point of taking a lock is to work on the
// file.
func TestYourOwnLockDoesNotBlockYou(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "anna", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	writeFile(t, space.Root(), "hero.blend", "anna's edit")
	if err := commitAs(ctx, space, "anna", "anna edits her own locked file"); err != nil {
		t.Fatalf("anna was blocked by her own lock: %v", err)
	}
}

// TestASnapshotIsNeverBlocked: the safety net has to work when things are going
// wrong, and a contested file is exactly when a snapshot matters most (E12).
func TestASnapshotIsNeverBlocked(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "ben", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	writeFile(t, space.Root(), "hero.blend", "ben's edit")
	if _, err := space.Snapshot(ctx, &Ignore{}, SnapshotOptions{
		Author: "ben", Now: fixedTime(),
	}); err != nil {
		t.Fatalf("a snapshot was blocked by a foreign lock: %v", err)
	}
}

// TestDeletingALockedFileIsRefused: removing what somebody reserved is the same
// interference as changing it.
func TestDeletingALockedFileIsRefused(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "ben", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if err := os.Remove(filepath.Join(space.Root(), "hero.blend")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := commitAs(ctx, space, "ben", "ben deletes it"); !errors.Is(err, errs.ErrLockHeld) {
		t.Fatalf("deleting a locked file returned %v, want ErrLockHeld", err)
	}
}

// TestAnExpiredLockDoesNotBlockACommit: an expired lock may be taken over by
// anyone, so it cannot keep refusing work either.
func TestAnExpiredLockDoesNotBlockACommit(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "ben", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	writeFile(t, space.Root(), "hero.blend", "ben's edit")
	_, err := space.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author:  "ben",
		Message: "ben edits it much later",
		Now:     fixedTime().AddDate(0, 0, 30), // past the 14-day default
	})
	if err != nil {
		t.Fatalf("an expired lock still blocked a commit: %v", err)
	}
}

// TestLocksDoNotApplyWhenLockingIsOff: switching the feature off must not leave
// stale locks refusing work.
func TestLocksDoNotApplyWhenLockingIsOff(t *testing.T) {
	space, _, ctx := lockingSpace(t)
	if err := commitAs(ctx, space, "ben", "baseline"); err != nil {
		t.Fatalf("baseline commit: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := space.SetSettings(ctx, store.Settings{Locking: false}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	writeFile(t, space.Root(), "hero.blend", "ben's edit")
	if err := commitAs(ctx, space, "ben", "ben edits it"); err != nil {
		t.Fatalf("a lock applied with locking switched off: %v", err)
	}
}
