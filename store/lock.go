package store

import (
	"context"
	"time"
)

// Lock is a reservation on one file (E48-E51).
//
// It is a reservation, not an obligation: committing without one is fine as
// long as nobody else holds one, and editing is never blocked - requiring a
// lock before an edit would make offline work impossible, which invariant 9
// forbids. What a lock buys is that the *other* side is refused at commit time
// and sees the file read-only while working.
type Lock struct {
	// Path is the manifest path being reserved.
	Path string

	// Owner is who holds it. In phase 1 there is no authentication behind
	// this, so it is a name rather than an identity.
	Owner string

	// Since is when the current holder took it. Expiry is measured from here.
	Since time.Time

	// Reason is optional free text - "retopo, ~2 days".
	Reason string

	// BrokenFrom is who held the lock before it was taken over, empty if it
	// was never broken. Kept rather than discarded so the original holder is
	// told on their next sync instead of having to be told by a person (E51).
	BrokenFrom string

	// BrokenAt is when that happened.
	BrokenAt time.Time
}

// Expired reports whether the lock is old enough to be taken over.
//
// The comparison lives here rather than in the store because the deadline
// comes from the project settings, which the store does not read. It is also
// the one piece of lock logic that depends on a clock - see DefaultLockExpiry
// for why that deadline is deliberately generous.
func (l Lock) Expired(now time.Time, expiry time.Duration) bool {
	return now.Sub(l.Since) >= expiry
}

// LockStore holds file locks.
//
// Like refs they are mutable, non content-addressed state (E50), and taking
// one has to be atomic: two people reaching for the same file at the same
// moment must not both succeed. In a later server they belong in the index
// rather than the blob store, for the same reason refs do.
type LockStore interface {
	// Acquire takes a lock. It reports ErrLockHeld if someone already has it,
	// including the holder themselves - re-taking a lock is a mistake worth
	// surfacing rather than a no-op.
	Acquire(ctx context.Context, lock Lock) error

	// Release gives up a lock held by owner. Releasing a lock somebody else
	// holds reports ErrLockHeld; releasing one that does not exist is not an
	// error, so that cleaning up twice is harmless.
	Release(ctx context.Context, path, owner string) error

	// Transfer hands a lock to a new owner, recording who lost it. This is
	// what --force does, and what taking over an expired lock does: the store
	// does not distinguish the two, because whether it was allowed is a
	// question about settings and clocks that belongs one level up.
	Transfer(ctx context.Context, path, to string, at time.Time) (Lock, error)

	// Get returns one lock, reporting ErrLockNotFound if the path is free.
	Get(ctx context.Context, path string) (Lock, error)

	// List returns every lock, ordered by path.
	List(ctx context.Context) ([]Lock, error)
}
