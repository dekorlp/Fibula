package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

func lockingSpace(t *testing.T) (*Space, string, context.Context) {
	t.Helper()
	ctx := context.Background()

	space, storeDir := newSpace(t)
	if err := space.SetSettings(ctx, store.Settings{Locking: true}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	writeFile(t, space.Root(), "hero.blend", "hero")
	writeFile(t, space.Root(), "levels/hangar.blend", "hangar")
	writeFile(t, space.Root(), "levels/dock.blend", "dock")
	return space, storeDir, ctx
}

func lockReq(pattern, owner string) LockRequest {
	return LockRequest{Pattern: pattern, Owner: owner, Now: fixedTime()}
}

// TestLockingMustBeEnabled: writing locks nobody will honour is worse than
// refusing, because the user would believe they were protected (E48).
func TestLockingMustBeEnabled(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "hero.blend", "hero")

	_, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna"))
	if !errors.Is(err, errs.ErrLockingDisabled) {
		t.Fatalf("locking on a project without it enabled returned %v", err)
	}
}

// TestAPatternLocksEachMatchingFile covers E50: no directory locks, one lock
// per file, so there is never a question about what a new file inherits.
func TestAPatternLocksEachMatchingFile(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	taken, err := space.Lock(ctx, &Ignore{}, lockReq("levels/**", "anna"))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if len(taken) != 2 {
		t.Fatalf("took %d locks, want one per matching file: %+v", len(taken), taken)
	}

	all, err := space.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks: %v", err)
	}
	if len(all) != 2 || all[0].Path != "levels/dock.blend" || all[1].Path != "levels/hangar.blend" {
		t.Errorf("stored locks = %+v", all)
	}
}

func TestAPatternThatMatchesNothingIsAnError(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("nothing/here/**", "anna")); err == nil {
		t.Error("a pattern matching no file was accepted")
	}
}

// TestSomeoneElsesLockIsRefusedWithoutForce is the reservation doing its job.
func TestSomeoneElsesLockIsRefusedWithoutForce(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "ben")); !errors.Is(err, errs.ErrLockHeld) {
		t.Fatalf("ben took anna's lock without force: %v", err)
	}
}

// TestForceTakesOverAndRecordsWhoLostIt is the ill-colleague case (E51): the
// lock changes hands and the tool remembers, so anna is told rather than having
// to be told.
func TestForceTakesOverAndRecordsWhoLostIt(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	req := lockReq("hero.blend", "ben")
	req.Force = true
	taken, err := space.Lock(ctx, &Ignore{}, req)
	if err != nil {
		t.Fatalf("forced Lock: %v", err)
	}
	if len(taken) != 1 || taken[0].BrokenFrom != "anna" || taken[0].Owner != "ben" {
		t.Fatalf("forced lock = %+v", taken)
	}
}

// TestAnExpiredLockNeedsNoForce: the answer to a holder who is gone should not
// require a flag that also works on a live lock.
func TestAnExpiredLockNeedsNoForce(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if err := space.SetSettings(ctx, store.Settings{Locking: true, LockExpiry: time.Hour}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	later := lockReq("hero.blend", "ben")
	later.Now = fixedTime().Add(2 * time.Hour)
	taken, err := space.Lock(ctx, &Ignore{}, later)
	if err != nil {
		t.Fatalf("taking over an expired lock needed force: %v", err)
	}
	if taken[0].BrokenFrom != "anna" {
		t.Errorf("the takeover did not record who lost it: %+v", taken[0])
	}
}

func TestUnlockOnlyReleasesYourOwn(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.Unlock(ctx, &Ignore{}, "hero.blend", "ben", false); !errors.Is(err, errs.ErrLockHeld) {
		t.Fatalf("ben released anna's lock: %v", err)
	}

	released, err := space.Unlock(ctx, &Ignore{}, "hero.blend", "ben", true)
	if err != nil {
		t.Fatalf("forced Unlock: %v", err)
	}
	if released != 1 {
		t.Errorf("released %d, want 1", released)
	}
}

// TestLockedBySomeoneElseSkipsYoursAndExpiredOnes is what the commit check and
// the read-only attribute will both be built on.
func TestLockedBySomeoneElseSkipsYoursAndExpiredOnes(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if err := space.SetSettings(ctx, store.Settings{Locking: true, LockExpiry: time.Hour}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("levels/dock.blend", "ben")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	foreign, err := space.LockedBySomeoneElse(ctx, "anna", fixedTime())
	if err != nil {
		t.Fatalf("LockedBySomeoneElse: %v", err)
	}
	if len(foreign) != 1 || foreign[0].Owner != "ben" {
		t.Fatalf("foreign locks = %+v, want just ben's", foreign)
	}

	// Once everything has expired, nothing is foreign any more.
	foreign, err = space.LockedBySomeoneElse(ctx, "anna", fixedTime().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("LockedBySomeoneElse: %v", err)
	}
	if len(foreign) != 0 {
		t.Errorf("expired locks still count as foreign: %+v", foreign)
	}
}

// TestLockedBySomeoneElseIsEmptyWhenLockingIsOff: a project that never turned
// it on must not be affected by locks left in the store.
func TestLockedBySomeoneElseIsEmptyWhenLockingIsOff(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "ben")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := space.SetSettings(ctx, store.Settings{Locking: false}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	foreign, err := space.LockedBySomeoneElse(ctx, "anna", fixedTime())
	if err != nil {
		t.Fatalf("LockedBySomeoneElse: %v", err)
	}
	if len(foreign) != 0 {
		t.Errorf("locks apply with locking switched off: %+v", foreign)
	}
}
