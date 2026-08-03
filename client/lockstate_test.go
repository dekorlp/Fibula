package client

import (
	"testing"
	"time"

	"github.com/dekorlp/fibula/store"
)

// TestALostLockIsNoticedByTheTool is E51's promise: the holder finds out
// without anyone remembering to tell them.
func TestALostLockIsNoticedByTheTool(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if lost, err := space.LostLocks(ctx, "anna"); err != nil || len(lost) != 0 {
		t.Fatalf("a freshly taken lock reads as lost: %+v, %v", lost, err)
	}

	// Ben takes it over while anna is away.
	req := lockReq("hero.blend", "ben")
	req.Force = true
	if _, err := space.Lock(ctx, &Ignore{}, req); err != nil {
		t.Fatalf("forced Lock: %v", err)
	}

	lost, err := space.LostLocks(ctx, "anna")
	if err != nil {
		t.Fatalf("LostLocks: %v", err)
	}
	if len(lost) != 1 || lost[0].Path != "hero.blend" || lost[0].Holder != "ben" {
		t.Fatalf("lost locks = %+v", lost)
	}
	if lost[0].BrokenFrom != "anna" {
		t.Errorf("the loss does not record that it was taken from anna: %+v", lost[0])
	}
}

// TestALossIsReportedOnceRatherThanForever: an unread notice is useful, a
// permanent one is noise.
func TestALossIsReportedOnceRatherThanForever(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	req := lockReq("hero.blend", "ben")
	req.Force = true
	if _, err := space.Lock(ctx, &Ignore{}, req); err != nil {
		t.Fatalf("forced Lock: %v", err)
	}

	lost, err := space.LostLocks(ctx, "anna")
	if err != nil || len(lost) != 1 {
		t.Fatalf("first check: %+v, %v", lost, err)
	}
	if err := space.ForgetLostLocks(lost); err != nil {
		t.Fatalf("ForgetLostLocks: %v", err)
	}

	again, err := space.LostLocks(ctx, "anna")
	if err != nil {
		t.Fatalf("LostLocks: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("the same loss is reported twice: %+v", again)
	}
}

// TestReleasingALockStopsTrackingIt: giving one up deliberately is not a loss.
func TestReleasingALockStopsTrackingIt(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := space.Unlock(ctx, &Ignore{}, "hero.blend", "anna", false); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	lost, err := space.LostLocks(ctx, "anna")
	if err != nil {
		t.Fatalf("LostLocks: %v", err)
	}
	if len(lost) != 0 {
		t.Errorf("a deliberately released lock is reported as lost: %+v", lost)
	}
}

// TestAnExpiredTakeoverIsAlsoNoticed: expiry is the other way a lock changes
// hands, and it needs no force at all.
func TestAnExpiredTakeoverIsAlsoNoticed(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	if err := space.SetSettings(ctx, store.Settings{Locking: true, LockExpiry: time.Hour}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if _, err := space.Lock(ctx, &Ignore{}, lockReq("hero.blend", "anna")); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	later := lockReq("hero.blend", "ben")
	later.Now = fixedTime().Add(2 * time.Hour)
	if _, err := space.Lock(ctx, &Ignore{}, later); err != nil {
		t.Fatalf("takeover: %v", err)
	}

	lost, err := space.LostLocks(ctx, "anna")
	if err != nil {
		t.Fatalf("LostLocks: %v", err)
	}
	if len(lost) != 1 || lost[0].Holder != "ben" {
		t.Errorf("an expired takeover was not noticed: %+v", lost)
	}
}

// TestNoLocksMeansNoWork: the common case must not read state that is not
// there.
func TestNoLocksMeansNoWork(t *testing.T) {
	space, _, ctx := lockingSpace(t)

	lost, err := space.LostLocks(ctx, "anna")
	if err != nil {
		t.Fatalf("LostLocks on a space that never locked anything: %v", err)
	}
	if len(lost) != 0 {
		t.Errorf("lost locks = %+v, want none", lost)
	}
}

// TestTheAuthorComesFromTheSpaceConfig: a shared machine account would
// otherwise make two people indistinguishable to every lock in the project.
func TestTheAuthorComesFromTheSpaceConfig(t *testing.T) {
	space, storeDir := newSpace(t)

	config := space.Config()
	if config.Author != "" {
		t.Fatalf("a fresh space already has an author: %q", config.Author)
	}
	config.Author = "anna"
	if err := writeConfig(space.stateDir(), config); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}

	reopened, err := Open(space.Root())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := reopened.Config().Author; got != "anna" {
		t.Errorf("author = %q, want anna", got)
	}
	_ = storeDir
}
