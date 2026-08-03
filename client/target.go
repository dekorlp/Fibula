package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
)

// recordTarget is where a recorded version is published: a new entry on the
// snapshot timeline, or the ref the space is on.
type recordTarget interface {
	// parentOf returns what the new version should name as its parent.
	parentOf(ctx context.Context, s *Space) (hash.VersionID, bool, error)

	// publish points a ref at the new version.
	publish(ctx context.Context, s *Space, parent hash.VersionID, hasParent bool,
		id hash.VersionID, at time.Time) (store.RefName, error)

	// advancesBase reports whether recording this moves the version the next
	// commit builds on. A snapshot does not: it records the directory as it
	// stands, without changing what the space descends from.
	advancesBase() bool

	// enforcesLocks reports whether a foreign lock refuses this operation.
	//
	// Only publishing is checked. A snapshot lands on its own ref, touches
	// nobody else's state and is the safety net that has to work when things
	// are going wrong - blocking it would take the net away exactly when a
	// contested file needs preserving (E49, E12).
	enforcesLocks() bool
}

// snapshotTarget writes a point on the timeline. It gives the version no
// parent at all: a snapshot is a point in time, and a parent pointer would
// keep every older snapshot reachable and make the thinning schedule of E14
// impossible (E12, addendum).
type snapshotTarget struct{}

func (snapshotTarget) parentOf(context.Context, *Space) (hash.VersionID, bool, error) {
	return hash.VersionID{}, false, nil
}

func (snapshotTarget) advancesBase() bool  { return false }
func (snapshotTarget) enforcesLocks() bool { return false }

func (snapshotTarget) publish(ctx context.Context, s *Space, _ hash.VersionID, _ bool,
	id hash.VersionID, at time.Time,
) (store.RefName, error) {
	ref, err := snapshotRefName(at, id)
	if err != nil {
		return store.RefName{}, err
	}
	// Publishing is idempotent. An unchanged tree snapshotted twice in the
	// same second produces the same manifest, therefore the same version,
	// therefore the same ref name and value - that is one snapshot recorded
	// twice, not a conflict.
	switch existing, err := s.refs.Get(ctx, ref); {
	case err == nil && existing == id:
		return ref, nil
	case err != nil && !errors.Is(err, errs.ErrRefNotFound):
		return store.RefName{}, fmt.Errorf("publish snapshot: %w", err)
	}

	if err := s.refs.CompareAndSwap(ctx, ref, hash.VersionID{}, id); err != nil {
		return store.RefName{}, fmt.Errorf("publish snapshot: %w", err)
	}
	return ref, nil
}

// commitTarget advances the ref the space is on, and the new version names the
// previous one as its parent. That is the content graph - the history log
// walks, and the one that is never thinned.
type commitTarget struct{}

// parentOf returns the ref's current value, but only after establishing that
// this space actually descends from it.
//
// Without that check a commit names head as its parent while the manifest is
// built from a working directory that never contained head's changes, so
// recording it deletes the other side's work with no signal at all - the
// automatic overwrite E13.3 forbids (TP-005 EC-401). The compare-and-swap
// downstream cannot see this: the value it replaces really is the one it read.
//
// The rule is that the local base and the ref must agree exactly: both absent
// (the first commit) or both present and equal. Anything else means the space
// is not where it thinks it is, and the safe answer is to refuse rather than
// to guess which side should win.
func (commitTarget) parentOf(ctx context.Context, s *Space) (hash.VersionID, bool, error) {
	current, onRef, err := s.refValue(ctx, s.currentRef())
	if err != nil {
		return hash.VersionID{}, false, err
	}

	local, hasLocal, err := s.baseVersion()
	if err != nil {
		return hash.VersionID{}, false, err
	}

	switch {
	case !hasLocal && !onRef:
		return hash.VersionID{}, false, nil
	case hasLocal && onRef && local == current:
		return current, true, nil
	case !hasLocal:
		return hash.VersionID{}, false, fmt.Errorf(
			"%w: %s is at %s but this space has no history yet; check out first",
			errs.ErrSpaceBehind, s.currentRef(), current)
	case !onRef:
		return hash.VersionID{}, false, fmt.Errorf(
			"%w: this space is at %s but %s does not exist in the store",
			errs.ErrSpaceBehind, local, s.currentRef())
	default:
		return hash.VersionID{}, false, fmt.Errorf(
			"%w: %s has moved to %s since this space checked out %s; "+
				"committing would discard that work",
			errs.ErrSpaceBehind, s.currentRef(), current, local)
	}
}

func (commitTarget) advancesBase() bool  { return true }
func (commitTarget) enforcesLocks() bool { return true }

func (commitTarget) publish(ctx context.Context, s *Space, parent hash.VersionID, hasParent bool,
	id hash.VersionID, _ time.Time,
) (store.RefName, error) {
	name := s.currentRef()
	if err := s.advanceRef(ctx, name, parent, hasParent, id); err != nil {
		return store.RefName{}, err
	}
	return store.LocalRef(name)
}
