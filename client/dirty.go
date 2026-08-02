package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// ClearCheck is the result of the check that must pass before any local file
// is deleted (E17, CLAUDE.md invariant 6).
type ClearCheck struct {
	// Safe files are provably recoverable: re-hashed here, present in a
	// reachable version, and with every chunk confirmed in the store.
	Safe []string

	// Unversioned files are not in any reachable version. They do not abort
	// the check - they are snapshotted and pushed before deletion (E17).
	Unversioned []string

	// Ignored files are left in place and do not block (E18).
	Ignored      []string
	IgnoredBytes int64

	// Problems is why the check refused. Non-empty means nothing may be
	// deleted.
	Problems []string
}

// OK reports whether deleting is permitted.
func (c ClearCheck) OK() bool { return len(c.Problems) == 0 }

// Err turns a failed check into an error listing every reason, so that a user
// sees all of them at once rather than fixing them one run at a time.
func (c ClearCheck) Err() error {
	if c.OK() {
		return nil
	}
	return fmt.Errorf("%w: %s", errs.ErrDirty, joinProblems(c.Problems))
}

func joinProblems(problems []string) string {
	out := ""
	for i, p := range problems {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}

// CheckClear decides whether the working directory may be deleted.
//
// This is the trust question of the whole space feature, so it deliberately
// shares almost nothing with Status:
//
//	                   status, auto snapshot    space clear
//	data source        the status cache         a full re-hash, cache ignored
//	store existence    assumed                  positively confirmed, batched
//	when in doubt      continue                 abort
//
// The status cache is a heuristic - mtime lies across clock jumps, with tools
// that preserve timestamps, and on network shares with coarse resolution. A
// heuristic is fine for telling a user what changed. It is not fine for
// deciding what may be deleted, so every file is read and hashed again here,
// however long that takes. For 200 GB with BLAKE3 that is one to two minutes;
// for an operation that deletes data it is the right side of the trade.
//
// Three checks, all mandatory (E17):
//
//  1. Re-hash every local file to its FileID.
//  2. Every FileID must appear in a reachable version or snapshot.
//  3. Every referenced chunk must exist in the store - not just locally.
//
// The third is the one people forget. A snapshot created locally and never
// synchronized sits entirely on the very disk about to be cleared, so without
// this check "it is versioned" means nothing.
func (s *Space) CheckClear(ctx context.Context, ignore *Ignore) (ClearCheck, error) {
	var check ClearCheck

	if err := s.confirmStoreHoldsHead(ctx); err != nil {
		check.Problems = append(check.Problems, err.Error())
		return check, nil
	}

	reachable, err := s.reachableFiles(ctx)
	if err != nil {
		// A store we cannot read is not a store we may trust with the only
		// copy. This is the abort E17 asks for, not a degraded mode.
		check.Problems = append(check.Problems,
			fmt.Sprintf("the store could not be read, so nothing can be confirmed: %v", err))
		return check, nil
	}

	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return ClearCheck{}, err
	}
	if err := s.collectIgnored(ignore, &check); err != nil {
		return ClearCheck{}, err
	}

	candidates := s.rehashAll(files, reachable, &check)
	s.confirmChunks(ctx, candidates, reachable, &check)

	sort.Strings(check.Safe)
	sort.Strings(check.Unversioned)
	return check, nil
}

// confirmStoreHoldsHead is the "positively confirmed" half of E17, applied to
// the store itself rather than to a single object.
//
// Without it, a store that has vanished is indistinguishable from a store that
// is merely empty: every local file comes back unversioned, nothing is
// reported as a problem, and the clear proceeds by re-uploading everything
// into whatever directory now sits at that path. On an unmounted network share
// that means writing the only copy to the mount point instead of to the store.
//
// So the space asks a question with a known answer: it has a head, therefore
// the store must be able to produce that version. A store that cannot is not
// one to delete anything against.
func (s *Space) confirmStoreHoldsHead(ctx context.Context) error {
	head, err := s.Head()
	if errors.Is(err, errs.ErrRefNotFound) {
		return nil // a space that never snapshotted has nothing to confirm
	}
	if err != nil {
		return fmt.Errorf("the space head could not be read: %w", err)
	}

	if _, err := s.objects.Get(ctx, store.VersionKey(head.Version)); err != nil {
		return fmt.Errorf("the store cannot produce the current version %s, so it cannot be trusted "+
			"with the only copy: %w", head.Version, err)
	}
	return nil
}

// rehashAll is check 1 and check 2: every file is read and hashed again, and
// the result is looked up among the FileIDs a reachable version references.
func (s *Space) rehashAll(files []ScannedFile, reachable map[hash.FileID]object.File,
	check *ClearCheck,
) map[hash.FileID][]string {
	candidates := make(map[hash.FileID][]string, len(files))

	for _, f := range files {
		id, err := s.rehash(f)
		if err != nil {
			check.Problems = append(check.Problems, fmt.Sprintf("%s could not be read: %v", f.Path, err))
			continue
		}
		if _, versioned := reachable[id]; !versioned {
			check.Unversioned = append(check.Unversioned, f.Path)
			continue
		}
		candidates[id] = append(candidates[id], f.Path)
	}
	return candidates
}

