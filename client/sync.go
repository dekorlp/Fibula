package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/manifest"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// conflictsDir holds the other side's version of every file the merge could not
// decide, under its original name so it can actually be opened (E46).
const conflictsDir = "conflicts"

// SyncResult reports what a sync did.
type SyncResult struct {
	// AlreadyCurrent is set when the space was up to date and nothing ran.
	AlreadyCurrent bool
	// To is the version the space now descends from.
	To hash.VersionID

	Written int
	Removed int
	Bytes   int64

	Conflicts []manifest.Conflict
}

// Sync brings the working directory onto the current state of its ref (E52).
//
// There is no fetch step: on a shared store the ref is right there, and a
// fetch/pull pair would be copied from Git where it models a network hop.
//
// Nor is there a fast-forward special case. When the directory holds no local
// changes the merge of E44 already returns exactly the other side, so one path
// covers both situations - and the one that does more work is the rare one.
//
// Afterwards Base equals the ref, so an ordinary commit succeeds. In phase 1
// that commit has a single parent, because the staleness check makes history
// unable to fork (E45).
func (s *Space) Sync(ctx context.Context, ignore *Ignore) (SyncResult, error) {
	refName := s.currentRef()
	current, onRef, err := s.refValue(ctx, refName)
	if err != nil {
		return SyncResult{}, err
	}
	if !onRef {
		// Nothing has been published yet, so there is nothing to sync onto.
		return SyncResult{AlreadyCurrent: true}, nil
	}

	base, hasBase, err := s.baseVersion()
	if err != nil {
		return SyncResult{}, err
	}
	if hasBase && base == current {
		return SyncResult{AlreadyCurrent: true, To: current}, nil
	}

	result, err := s.mergeOnto(ctx, ignore, base, hasBase, current)
	if err != nil {
		return SyncResult{}, err
	}
	if err := s.settleAfterSync(ctx, refName, current); err != nil {
		return result, err
	}
	return result, nil
}

// mergeOnto computes and applies the merge, leaving the working directory in
// the merged state and the conflict copies on disk.
func (s *Space) mergeOnto(ctx context.Context, ignore *Ignore, base hash.VersionID, hasBase bool,
	target hash.VersionID,
) (SyncResult, error) {
	theirs, err := s.manifestOfVersion(ctx, target)
	if err != nil {
		return SyncResult{}, err
	}

	var baseManifest object.Manifest
	if hasBase {
		if baseManifest, err = s.manifestOfVersion(ctx, base); err != nil {
			return SyncResult{}, err
		}
	}

	tree, err := s.scanWorkingTree(ctx, ignore)
	if err != nil {
		return SyncResult{}, err
	}
	ours, err := tree.builder.Build()
	if err != nil {
		return SyncResult{}, err
	}

	merged, conflicts := manifest.Merge(baseManifest, ours, theirs)

	result, err := s.applyMerged(ctx, ours, merged)
	if err != nil {
		return result, err
	}
	result.To = target
	result.Conflicts = conflicts

	return result, s.writeConflictCopies(ctx, conflicts)
}

// applyMerged moves the working directory from ours to merged.
//
// It writes the difference rather than the whole manifest, and that is not an
// optimisation: our own uncommitted entries are already correct on disk, and
// several of them may not be in the store at all, so restoring them wholesale
// would fail on the files we care about most.
func (s *Space) applyMerged(ctx context.Context, ours, merged object.Manifest) (SyncResult, error) {
	changes := manifest.Diff(ours, merged)

	// The cache starts from what was recorded before the sync, not from the
	// scan. Only the files this sync actually wrote may move forward: our own
	// uncommitted changes have to keep looking uncommitted, or status would
	// report a clean directory over work nobody has recorded.
	cache, err := s.LoadCache()
	if err != nil {
		return SyncResult{}, err
	}

	result, err := s.writeIncoming(ctx, cache, incomingOf(changes))
	if err != nil {
		return result, err
	}

	for _, entry := range append(changes.Removed, renameSources(changes.Renamed)...) {
		if _, err := s.deleteFile(entry.Path); err != nil {
			return result, err
		}
		cache.Remove(entry.Path)
		result.Removed++
	}

	if err := s.SaveCache(cache); err != nil {
		return result, err
	}
	return result, s.pruneEmptyDirs()
}

