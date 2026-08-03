package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

// settingsFile holds project-wide configuration beside the objects and refs.
// One line per field, tab separated - the same shape the client uses for its
// local state, and readable with cat when something looks wrong.
const settingsFile = "settings"

// OpenSettings returns the settings store for an existing store directory.
func OpenSettings(dir string) (store.SettingsStore, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve store directory: %w", err)
	}
	// Same reasoning as Open: a vanished store must not read as an empty one,
	// because "locking disabled" and "no store" are different answers.
	if info, err := os.Stat(filepath.Join(abs, objectsDir)); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a store", errs.ErrInvalidStore, abs)
	}
	return &settingsStore{root: abs}, nil
}

type settingsStore struct{ root string }

func (s *settingsStore) path() string { return filepath.Join(s.root, settingsFile) }

// Settings reads the configuration. A store with no settings file reports the
// zero value, which is how every store behaved before settings existed.
func (s *settingsStore) Settings(ctx context.Context) (store.Settings, error) {
	if err := ctx.Err(); err != nil {
		return store.Settings{}, fmt.Errorf("read settings: %w", err)
	}

	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return store.Settings{}, nil
	}
	if err != nil {
		return store.Settings{}, fmt.Errorf("read settings: %w", err)
	}
	return parseSettings(string(data))
}

func parseSettings(body string) (store.Settings, error) {
	var settings store.Settings

	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "\t")
		if !ok {
			return store.Settings{}, fmt.Errorf("settings: malformed line %q", line)
		}

		switch key {
		case "locking":
			settings.Locking = value == "true"
		case "lock_expiry_seconds":
			seconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return store.Settings{}, fmt.Errorf("settings: lock_expiry_seconds: %w", err)
			}
			settings.LockExpiry = time.Duration(seconds) * time.Second
		default:
			// Unknown keys are ignored rather than rejected: a newer client
			// writing a field this one does not know must not make the store
			// unreadable.
			continue
		}
	}
	return settings, nil
}

// SetSettings replaces the configuration through a temporary file and a rename,
// so an interrupted write cannot leave the store with half a setting.
func (s *settingsStore) SetSettings(ctx context.Context, settings store.Settings) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	body := "locking\t" + strconv.FormatBool(settings.Locking) + "\n" +
		"lock_expiry_seconds\t" + strconv.FormatInt(int64(settings.Expiry().Seconds()), 10) + "\n"

	if err := replaceFile(s.path(), []byte(body)); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// replaceFile writes state through a temporary file and a rename, so that an
// interrupted write cannot leave half a value behind.
//
// Used for the store's mutable, non content-addressed state - settings and
// locks. Objects do not go through here: they have their own path with the
// hash verification that this state has no equivalent of.
func replaceFile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, "state-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temp := f.Name()
	defer os.Remove(temp) //nolint:errcheck // a no-op once the rename succeeded

	if err := writeAndSync(f, data); err != nil {
		_ = f.Close() //nolint:errcheck // the write already failed and is being reported
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	return os.Rename(temp, target)
}

var _ store.SettingsStore = (*settingsStore)(nil)
