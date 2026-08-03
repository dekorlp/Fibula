package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dekorlp/fibula/chunk"
	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/manifest"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// SnapshotOptions controls one snapshot.
type SnapshotOptions struct {
	// Author is recorded on the version. The server validates it on push and
	// rejects a mismatch rather than correcting it (E30).
	Author string

	// Message makes this a deliberate version rather than an auto snapshot.
	// The two are the same object type, distinguished only by which fields
	// are set (E12).
	Message string

	// Expiry is set on auto snapshots. A deliberate version has none.
	Expiry time.Time

	// Now is the timestamp to record. Zero means time.Now, which is what
	// every caller but a test wants.
	Now time.Time
}

// SnapshotResult reports what one snapshot produced.
type SnapshotResult struct {
	Version  hash.VersionID
	Manifest hash.ManifestID
	Files    int
	Hashed   int // files that had to be read and chunked
	// Chunked is the size of the file content that was read and cut this
	// run. It is deliberately not called "uploaded": Put is idempotent, so a
	// chunk the store already holds is offered and discarded, and counting it
	// here would overstate what the run actually cost. Reporting the genuinely
	// new bytes needs Put to say whether the write was new — a store interface
	// question that belongs with the first network backend (F-S5).
	Chunked int64
}

// Snapshot records the working directory as a version.
//
// Unchanged files are taken from the status cache and never read: that is the
// entire point of the cache, and on a 200 GB tree it is the difference between
// a snapshot every few minutes and a snapshot nobody can afford to take. A
// file whose size or mtime moved is re-chunked, its chunks written to the
// store, and its new chunk list remembered locally (E16).
func (s *Space) Snapshot(ctx context.Context, ignore *Ignore, opts SnapshotOptions) (SnapshotResult, error) {
	opts.Message = ""
	if opts.Expiry.IsZero() {
		opts.Expiry = expiryFor(opts.Now)
	}
	return s.record(ctx, ignore, opts, snapshotTarget{})
}

// record scans the working directory, writes everything that changed and
// commits the result under whichever ref the target names.
//
// Snapshot and Commit differ only in the target and in which fields of the
// version are set (E12), which is what makes restore, diff and checkout work
// identically for both.
func (s *Space) record(ctx context.Context, ignore *Ignore, opts SnapshotOptions, target recordTarget) (SnapshotResult, error) {
	scan, err := s.scanWorkingTree(ctx, ignore)
	if err != nil {
		return SnapshotResult{}, err
	}

	next := scan.cache
	builder := scan.builder
	result := SnapshotResult{Files: scan.files, Hashed: scan.hashed, Chunked: scan.chunked}

	if target.enforcesLocks() {
		proposed, buildErr := builder.Build()
		if buildErr != nil {
			return SnapshotResult{}, buildErr
		}
		if err := s.checkLocksForCommit(ctx, proposed, opts.Author, opts.Now); err != nil {
			return SnapshotResult{}, err
		}
	}

	result.Version, result.Manifest, err = s.commit(ctx, builder, opts, target)
	if err != nil {
		return SnapshotResult{}, err
	}

	// The cache is written only after the version is safely in the store. If
	// the order were reversed, a crash in between would leave a cache
	// vouching for content the store never received - and the dirty check
	// would then be reasoning from a lie.
	if err := s.SaveCache(next); err != nil {
		return SnapshotResult{}, err
	}
	return result, nil
}

// workingTree is the current state of the directory: every file hashed, with
// the cache that vouches for it. Shared by record and Sync, which need the same
// scan for different reasons - one to publish it, one to merge against it.
type workingTree struct {
	builder manifest.Builder
	cache   *Cache
	files   int
	hashed  int
	chunked int64
}

// scanWorkingTree hashes whatever the cache cannot vouch for and returns the
// directory as a manifest under construction. Chunks reach the store as a side
// effect, which is what makes the result usable without a commit.
func (s *Space) scanWorkingTree(ctx context.Context, ignore *Ignore) (workingTree, error) {
	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return workingTree{}, err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return workingTree{}, err
	}

	tree := workingTree{cache: NewCache(), files: len(files)}
	for _, f := range files {
		entry, uploaded, hashed, err := s.snapshotFile(ctx, cache, f)
		if err != nil {
			return workingTree{}, err
		}
		if hashed {
			tree.hashed++
		}
		tree.chunked += uploaded

		tree.cache.Put(entry)
		if err := tree.builder.Add(entry.Path, entry.File, entry.Size); err != nil {
			return workingTree{}, err
		}
	}
	return tree, nil
}

