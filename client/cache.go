package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/path"
	"github.com/dekorlp/fibula/store"
)

// CacheEntry is what the space remembers about one working-directory file
// (E16).
//
// The chunk list is not in here. It lives in the file object under the same
// FileID in the space's local store, which keeps this file one short line per
// asset while still meeting E16's requirement that the chunk list be known
// without re-chunking.
type CacheEntry struct {
	Path  string
	MTime int64 // Unix nanoseconds, as reported by the filesystem
	Size  int64
	File  hash.FileID
}

// Cache is the status cache: the heuristic that makes `status` on a 200 GB
// tree finish without hashing anything.
//
// It is a heuristic and nothing more. mtime lies across clock jumps, with
// tools that preserve timestamps, and on network shares with coarse
// resolution. That is tolerable for status and for auto snapshots, and it is
// explicitly not tolerable before deleting data — which is why the dirty check
// ignores this file entirely (E17).
type Cache struct {
	entries map[string]CacheEntry
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{entries: map[string]CacheEntry{}} }

// Lookup returns the remembered entry for a path.
func (c *Cache) Lookup(p string) (CacheEntry, bool) {
	entry, ok := c.entries[p]
	return entry, ok
}

// Put records an entry.
func (c *Cache) Put(entry CacheEntry) { c.entries[entry.Path] = entry }

// Paths returns every remembered path, sorted.
func (c *Cache) Paths() []string {
	paths := make([]string, 0, len(c.entries))
	for p := range c.entries {
		paths = append(paths, p)
	}
	path.Sort(paths)
	return paths
}

// Matches reports whether a file on disk still looks like the remembered one.
// Size and mtime together are the heuristic; neither alone is worth much.
func (e CacheEntry) Matches(size, mtime int64) bool {
	return e.Size == size && e.MTime == mtime
}

// LoadCache reads the status cache. A missing cache is not an error — it means
// everything has to be looked at, which is correct for a fresh space.
func (s *Space) LoadCache() (*Cache, error) {
	data, err := os.ReadFile(filepath.Join(s.stateDir(), cacheFile))
	if errors.Is(err, os.ErrNotExist) {
		return NewCache(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read status cache: %w", err)
	}

	cache := NewCache()
	if len(data) == 0 {
		return cache, nil
	}

	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		entry, err := parseCacheLine(line)
		if err != nil {
			// A damaged cache is not a reason to fail: it is a heuristic, and
			// throwing it away only costs the next scan some time. Silently
			// trusting half of it would be worse.
			return NewCache(), nil //nolint:nilerr // deliberate, see comment
		}
		cache.Put(entry)
	}
	return cache, nil
}

func parseCacheLine(line string) (CacheEntry, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != 4 {
		return CacheEntry{}, fmt.Errorf("%w: %d fields, want 4", errs.ErrMalformedObject, len(fields))
	}

	if err := path.Validate(fields[0]); err != nil {
		return CacheEntry{}, err
	}
	mtime, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return CacheEntry{}, fmt.Errorf("mtime: %w", err)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return CacheEntry{}, fmt.Errorf("size: %w", err)
	}
	file, err := hash.ParseFileID(fields[3])
	if err != nil {
		return CacheEntry{}, err
	}
	return CacheEntry{Path: fields[0], MTime: mtime, Size: size, File: file}, nil
}

// SaveCache writes the status cache, sorted so that two runs over the same
// tree produce the same file.
func (s *Space) SaveCache(c *Cache) error {
	paths := c.Paths()
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		e := c.entries[p]
		fmt.Fprintf(&b, "%s\t%d\t%d\t%s\n", e.Path, e.MTime, e.Size, e.File)
	}
	return writeFileAtomic(filepath.Join(s.stateDir(), cacheFile), []byte(b.String()))
}

// PutFileObject remembers the chunk list of a file locally (E16).
func (s *Space) PutFileObject(ctx context.Context, id hash.FileID, file object.File) error {
	data, err := file.Marshal()
	if err != nil {
		return fmt.Errorf("marshal file object: %w", err)
	}
	if err := s.local.Put(ctx, store.FileKey(id), data); err != nil {
		return fmt.Errorf("cache chunk list for %s: %w", id, err)
	}
	return nil
}

// FileObject returns a remembered chunk list, if the space has one.
func (s *Space) FileObject(ctx context.Context, id hash.FileID) (object.File, bool, error) {
	data, err := s.local.Get(ctx, store.FileKey(id))
	if errors.Is(err, errs.ErrObjectNotFound) {
		return object.File{}, false, nil
	}
	if err != nil {
		return object.File{}, false, fmt.Errorf("read cached chunk list for %s: %w", id, err)
	}

	file, err := object.UnmarshalFile(data)
	if err != nil {
		return object.File{}, false, fmt.Errorf("cached chunk list for %s: %w", id, err)
	}
	return file, true, nil
}
