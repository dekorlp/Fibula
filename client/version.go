package client

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// SnapshotPrefix is the ref namespace holding the snapshot timeline.
//
// One ref per snapshot, named by timestamp, rather than one ref at the head of
// a parent chain. That is what makes the thinning schedule of E14 possible at
// all: with parent pointers a newer snapshot keeps every older one reachable,
// so the middle of a timeline could never be thinned (E12, addendum).
const SnapshotPrefix = "snapshots"

// snapshotStamp is the timestamp form used in snapshot ref names: RFC 3339
// without the separators a ref name may not contain, so that the names sort
// chronologically as plain strings.
const snapshotStamp = "20060102T150405Z"

// Snapshot is one entry of the timeline.
type Snapshot struct {
	Ref     store.RefName
	Version hash.VersionID
	Time    time.Time
	Expiry  time.Time
}

// snapshotRefName builds the ref name of a snapshot. The version prefix keeps
// two snapshots taken in the same second apart.
func snapshotRefName(at time.Time, id hash.VersionID) (store.RefName, error) {
	name := fmt.Sprintf("%s/%s-%s", SnapshotPrefix, at.UTC().Format(snapshotStamp), id.String()[:8])
	return store.LocalRef(name)
}

// Timeline returns every snapshot, oldest first.
//
// The order comes from the ref names rather than from the objects, which is
// the point of the addendum to E12: the names carry the timeline, the objects
// carry the state.
func (s *Space) Timeline(ctx context.Context) ([]Snapshot, error) {
	names, err := s.refs.List(ctx, store.ScopeLocal)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	var timeline []Snapshot
	for _, name := range names {
		if !strings.HasPrefix(name.Name(), SnapshotPrefix+"/") {
			continue
		}

		id, err := s.refs.Get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("read snapshot ref %s: %w", name, err)
		}
		version, err := s.readVersion(ctx, id)
		if err != nil {
			return nil, err
		}
		timeline = append(timeline, Snapshot{Ref: name, Version: id, Time: version.Time, Expiry: version.Expiry})
	}

	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Time.Before(timeline[j].Time) })
	return timeline, nil
}

// Commit records the working directory as a deliberate version (E12): message
// mandatory, no expiry, and it advances the ref the space is on.
//
// It shares everything but those fields with Snapshot, which is the point of
// E12 - restore, diff and checkout then work identically for both.
func (s *Space) Commit(ctx context.Context, ignore *Ignore, opts SnapshotOptions) (SnapshotResult, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return SnapshotResult{}, fmt.Errorf("%w: a deliberate version needs a message", errs.ErrInconsistentObject)
	}
	opts.Expiry = time.Time{}

	return s.record(ctx, ignore, opts, commitTarget{})
}

// PromotedPrefix is the ref namespace holding promoted snapshots.
const PromotedPrefix = "promoted"

// Promote turns an auto snapshot into a deliberate version (E12, F-S4-03).
//
// It creates a new object referencing the same manifest rather than mutating
// the snapshot - content-addressed objects are never mutated - and costs
// nothing, because the chunks have been in the store since the snapshot was
// taken. The snapshot itself goes on expiring normally.
//
// It does not move the ref the space is on, and that is the correction a dry
// run produced. E12's phrasing is "yesterday's 14:20 state was good, I'll keep
// that one" - keep, meaning make permanent, not revert to. Advancing main
// would have swapped the working state out from under whoever ran the command,
// silently discarding newer work from the tip; the version is instead
// published under its own ref, which is what makes it survive expiry and
// garbage collection without changing where anyone is working. Wanting that
// state back is a checkout, and a checkout is an explicit act.
func (s *Space) Promote(ctx context.Context, snapshot hash.VersionID, opts SnapshotOptions) (SnapshotResult, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return SnapshotResult{}, fmt.Errorf("%w: a promoted version needs a message", errs.ErrInconsistentObject)
	}

	original, err := s.readVersion(ctx, snapshot)
	if err != nil {
		return SnapshotResult{}, err
	}
	if original.Expiry.IsZero() {
		return SnapshotResult{}, fmt.Errorf("%w: %s is already a deliberate version",
			errs.ErrInconsistentObject, snapshot)
	}

	opts.Expiry = time.Time{}
	versionID, err := s.putVersion(ctx, original.Manifest, hash.VersionID{}, false, opts)
	if err != nil {
		return SnapshotResult{}, err
	}

	at := opts.Now
	if at.IsZero() {
		at = time.Now()
	}
	ref, err := store.LocalRef(fmt.Sprintf("%s/%s-%s",
		PromotedPrefix, at.UTC().Format(snapshotStamp), versionID.String()[:8]))
	if err != nil {
		return SnapshotResult{}, err
	}
	if err := s.refs.CompareAndSwap(ctx, ref, hash.VersionID{}, versionID); err != nil {
		return SnapshotResult{}, fmt.Errorf("publish promoted version: %w", err)
	}

	return SnapshotResult{Version: versionID, Manifest: original.Manifest}, nil
}

