package store

import (
	"context"
	"time"
)

// DefaultLockExpiry is how long a file lock survives without being renewed
// (E51).
//
// Deliberately generous. Expiry depends on a clock, and over a network share
// the server stamps the file while the client reads its own time - the skew
// TP-005 named as the likely real-NAS defect. At thirty seconds a minute of
// skew is fatal; at fourteen days it is irrelevant. Where a deadline may be
// long, it should be.
const DefaultLockExpiry = 14 * 24 * time.Hour

// Settings is project-wide configuration that belongs to the store rather than
// to any one client (E51).
//
// It lives here and not in a client's local state because two clients must not
// disagree about it: a locally configured expiry would let one of them consider
// a lock dead that the other still honours. "Locking is enabled for this
// project" has the same problem, and worse - a freshly initialized space would
// not know the rule exists and would quietly ignore it.
type Settings struct {
	// Locking reports whether file locks are in force for this project.
	// Off by default, so a store written before this existed keeps working
	// and a solo developer never meets the feature (E48).
	Locking bool

	// LockExpiry is how long a lock survives. Zero means DefaultLockExpiry.
	LockExpiry time.Duration
}

// Expiry returns the effective lock lifetime.
func (s Settings) Expiry() time.Duration {
	if s.LockExpiry <= 0 {
		return DefaultLockExpiry
	}
	return s.LockExpiry
}

// SettingsStore holds a store's project-wide configuration.
//
// It is separate from RefStore even though both hold mutable, non
// content-addressed state, because the access patterns have nothing in common:
// a ref is compare-and-swapped by name and read constantly, settings are read
// as a whole and written by hand perhaps twice in a project's life. Folding
// them together would be the same conflation E24 avoids between objects and
// refs.
//
// A store with no settings recorded reports the zero value, which reads as
// "locking disabled" - the behaviour every store had before this existed.
type SettingsStore interface {
	// Settings returns the project configuration.
	Settings(ctx context.Context) (Settings, error)

	// SetSettings replaces it. Writing is atomic; concurrent writers may
	// overwrite each other, which is acceptable for a value changed by hand
	// and never in a hot path.
	SetSettings(ctx context.Context, s Settings) error
}
