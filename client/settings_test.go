package client

import (
	"context"
	"testing"
	"time"

	"github.com/dekorlp/fibula/store"
)

// TestAFreshSpacePicksUpTheProjectSettings is F-S5a-01's acceptance condition
// and the reason the setting lives in the store at all (E51): a client that
// joins later has to learn the project's rules without being told them.
func TestAFreshSpacePicksUpTheProjectSettings(t *testing.T) {
	ctx := context.Background()
	first, storeDir := newSpace(t)

	want := store.Settings{Locking: true, LockExpiry: 48 * time.Hour}
	if err := first.SetSettings(ctx, want); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	second, err := Init(t.TempDir(), storeDir)
	if err != nil {
		t.Fatalf("Init a second space: %v", err)
	}

	got, err := second.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !got.Locking {
		t.Error("a freshly initialized space does not know locking is in force")
	}
	if got.Expiry() != want.LockExpiry {
		t.Errorf("expiry = %v, want %v", got.Expiry(), want.LockExpiry)
	}
}

// TestSettingsAreReadFreshEachTime: caching them at open time would let a
// client act on "locking is off" after another one turned it on, which is the
// stale answer that must not be given.
func TestSettingsAreReadFreshEachTime(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)

	if got, err := space.Settings(ctx); err != nil || got.Locking {
		t.Fatalf("a new store should start with locking off: %+v, %v", got, err)
	}

	other, err := Init(t.TempDir(), storeDir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := other.SetSettings(ctx, store.Settings{Locking: true}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	got, err := space.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !got.Locking {
		t.Error("the already-open space is still reporting the old setting")
	}
}

// TestAStoreWithoutSettingsStillOpens: every space created before settings
// existed has to keep working, with locking off.
func TestAStoreWithoutSettingsStillOpens(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	got, err := space.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Locking {
		t.Error("locking is on in a store that never configured it")
	}
	if got.Expiry() != store.DefaultLockExpiry {
		t.Errorf("expiry = %v, want the default", got.Expiry())
	}
}