// snapshotFile returns the cache entry for one file, chunking it only if the
// cache cannot vouch for it.
func (s *Space) snapshotFile(ctx context.Context, cache *Cache, f ScannedFile) (CacheEntry, int64, bool, error) {
	if entry, known := cache.Lookup(f.Path); known && entry.Matches(f.Size, f.MTime) {
		return entry, 0, false, nil
	}

	abs := filepath.Join(s.root, filepath.FromSlash(f.Path))
	file, err := os.Open(abs) //nolint:gosec // a path produced by scanning this space
	if err != nil {
		return CacheEntry{}, 0, false, fmt.Errorf("open %s: %w", f.Path, err)
	}
	defer file.Close() //nolint:errcheck // read-only

	var uploaded int64
	result, err := chunk.BuildFile(ctx, file, chunk.DefaultParams(),
		func(ctx context.Context, id hash.ChunkID, data []byte) error {
			uploaded += int64(len(data))
			return s.objects.Put(ctx, store.ChunkKey(id), data)
		})
	if err != nil {
		return CacheEntry{}, 0, false, fmt.Errorf("chunk %s: %w", f.Path, err)
	}

	if err := s.putFile(ctx, result); err != nil {
		return CacheEntry{}, 0, false, fmt.Errorf("%s: %w", f.Path, err)
	}

	// Re-stat after reading: if the file changed while it was being read, the
	// mtime recorded here belongs to content we did not hash. Recording the
	// pre-read mtime would let the cache vouch for the wrong bytes.
	size, mtime, err := statFile(s.root, f.Path)
	if err != nil {
		return CacheEntry{}, 0, false, err
	}

	return CacheEntry{Path: f.Path, MTime: mtime, Size: size, File: result.ID}, uploaded, true, nil
}

// putFile writes the file object to the store and remembers it locally.
func (s *Space) putFile(ctx context.Context, result chunk.Result) error {
	data, err := result.File.Marshal()
	if err != nil {
		return fmt.Errorf("marshal file object: %w", err)
	}
	if err := s.objects.Put(ctx, store.FileKey(result.ID), data); err != nil {
		return fmt.Errorf("store file object: %w", err)
	}
	return s.PutFileObject(ctx, result.ID, result.File)
}

// commit writes the manifest and the version and publishes it.
func (s *Space) commit(ctx context.Context, builder manifest.Builder, opts SnapshotOptions,
	target recordTarget,
) (hash.VersionID, hash.ManifestID, error) {
	manifestID, manifestBytes, err := s.putManifest(ctx, builder)
	if err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}

	parent, hasParent, err := target.parentOf(ctx, s)
	if err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}

	versionID, err := s.putVersion(ctx, manifestID, parent, hasParent, opts)
	if err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}

	at := opts.Now
	if at.IsZero() {
		at = time.Now()
	}
	if _, err := target.publish(ctx, s, parent, hasParent, versionID, at); err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}
	if err := s.recordHead(versionID, manifestBytes, target.advancesBase()); err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}
	return versionID, manifestID, nil
}

func (s *Space) putManifest(ctx context.Context, builder manifest.Builder) (hash.ManifestID, []byte, error) {
	m, err := builder.Build()
	if err != nil {
		return hash.ManifestID{}, nil, err
	}
	data, err := m.Marshal()
	if err != nil {
		return hash.ManifestID{}, nil, err
	}

	id, err := store.PutManifest(ctx, s.objects, data)
	if err != nil {
		return hash.ManifestID{}, nil, err
	}
	return id, data, nil
}

func (s *Space) putVersion(ctx context.Context, manifestID hash.ManifestID,
	parent hash.VersionID, hasParent bool, opts SnapshotOptions,
) (hash.VersionID, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	version := object.Version{
		Manifest: manifestID,
		Author:   opts.Author,
		Time:     now,
		Expiry:   opts.Expiry,
		Message:  opts.Message,
	}
	if hasParent {
		version.Parents = []hash.VersionID{parent}
	}

	data, err := version.Marshal()
	if err != nil {
		return hash.VersionID{}, err
	}
	id := hash.Version(data)
	if err := s.objects.Put(ctx, store.VersionKey(id), data); err != nil {
		return hash.VersionID{}, fmt.Errorf("store version: %w", err)
	}
	return id, nil
}

// recordHead points the space at the new version and keeps its manifest
// locally, so that status, clear and restore do not need the store to know
// what the space is supposed to contain (E16).
//
// The ref the space is on does not change here. Taking a snapshot must not
// move anyone off main - the timeline is a safety net running alongside the
// history, not a place to work from.
func (s *Space) recordHead(versionID hash.VersionID, manifestBytes []byte, advancesBase bool) error {
	ref, err := store.LocalRef(s.currentRef())
	if err != nil {
		return err
	}

	head := Head{Ref: ref, Version: versionID, Base: versionID}
	if !advancesBase {
		// A snapshot leaves the base where it was, so that a commit after any
		// number of snapshots still compares against the version it actually
		// descends from. A space with no head yet keeps the zero base, which
		// reads as "never committed".
		previous, err := s.Head()
		if err != nil && !errors.Is(err, errs.ErrRefNotFound) {
			return err
		}
		head.Base = previous.Base
	}

	if err := s.SetHead(head); err != nil {
		return err
	}
	return s.writeCheckedOutManifest(manifestBytes)
}

// writeCheckedOutManifest keeps the manifest of the current state locally, so
// that status, clear and restore do not need the store to know what the space
// is supposed to contain (E16).
func (s *Space) writeCheckedOutManifest(data []byte) error {
	return writeFileAtomic(filepath.Join(s.stateDir(), manifestFile), data)
}

// CheckedOutManifest reads the locally kept manifest of the current state.
func (s *Space) CheckedOutManifest() (object.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(s.stateDir(), manifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return object.Manifest{}, fmt.Errorf("%w: the space has no snapshot yet", errs.ErrRefNotFound)
	}
	if err != nil {
		return object.Manifest{}, fmt.Errorf("read checked-out manifest: %w", err)
	}
	return object.UnmarshalManifest(data)
}
