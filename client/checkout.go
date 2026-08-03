package client

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/manifest"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// CheckoutResult reports what a checkout changed on disk.
type CheckoutResult struct {
	Version  hash.VersionID
	Written  int
	Removed  int
	Bytes    int64
	Snapshot bool
}

// minPrefix is the shortest abbreviation Resolve accepts. Eight hex characters
// are 32 bits, which is far more than enough to be unique among the versions
// one project produces, and short enough to type.
const minPrefix = 8

// Resolve turns a ref name, a full version hash or an unambiguous prefix of
// one into a version.
//
// Refs are tried first, so that "main" means the ref rather than a hash that
// happens to start that way.
//
// Prefixes are accepted because the tool prints them: log and snapshots
// abbreviate to twelve characters, and a command line that cannot take back
// what it just printed is a command line nobody enjoys using. An ambiguous
// prefix is an error rather than a guess - picking one of two versions to
// check out is the kind of helpfulness that loses work.
func (s *Space) Resolve(ctx context.Context, name string) (hash.VersionID, error) {
	if id, exists, err := s.refValue(ctx, name); err == nil && exists {
		return id, nil
	}

	if id, err := hash.ParseVersionID(name); err == nil {
		if _, err := s.readVersion(ctx, id); err != nil {
			return hash.VersionID{}, err
		}
		return id, nil
	}

	return s.resolvePrefix(ctx, name)
}

// resolvePrefix searches the versions this space knows about: every ref, every
// snapshot on the timeline, and the deliberate history behind them.
func (s *Space) resolvePrefix(ctx context.Context, prefix string) (hash.VersionID, error) {
	if len(prefix) < minPrefix || !isLowerHex(prefix) {
		return hash.VersionID{}, fmt.Errorf("%w: %q is neither a ref nor a version, and too short or malformed "+
			"to be a prefix (at least %d lowercase hex characters)", errs.ErrRefNotFound, prefix, minPrefix)
	}

	known, err := s.knownVersions(ctx)
	if err != nil {
		return hash.VersionID{}, err
	}

	var matches []hash.VersionID
	for _, id := range known {
		if strings.HasPrefix(id.String(), prefix) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return hash.VersionID{}, fmt.Errorf("%w: no version starts with %q", errs.ErrRefNotFound, prefix)
	default:
		return hash.VersionID{}, fmt.Errorf("%w: %d versions start with %q, use more characters",
			errs.ErrRefNotFound, len(matches), prefix)
	}
}

// knownVersions collects every version reachable from a ref, sorted so that
// the answer does not depend on map iteration order.
func (s *Space) knownVersions(ctx context.Context) ([]hash.VersionID, error) {
	seen := make(map[hash.VersionID]struct{})

	for _, scope := range []store.Scope{store.ScopeLocal, store.ScopeRemote} {
		names, err := s.refs.List(ctx, scope)
		if err != nil {
			return nil, fmt.Errorf("list %s refs: %w", scope, err)
		}
		for _, name := range names {
			id, err := s.refs.Get(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("read ref %s: %w", name, err)
			}
			if err := s.walkKnown(ctx, id, seen); err != nil {
				return nil, err
			}
		}
	}

	known := make([]hash.VersionID, 0, len(seen))
	for id := range seen {
		known = append(known, id)
	}
	sort.Slice(known, func(i, j int) bool { return known[i].String() < known[j].String() })
	return known, nil
}