// currentRef is the ref the space commits onto. A fresh space is on main
// (E13).
func (s *Space) currentRef() string {
	head, err := s.Head()
	if err != nil || head.Ref.Name() == "" {
		return store.DefaultRef
	}
	return head.Ref.Name()
}

func (s *Space) refValue(ctx context.Context, name string) (hash.VersionID, bool, error) {
	ref, err := store.LocalRef(name)
	if err != nil {
		return hash.VersionID{}, false, err
	}

	id, err := s.refs.Get(ctx, ref)
	if errors.Is(err, errs.ErrRefNotFound) {
		return hash.VersionID{}, false, nil
	}
	if err != nil {
		return hash.VersionID{}, false, fmt.Errorf("read ref %s: %w", name, err)
	}
	return id, true, nil
}

// advanceRef moves a ref through compare-and-swap, never through a
// read-modify-write (E13). A zero expected value means the ref must not exist
// yet.
func (s *Space) advanceRef(ctx context.Context, name string, from hash.VersionID, exists bool, to hash.VersionID) error {
	ref, err := store.LocalRef(name)
	if err != nil {
		return err
	}

	expected := hash.VersionID{}
	if exists {
		expected = from
	}
	if err := s.refs.CompareAndSwap(ctx, ref, expected, to); err != nil {
		return fmt.Errorf("advance %s: %w", name, err)
	}
	return nil
}

// LogEntry is one line of history.
type LogEntry struct {
	Version  hash.VersionID
	Manifest hash.ManifestID
	Author   string
	Time     time.Time
	Message  string

	// Snapshot marks an auto snapshot, which is an expiry being set rather
	// than a different object type (E12).
	Snapshot bool
	Expiry   time.Time
}

// Log walks the parent chain of a ref, newest first (F-S4-04).
//
// It follows the content graph, which is the deliberate history. Auto
// snapshots do not appear here: they are a timeline, listed by Timeline, and
// mixing the two would make history look different depending on how recently
// somebody saved.
func (s *Space) Log(ctx context.Context, ref string, limit int) ([]LogEntry, error) {
	id, exists, err := s.refValue(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", errs.ErrRefNotFound, ref)
	}

	var entries []LogEntry
	seen := make(map[hash.VersionID]struct{})

	for pending := []hash.VersionID{id}; len(pending) > 0; {
		current := pending[0]
		pending = pending[1:]

		if _, done := seen[current]; done {
			continue
		}
		seen[current] = struct{}{}

		version, err := s.readVersion(ctx, current)
		if err != nil {
			return nil, err
		}
		entries = append(entries, logEntryOf(current, version))

		if limit > 0 && len(entries) >= limit {
			break
		}
		pending = append(pending, version.Parents...)
	}
	return entries, nil
}

func logEntryOf(id hash.VersionID, version object.Version) LogEntry {
	return LogEntry{
		Version:  id,
		Manifest: version.Manifest,
		Author:   version.Author,
		Time:     version.Time,
		Message:  version.Message,
		Snapshot: !version.Expiry.IsZero(),
		Expiry:   version.Expiry,
	}
}
