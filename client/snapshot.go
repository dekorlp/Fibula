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

// SnapshotRef is the local ref holding the head of the snapshot chain.
//
// Auto snapshots are reachable only through this chain, never from a
// deliberate ref (E12). The chain is a timeline rather than a content graph:
// it stays linear even when the working state changes completely in between.
const SnapshotRef = "snapshots"

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
	Hashed   int   // files that had to be read and chunked
	Uploaded int64 // bytes of chunk content written to the store
}

// Snapshot records the working directory as a version.
//
// Unchanged files are taken from the status cache and never read: that is the
// entire point of the cache, and on a 200 GB tree it is the difference between
// a snapshot every few minutes and a snapshot nobody can afford to take. A
// file whose size or mtime moved is re-chunked, its chunks written to the
// store, and its new chunk list remembered locally (E16).
func (s *Space) Snapshot(ctx context.Context, ignore *Ignore, opts SnapshotOptions) (SnapshotResult, error) {
	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return SnapshotResult{}, err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return SnapshotResult{}, err
	}

	next := NewCache()
	var builder manifest.Builder
	result := SnapshotResult{Files: len(files)}

	for _, f := range files {
		entry, uploaded, hashed, err := s.snapshotFile(ctx, cache, f)
		if err != nil {
			return SnapshotResult{}, err
		}
		if hashed {
			result.Hashed++
		}
		result.Uploaded += uploaded

		next.Put(entry)
		if err := builder.Add(entry.Path, entry.File, entry.Size); err != nil {
			return SnapshotResult{}, err
		}
	}

	result.Version, result.Manifest, err = s.commit(ctx, builder, opts)
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

// commit writes the manifest and the version, and advances the snapshot chain.
func (s *Space) commit(ctx context.Context, builder manifest.Builder, opts SnapshotOptions) (hash.VersionID, hash.ManifestID, error) {
	manifestID, manifestBytes, err := s.putManifest(ctx, builder)
	if err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}

	parent, hasParent, err := s.snapshotHead(ctx)
	if err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}

	versionID, err := s.putVersion(ctx, manifestID, parent, hasParent, opts)
	if err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}
	if err := s.advanceSnapshotChain(ctx, parent, hasParent, versionID); err != nil {
		return hash.VersionID{}, hash.ManifestID{}, err
	}
	if err := s.recordHead(versionID, manifestBytes); err != nil {
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

	id := hash.Manifest(data)
	if err := s.objects.Put(ctx, store.ManifestKey(id), data); err != nil {
		return hash.ManifestID{}, nil, fmt.Errorf("store manifest: %w", err)
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
func (s *Space) recordHead(versionID hash.VersionID, manifestBytes []byte) error {
	ref, err := store.LocalRef(SnapshotRef)
	if err != nil {
		return err
	}
	if err := s.SetHead(Head{Ref: ref, Version: versionID}); err != nil {
		return err
	}
	return s.writeCheckedOutManifest(manifestBytes)
}

func (s *Space) snapshotHead(ctx context.Context) (hash.VersionID, bool, error) {
	ref, err := store.LocalRef(SnapshotRef)
	if err != nil {
		return hash.VersionID{}, false, err
	}

	current, err := s.refs.Get(ctx, ref)
	if errors.Is(err, errs.ErrRefNotFound) {
		return hash.VersionID{}, false, nil
	}
	if err != nil {
		return hash.VersionID{}, false, fmt.Errorf("read snapshot chain: %w", err)
	}
	return current, true, nil
}

func (s *Space) advanceSnapshotChain(ctx context.Context, parent hash.VersionID, hasParent bool, next hash.VersionID) error {
	ref, err := store.LocalRef(SnapshotRef)
	if err != nil {
		return err
	}

	expected := hash.VersionID{}
	if hasParent {
		expected = parent
	}
	if err := s.refs.CompareAndSwap(ctx, ref, expected, next); err != nil {
		return fmt.Errorf("advance snapshot chain: %w", err)
	}
	return nil
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