func (s *Space) walkKnown(ctx context.Context, id hash.VersionID, seen map[hash.VersionID]struct{}) error {
	pending := []hash.VersionID{id}

	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		if _, done := seen[current]; done {
			continue
		}
		seen[current] = struct{}{}

		version, err := s.readVersion(ctx, current)
		if err != nil {
			return err
		}
		pending = append(pending, version.Parents...)
	}
	return nil
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Checkout puts the working directory into the state of a ref or version
// (F-S4-04).
//
// It runs the dirty check first, and that is not caution but the same rule as
// clearing: switching away from unsaved work destroys it exactly as deleting
// it would, so it goes through the same gate. Unversioned files are
// snapshotted rather than refused, for the reason E17 gives.
func (s *Space) Checkout(ctx context.Context, ignore *Ignore, target string, opts ClearOptions) (CheckoutResult, error) {
	id, err := s.Resolve(ctx, target)
	if err != nil {
		return CheckoutResult{}, err
	}

	check, err := s.CheckClear(ctx, ignore)
	if err != nil {
		return CheckoutResult{}, err
	}
	if !check.OK() {
		return CheckoutResult{}, check.Err()
	}
	if len(check.Unversioned) > 0 {
		if _, err := s.snapshotUnversioned(ctx, ignore, opts); err != nil {
			return CheckoutResult{}, err
		}
	}

	version, err := s.readVersion(ctx, id)
	if err != nil {
		return CheckoutResult{}, err
	}
	targetManifest, err := s.readManifest(ctx, version.Manifest)
	if err != nil {
		return CheckoutResult{}, err
	}

	result, err := s.applyManifest(ctx, ignore, targetManifest)
	if err != nil {
		return result, err
	}
	result.Version = id
	result.Snapshot = !version.Expiry.IsZero()

	if err := s.pointHeadAt(ctx, target, id, targetManifest); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Space) readManifest(ctx context.Context, id hash.ManifestID) (object.Manifest, error) {
	data, err := s.objects.Get(ctx, store.ManifestKey(id))
	if err != nil {
		return object.Manifest{}, fmt.Errorf("read manifest %s: %w", id, err)
	}
	return object.UnmarshalManifest(data)
}

// applyManifest makes the working directory match a manifest: write what it
// names, remove what it does not.
//
// Removing is the half a plain restore does not do, and it is why checkout
// needs the dirty check: everything about to disappear has just been proven
// recoverable.
func (s *Space) applyManifest(ctx context.Context, ignore *Ignore, m object.Manifest) (CheckoutResult, error) {
	var result CheckoutResult

	written, err := s.RestoreManifest(ctx, m)
	if err != nil {
		return result, err
	}
	result.Written = written.Files
	result.Bytes = written.Bytes

	wanted := make(map[string]struct{}, len(m.Entries))
	for _, entry := range m.Entries {
		wanted[entry.Path] = struct{}{}
	}

	present, err := s.Scan(ctx, ignore)
	if err != nil {
		return result, err
	}
	for _, f := range present {
		if _, keep := wanted[f.Path]; keep {
			continue
		}
		if _, err := s.deleteFile(f.Path); err != nil {
			return result, err
		}
		result.Removed++
	}

	if err := s.pruneEmptyDirs(); err != nil {
		return result, err
	}
	return result, nil
}

// pointHeadAt records where the space now stands. Checking out a ref moves
// onto it; checking out a bare version keeps the current ref, because a
// version is a place to look rather than a place to work.
func (s *Space) pointHeadAt(ctx context.Context, target string, id hash.VersionID, m object.Manifest) error {
	name := s.currentRef()
	if _, exists, err := s.refValue(ctx, target); err == nil && exists {
		name = target
	}

	ref, err := store.LocalRef(name)
	if err != nil {
		return err
	}
	if err := s.SetHead(Head{Ref: ref, Version: id}); err != nil {
		return err
	}

	data, err := m.Marshal()
	if err != nil {
		return err
	}
	if err := s.writeCheckedOutManifest(data); err != nil {
		return err
	}
	return s.refreshCacheFrom(ctx, m)
}

// Diff compares two states, each named by a ref or a version (F-S4-04).
func (s *Space) Diff(ctx context.Context, from, to string) (manifest.Changes, error) {
	before, err := s.manifestOf(ctx, from)
	if err != nil {
		return manifest.Changes{}, err
	}
	after, err := s.manifestOf(ctx, to)
	if err != nil {
		return manifest.Changes{}, err
	}
	return manifest.Diff(before, after), nil
}

func (s *Space) manifestOf(ctx context.Context, name string) (object.Manifest, error) {
	id, err := s.Resolve(ctx, name)
	if err != nil {
		return object.Manifest{}, err
	}
	version, err := s.readVersion(ctx, id)
	if err != nil {
		return object.Manifest{}, err
	}
	return s.readManifest(ctx, version.Manifest)
}
