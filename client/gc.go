package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// GraceDefault is how recently an object may have been written and still be
// spared by garbage collection even though nothing references it yet.
//
// Without a grace period, GC races every in-flight upload: a client that has
// written its chunks but not yet the manifest naming them looks exactly like a
// client that wrote garbage. One hour is long enough for any single upload
// this design produces and short enough that abandoned uploads do not
// accumulate for long.
const GraceDefault = time.Hour

// GCOptions controls one collection.
type GCOptions struct {
	// Grace spares objects written within this window. Zero uses
	// GraceDefault; a negative value disables the protection, which only a
	// test should ever ask for.
	Grace time.Duration

	// Now is the reference time for the grace window.
	Now time.Time

	// DryRun reports what would be deleted without deleting anything.
	DryRun bool
}

// GCResult reports what a collection found.
type GCResult struct {
	Reachable int
	Scanned   int
	Deleted   int
	Spared    int // unreachable but inside the grace window
	Bytes     int64
}

// Collect deletes store objects that nothing reachable references (F-S4-06).
//
// This is the second data-loss-critical operation after the dirty check, and
// it is built the same way: the answer is computed positively rather than
// inferred. Reachability starts at every ref — deliberate refs and every
// snapshot the thinning schedule still keeps — and walks version to manifest
// to file object to chunk. An object is deleted only when it was not met on
// that walk.
//
// Three properties matter, and each has its own test:
//
//   - Expiring a snapshot never removes a chunk a reachable deliberate version
//     references. That is not a rule here; it follows from the walk, because
//     the deliberate version is a root of its own (E14, E12 addendum).
//   - A manifest reachable only through a snapshot keeps its chunks alive, for
//     the same reason.
//   - An object written during the run survives, through the grace window.
//
// Deletion goes through GCStore, which is the only interface in the system
// that can delete at all (E25). A caller holding an ordinary ObjectStore
// cannot reach this path even by mistake.
func Collect(ctx context.Context, objects store.GCStore, refs store.RefStore,
	storeDir string, opts GCOptions,
) (GCResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	grace := opts.Grace
	if grace == 0 {
		grace = GraceDefault
	}

	reachable, err := reachableObjects(ctx, objects, refs)
	if err != nil {
		return GCResult{}, err
	}

	candidates, err := walkStore(storeDir)
	if err != nil {
		return GCResult{}, err
	}

	result := GCResult{Reachable: len(reachable), Scanned: len(candidates)}
	return sweep(ctx, objects, candidates, reachable, sweepPolicy{
		grace: grace, now: now, dryRun: opts.DryRun,
	}, result)
}

// sweepPolicy is what the sweep decides by, kept together so that the decision
// is one struct rather than four parameters.
type sweepPolicy struct {
	grace  time.Duration
	now    time.Time
	dryRun bool
}

// sweep deletes what the walk did not reach and the grace window does not
// protect.
func sweep(ctx context.Context, objects store.GCStore, candidates []candidate,
	reachable map[store.Key]struct{}, policy sweepPolicy, result GCResult,
) (GCResult, error) {
	for _, c := range candidates {
		if _, live := reachable[c.key]; live {
			continue
		}
		if policy.grace >= 0 && policy.now.Sub(c.modified) < policy.grace {
			result.Spared++
			continue
		}

		if !policy.dryRun {
			if err := objects.Delete(ctx, []store.Key{c.key}); err != nil {
				return result, fmt.Errorf("delete %s: %w", c.key, err)
			}
		}
		result.Deleted++
		result.Bytes += c.size
	}
	return result, nil
}

// reachableObjects walks every root and collects the keys that must survive.
func reachableObjects(ctx context.Context, objects store.ObjectStore, refs store.RefStore) (map[store.Key]struct{}, error) {
	reachable := make(map[store.Key]struct{})

	roots, err := allRoots(ctx, refs)
	if err != nil {
		return nil, err
	}

	seen := make(map[hash.VersionID]struct{}, len(roots))
	pending := append([]hash.VersionID(nil), roots...)

	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("collect garbage: %w", err)
		}

		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, done := seen[current]; done {
			continue
		}
		seen[current] = struct{}{}

		version, err := markVersion(ctx, objects, current, reachable)
		if err != nil {
			return nil, err
		}
		pending = append(pending, version.Parents...)
	}
	return reachable, nil
}