// writeIncoming fetches the entries the other side contributed and records them
// in the cache as genuinely up to date.
func (s *Space) writeIncoming(ctx context.Context, cache *Cache, entries []object.Entry) (SyncResult, error) {
	var result SyncResult

	for _, entry := range entries {
		target, err := SafeJoin(s.root, entry.Path)
		if err != nil {
			return result, err
		}
		written, err := s.restoreEntry(ctx, target, entry)
		if err != nil {
			return result, err
		}
		size, mtime, err := statFile(s.root, entry.Path)
		if err != nil {
			return result, err
		}

		cache.Put(CacheEntry{Path: entry.Path, MTime: mtime, Size: size, File: entry.File})
		result.Written++
		result.Bytes += written
	}
	return result, nil
}

// incomingOf is everything the working directory does not have yet: added,
// changed and the destination of a rename.
func incomingOf(changes manifest.Changes) []object.Entry {
	entries := append([]object.Entry{}, changes.Added...)
	for _, c := range changes.Changed {
		entries = append(entries, c.After)
	}
	for _, r := range changes.Renamed {
		entries = append(entries, r.After)
	}
	return entries
}

func renameSources(renames []manifest.Rename) []object.Entry {
	sources := make([]object.Entry, len(renames))
	for i, r := range renames {
		sources[i] = r.Before
	}
	return sources
}

// writeConflictCopies puts the other side's version where it can be opened.
//
// The directory is cleared first: copies from an earlier sync describe a state
// that has since moved on, and a stale one is worse than none. Nothing is lost
// by clearing, because every copy is reconstructible from the store.
func (s *Space) writeConflictCopies(ctx context.Context, conflicts []manifest.Conflict) error {
	dir := filepath.Join(s.stateDir(), conflictsDir)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear conflict directory: %w", err)
	}

	for _, c := range conflicts {
		if c.Theirs == nil {
			// They deleted it, so there is no version to compare against.
			continue
		}
		target, err := SafeJoin(dir, c.Path)
		if err != nil {
			return err
		}
		if _, err := s.restoreEntry(ctx, target, *c.Theirs); err != nil {
			return fmt.Errorf("write conflict copy for %s: %w", c.Path, err)
		}
	}
	return nil
}

// settleAfterSync records where the space now stands.
//
// Head.Version and the checked-out manifest are set to *their* state, not to
// the merged one: the merged state is not a version, and recording it as one
// would claim that uncommitted work had been published. Base moves to the same
// place, which is what lets the next commit through (E45).
func (s *Space) settleAfterSync(ctx context.Context, refName string, target hash.VersionID) error {
	theirs, err := s.manifestOfVersion(ctx, target)
	if err != nil {
		return err
	}
	data, err := theirs.Marshal()
	if err != nil {
		return err
	}
	if err := s.writeCheckedOutManifest(data); err != nil {
		return err
	}

	ref, err := store.LocalRef(refName)
	if err != nil {
		return err
	}
	return s.SetHead(Head{Ref: ref, Version: target, Base: target})
}

// PendingConflicts lists the conflict copies still on disk, as manifest paths.
//
// They are the only trace of an unfinished decision, so status reports them for
// as long as they exist (E53). An empty result is the normal state.
func (s *Space) PendingConflicts() ([]string, error) {
	dir := filepath.Join(s.stateDir(), conflictsDir)

	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		switch {
		case os.IsNotExist(err):
			return nil // no conflicts is not a condition worth reporting
		case err != nil:
			return err
		case d.IsDir():
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list conflict copies: %w", err)
	}

	sort.Strings(paths)
	return paths, nil
}

func (s *Space) manifestOfVersion(ctx context.Context, id hash.VersionID) (object.Manifest, error) {
	version, err := s.readVersion(ctx, id)
	if err != nil {
		return object.Manifest{}, err
	}
	return s.readManifest(ctx, version.Manifest)
}
