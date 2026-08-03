package fs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

func newLocks(t *testing.T) store.LockStore {
	t.Helper()

	dir := t.TempDir()
	if _, err := Create(dir); err != nil {
		t.Fatalf("Create: %v", err)
	}
	locks, err := OpenLocks(dir)
	if err != nil {
		t.Fatalf("OpenLocks: %v", err)
	}
	return locks
}

func fixedNow() time.Time { return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC) }

func TestLockRoundTrip(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	want := store.Lock{
		Path: "levels/hangar.blend", Owner: "anna", Since: fixedNow(), Reason: "retopo",
	}
	if err := locks.Acquire(ctx, want); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	got, err := locks.Get(ctx, want.Path)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Owner != want.Owner || got.Reason != want.Reason || !got.Since.Equal(want.Since) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	if err := locks.Release(ctx, want.Path, "anna"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := locks.Get(ctx, want.Path); !errors.Is(err, errs.ErrLockNotFound) {
		t.Errorf("after release: %v, want ErrLockNotFound", err)
	}
}

// TestASecondAcquireIsRefused is the whole point of a lock.
func TestASecondAcquireIsRefused(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	first := store.Lock{Path: "hero.blend", Owner: "anna", Since: fixedNow()}
	if err := locks.Acquire(ctx, first); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	second := store.Lock{Path: "hero.blend", Owner: "ben", Since: fixedNow()}
	err := locks.Acquire(ctx, second)
	if !errors.Is(err, errs.ErrLockHeld) {
		t.Fatalf("second acquire returned %v, want ErrLockHeld", err)
	}
	// The message has to name who holds it, or the user cannot act on it.
	if got := err.Error(); !strings.Contains(got, "anna") {
		t.Errorf("error %q does not say who holds the lock", got)
	}
}

// TestReleasingSomeoneElsesLockIsRefused: giving up a lock is not the same as
// taking it away, which is what --force is for.
func TestReleasingSomeoneElsesLockIsRefused(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	if err := locks.Acquire(ctx, store.Lock{Path: "hero.blend", Owner: "anna", Since: fixedNow()}); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := locks.Release(ctx, "hero.blend", "ben"); !errors.Is(err, errs.ErrLockHeld) {
		t.Errorf("ben released anna's lock: %v", err)
	}
	if _, err := locks.Get(ctx, "hero.blend"); err != nil {
		t.Errorf("the lock is gone after a refused release: %v", err)
	}
}

// TestReleasingAFreePathIsHarmless: cleaning up twice must not be an error.
func TestReleasingAFreePathIsHarmless(t *testing.T) {
	if err := newLocks(t).Release(context.Background(), "never/locked.png", "anna"); err != nil {
		t.Errorf("releasing a free path: %v", err)
	}
}

// TestTransferRecordsWhoLostIt is what lets the original holder be told on
// their next sync rather than by a person who might forget (E51).
func TestTransferRecordsWhoLostIt(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	if err := locks.Acquire(ctx, store.Lock{
		Path: "hero.blend", Owner: "anna", Since: fixedNow(), Reason: "sculpting",
	}); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	later := fixedNow().Add(72 * time.Hour)
	next, err := locks.Transfer(ctx, "hero.blend", "ben", later)
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if next.Owner != "ben" || next.BrokenFrom != "anna" || !next.BrokenAt.Equal(later) {
		t.Fatalf("transfer produced %+v", next)
	}

	// And it survives a round trip, since that is when anna learns about it.
	got, err := locks.Get(ctx, "hero.blend")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BrokenFrom != "anna" || got.Owner != "ben" {
		t.Errorf("stored lock = %+v", got)
	}
}

func TestListIsOrderedByPath(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	for _, p := range []string{"z.png", "assets/a.png", "m.png"} {
		if err := locks.Acquire(ctx, store.Lock{Path: p, Owner: "anna", Since: fixedNow()}); err != nil {
			t.Fatalf("Acquire %s: %v", p, err)
		}
	}

	got, err := locks.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"assets/a.png", "m.png", "z.png"}
	if len(got) != len(want) {
		t.Fatalf("got %d locks, want %d", len(got), len(want))
	}
	for i, lock := range got {
		if lock.Path != want[i] {
			t.Errorf("lock %d = %q, want %q", i, lock.Path, want[i])
		}
	}
}

// TestConcurrentAcquireHasExactlyOneWinner: two people reaching for the same
// file at the same moment is the situation the lock exists for, and O_EXCL is
// what makes the answer atomic (E50).
func TestConcurrentAcquireHasExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	const contenders = 8
	var wg sync.WaitGroup
	results := make([]error, contenders)

	for i := range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = locks.Acquire(ctx, store.Lock{
				Path: "contested.blend", Owner: "artist", Since: fixedNow(),
			})
		}()
	}
	wg.Wait()

	won := 0
	for i, err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, errs.ErrLockHeld):
			t.Errorf("contender %d failed with %v, want ErrLockHeld", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d contenders acquired the same lock, want exactly one", won)
	}
}

// TestLockPathsAreValidated: the path comes from a client, and a lock on
// "../../etc/passwd" would otherwise write outside the store.
func TestLockPathsAreValidated(t *testing.T) {
	ctx := context.Background()
	locks := newLocks(t)

	for _, p := range []string{"../escape.txt", "/absolute.txt", ""} {
		err := locks.Acquire(ctx, store.Lock{Path: p, Owner: "anna", Since: fixedNow()})
		if !errors.Is(err, errs.ErrUnsafePath) && err == nil {
			t.Errorf("acquiring %q was allowed", p)
		}
	}
}

func TestExpiry(t *testing.T) {
	lock := store.Lock{Since: fixedNow()}

	if lock.Expired(fixedNow().Add(13*24*time.Hour), store.DefaultLockExpiry) {
		t.Error("a 13-day-old lock counts as expired under a 14-day deadline")
	}
	if !lock.Expired(fixedNow().Add(15*24*time.Hour), store.DefaultLockExpiry) {
		t.Error("a 15-day-old lock does not count as expired")
	}
}