func allRoots(ctx context.Context, refs store.RefStore) ([]hash.VersionID, error) {
	var roots []hash.VersionID

	for _, scope := range []store.Scope{store.ScopeLocal, store.ScopeRemote} {
		names, err := refs.List(ctx, scope)
		if err != nil {
			return nil, fmt.Errorf("list %s refs: %w", scope, err)
		}
		for _, name := range names {
			id, err := refs.Get(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("read ref %s: %w", name, err)
			}
			roots = append(roots, id)
		}
	}
	return roots, nil
}

// markVersion records a version and everything below it.
func markVersion(ctx context.Context, objects store.ObjectStore, id hash.VersionID,
	reachable map[store.Key]struct{},
) (object.Version, error) {
	data, err := objects.Get(ctx, store.VersionKey(id))
	if err != nil {
		return object.Version{}, fmt.Errorf("read version %s: %w", id, err)
	}
	version, err := object.UnmarshalVersion(data)
	if err != nil {
		return object.Version{}, err
	}
	reachable[store.VersionKey(id)] = struct{}{}

	if !version.Graph.IsZero() {
		reachable[store.GraphKey(version.Graph)] = struct{}{}
	}
	if err := markManifest(ctx, objects, version.Manifest, reachable); err != nil {
		return object.Version{}, err
	}
	return version, nil
}

func markManifest(ctx context.Context, objects store.ObjectStore, id hash.ManifestID,
	reachable map[store.Key]struct{},
) error {
	if _, done := reachable[store.ManifestKey(id)]; done {
		return nil
	}

	data, err := objects.Get(ctx, store.ManifestKey(id))
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", id, err)
	}
	m, err := object.UnmarshalManifest(data)
	if err != nil {
		return err
	}
	reachable[store.ManifestKey(id)] = struct{}{}

	for _, entry := range m.Entries {
		if err := markFile(ctx, objects, entry, reachable); err != nil {
			return err
		}
	}
	return nil
}

func markFile(ctx context.Context, objects store.ObjectStore, entry object.Entry,
	reachable map[store.Key]struct{},
) error {
	key := store.FileKey(entry.File)
	if _, done := reachable[key]; done {
		return nil
	}

	data, err := objects.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read file object for %s: %w", entry.Path, err)
	}
	file, err := object.UnmarshalFile(data)
	if err != nil {
		return fmt.Errorf("%s: %w", entry.Path, err)
	}
	reachable[key] = struct{}{}

	for _, ref := range file.Chunks {
		reachable[store.ChunkKey(ref.ID)] = struct{}{}
	}
	return nil
}

// candidate is one object found on disk.
type candidate struct {
	key      store.Key
	size     int64
	modified time.Time
}

// walkStore enumerates everything the store holds.
//
// This reaches into the filesystem layout rather than going through the store
// interface, because the interface deliberately offers no listing: E24 gives
// ObjectStore exactly Get, Put and Exists, and adding a List for the sake of
// garbage collection would hand every caller a way to enumerate the store.
// Keeping the enumeration here means the backend-specific part of GC is one
// function, and a future backend supplies its own.
func walkStore(storeDir string) ([]candidate, error) {
	root := filepath.Join(storeDir, "objects")

	var candidates []candidate
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		key, ok := keyFromPath(root, p)
		if !ok {
			return nil // not something this layout produced
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{key: key, size: info.Size(), modified: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: enumerate store: %w", errs.ErrInvalidStore, err)
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].key.String() < candidates[j].key.String() })
	return candidates, nil
}

// keyFromPath reverses the fan-out layout: objects/<type>/<ab>/<cd>/<rest>.
func keyFromPath(root, p string) (store.Key, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return store.Key{}, false
	}

	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 {
		return store.Key{}, false
	}
	return store.ParseKey(parts[0], parts[1]+parts[2]+parts[3])
}