// rehash reads a file and computes its FileID, ignoring the cache entirely.
func (s *Space) rehash(f ScannedFile) (hash.FileID, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(f.Path))
	file, err := os.Open(abs) //nolint:gosec // a path produced by scanning this space
	if err != nil {
		return hash.FileID{}, err
	}
	defer file.Close() //nolint:errcheck // read-only

	return hash.FileOf(file)
}

// reachableFiles collects every FileID that a reachable version references,
// walking the snapshot chain and the checked-out ref.
func (s *Space) reachableFiles(ctx context.Context) (map[hash.FileID]object.File, error) {
	heads, err := s.reachableHeads(ctx)
	if err != nil {
		return nil, err
	}

	reachable := make(map[hash.FileID]object.File)
	seen := make(map[hash.VersionID]struct{})

	for _, head := range heads {
		if err := s.walkVersions(ctx, head, seen, reachable); err != nil {
			return nil, err
		}
	}
	return reachable, nil
}

func (s *Space) reachableHeads(ctx context.Context) ([]hash.VersionID, error) {
	names, err := s.refs.List(ctx, store.ScopeLocal)
	if err != nil {
		return nil, fmt.Errorf("list local refs: %w", err)
	}

	heads := make([]hash.VersionID, 0, len(names))
	for _, name := range names {
		id, err := s.refs.Get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("read ref %s: %w", name, err)
		}
		heads = append(heads, id)
	}
	return heads, nil
}

// walkVersions follows the parent chain, collecting the file objects every
// manifest along the way references.
func (s *Space) walkVersions(ctx context.Context, id hash.VersionID,
	seen map[hash.VersionID]struct{}, reachable map[hash.FileID]object.File,
) error {
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
		if err := s.collectManifestFiles(ctx, version.Manifest, reachable); err != nil {
			return err
		}
		pending = append(pending, version.Parents...)
	}
	return nil
}

func (s *Space) readVersion(ctx context.Context, id hash.VersionID) (object.Version, error) {
	data, err := s.objects.Get(ctx, store.VersionKey(id))
	if err != nil {
		return object.Version{}, fmt.Errorf("read version %s: %w", id, err)
	}
	return object.UnmarshalVersion(data)
}

func (s *Space) collectManifestFiles(ctx context.Context, id hash.ManifestID, reachable map[hash.FileID]object.File) error {
	data, err := s.objects.Get(ctx, store.ManifestKey(id))
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", id, err)
	}
	m, err := object.UnmarshalManifest(data)
	if err != nil {
		return err
	}

	for _, entry := range m.Entries {
		if _, have := reachable[entry.File]; have {
			continue
		}
		fileData, err := s.objects.Get(ctx, store.FileKey(entry.File))
		if err != nil {
			return fmt.Errorf("read file object for %s: %w", entry.Path, err)
		}
		file, err := object.UnmarshalFile(fileData)
		if err != nil {
			return err
		}
		reachable[entry.File] = file
	}
	return nil
}

// confirmChunks is check 3: every chunk of every candidate file must be
// present in the store, confirmed by a batch query rather than assumed.
//
// The query is batched because it has to be: a per-chunk lookup over a 200 GB
// tree is a hundred thousand round trips, which on a remote store turns the
// safest operation in the system into the slowest.
func (s *Space) confirmChunks(ctx context.Context, candidates map[hash.FileID][]string,
	reachable map[hash.FileID]object.File, check *ClearCheck,
) {
	if len(candidates) == 0 {
		return
	}

	keys, owners := chunkKeysOf(candidates, reachable, check)
	if len(keys) == 0 {
		// Every candidate is an empty file: nothing to confirm, and nothing
		// to lose by deleting it.
		// Every candidate is an empty file: nothing to confirm, and nothing
		// to lose by deleting it.
		for _, paths := range candidates {
			check.Safe = append(check.Safe, paths...)
		}
		return
	}

	present, err := s.objects.Exists(ctx, keys)
	if err != nil {
		check.Problems = append(check.Problems, fmt.Sprintf("the store could not confirm chunks: %v", err))
		return
	}

	missing := make(map[string]struct{})
	for i, ok := range present {
		if !ok {
			for _, p := range owners[i] {
				missing[p] = struct{}{}
			}
		}
	}

	for _, paths := range candidates {
		for _, p := range paths {
			if _, bad := missing[p]; bad {
				check.Problems = append(check.Problems,
					fmt.Sprintf("%s has chunks that are not in the store yet", p))
				continue
			}
			check.Safe = append(check.Safe, p)
		}
	}
}

// chunkKeysOf flattens the chunk lists of every candidate into one batch, and
// remembers which paths depend on each key.
func chunkKeysOf(candidates map[hash.FileID][]string, reachable map[hash.FileID]object.File,
	check *ClearCheck,
) ([]store.Key, [][]string) {
	ids := make([]hash.FileID, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	index := make(map[store.Key]int)
	var keys []store.Key
	var owners [][]string

	for _, id := range ids {
		file, ok := reachable[id]
		if !ok {
			check.Problems = append(check.Problems,
				fmt.Sprintf("the file object for %s is missing from the store", id))
			continue
		}
		for _, ref := range file.Chunks {
			key := store.ChunkKey(ref.ID)
			at, seen := index[key]
			if !seen {
				at = len(keys)
				index[key] = at
				keys = append(keys, key)
				owners = append(owners, nil)
			}
			// One entry per FileID rather than per path: several paths can
			// share content, and a missing chunk affects all of them equally.
			owners[at] = append(owners[at], candidates[id][0])
		}
	}
	return keys, owners
}
