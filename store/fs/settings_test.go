package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

func newSettings(t *testing.T) (store.SettingsStore, string) {
	t.Helper()

	dir := t.TempDir()
	if _, err := Create(dir); err != nil {
		t.Fatalf("Create: %v", err)
	}
	settings, err := OpenSettings(dir)
	if err != nil {
		t.Fatalf("OpenSettings: %v", err)
	}
	return settings, dir
}

// TestAStoreWithoutSettingsHasLockingOff is the compatibility case: every store
// written before settings existed must keep working, and it must not suddenly
// start enforcing locks nobody configured (E48).
func TestAStoreWithoutSettingsHasLockingOff(t *testing.T) {
	ctx := context.Background()
	settings, dir := newSettings(t)

	if _, err := os.Stat(filepath.Join(dir, settingsFile)); !os.IsNotExist(err) {
		t.Fatalf("a fresh store already has a settings file")
	}

	got, err := settings.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Locking {
		t.Error("locking is on in a store that never configured it")
	}
	if got.Expiry() != store.DefaultLockExpiry {
		t.Errorf("expiry = %v, want the default %v", got.Expiry(), store.DefaultLockExpiry)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	settings, _ := newSettings(t)

	want := store.Settings{Locking: true, LockExpiry: 36 * time.Hour}
	if err := settings.SetSettings(ctx, want); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	got, err := settings.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Locking != want.Locking || got.Expiry() != want.LockExpiry {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestSettingsSurviveAReopen: the value belongs to the store, so a second
// client opening the same directory has to see it (E51).
func TestSettingsSurviveAReopen(t *testing.T) {
	ctx := context.Background()
	settings, dir := newSettings(t)

	if err := settings.SetSettings(ctx, store.Settings{Locking: true}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	second, err := OpenSettings(dir)
	if err != nil {
		t.Fatalf("OpenSettings again: %v", err)
	}
	got, err := second.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !got.Locking {
		t.Error("a second client does not see the project's locking setting")
	}
}

// TestUnknownSettingsAreIgnored: a newer client writing a field this one does
// not know must not make the store unreadable.
func TestUnknownSettingsAreIgnored(t *testing.T) {
	ctx := context.Background()
	settings, dir := newSettings(t)

	body := "locking\ttrue\nsomething_from_the_future\t42\n"
	if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := settings.Settings(ctx)
	if err != nil {
		t.Fatalf("an unknown field made the settings unreadable: %v", err)
	}
	if !got.Locking {
		t.Error("the field we do know was not read")
	}
}

// TestOpenSettingsRejectsWhatIsNotAStore mirrors Open: a vanished store must
// not read as one with locking disabled, because those are different answers
// and only one of them is safe to act on.
func TestOpenSettingsRejectsWhatIsNotAStore(t *testing.T) {
	if _, err := OpenSettings(t.TempDir()); err == nil {
		t.Fatal("an empty directory was accepted as a store")
	} else if !errors.Is(err, errs.ErrInvalidStore) {
		t.Errorf("error = %v, want ErrInvalidStore", err)
	}
}

// TestSettingsWriteIsAtomic: an interrupted write must not leave half a
// setting behind, so nothing but the final file may appear under the store.
func TestSettingsWriteIsAtomic(t *testing.T) {
	ctx := context.Background()
	settings, dir := newSettings(t)

	if err := settings.SetSettings(ctx, store.Settings{Locking: true}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if len(e.Name()) > 9 && e.Name()[:9] == "settings-" {
			t.Errorf("a temporary settings file was left behind: %s", e.Name())
		}
	}
}
