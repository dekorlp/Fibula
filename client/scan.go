package client

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	fpath "github.com/dekorlp/fibula/path"
)

// ScannedFile is one working-directory file as the scanner found it.
type ScannedFile struct {
	Path  string // normalized, relative to the space root
	Size  int64
	MTime int64 // Unix nanoseconds
}

// Scan walks the working directory and returns every file Fibula considers
// part of the space, sorted by path.
//
// It never reads file content. That is what makes `status` on a large tree
// cheap, and it is also the boundary that keeps this function honest: deciding
// whether a file changed is a separate question, answered by the cache for
// status and by re-hashing for the dirty check.
//
// Symbolic links are skipped rather than followed. Following them would let a
// link into another project pull that project's content into this manifest,
// and would make a cycle an infinite walk. Recording them as links is a format
// question that E7 has already answered in the negative: assets are data.
func (s *Space) Scan(ctx context.Context, ignore *Ignore) ([]ScannedFile, error) {
	var files []ScannedFile

	err := filepath.WalkDir(s.root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		file, keep, err := s.consider(abs, d, ignore)
		if err != nil || !keep {
			return err
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", s.root, err)
	}

	sort.Slice(files, func(i, j int) bool { return fpath.Less(files[i].Path, files[j].Path) })
	return files, nil
}

// consider decides what one walk entry is: a directory to descend or skip, a
// file to record, or something that is not an asset at all.
func (s *Space) consider(abs string, d fs.DirEntry, ignore *Ignore) (ScannedFile, bool, error) {
	rel, err := relativePath(s.root, abs)
	if err != nil {
		return ScannedFile{}, false, err
	}
	if rel == "" {
		return ScannedFile{}, false, nil // the root itself
	}

	if d.IsDir() {
		return ScannedFile{}, false, skipIgnoredDir(rel, ignore)
	}
	if !d.Type().IsRegular() || ignore.Match(rel, false) {
		return ScannedFile{}, false, nil
	}

	info, err := d.Info()
	if err != nil {
		return ScannedFile{}, false, fmt.Errorf("stat %s: %w", rel, err)
	}
	return ScannedFile{Path: rel, Size: info.Size(), MTime: info.ModTime().UnixNano()}, true, nil
}

// skipIgnoredDir decides whether to descend. The space's own directory is
// always skipped, and an ignored directory takes its whole subtree with it —
// which mirrors git and means clearing can report ignored content without
// walking into it.
func skipIgnoredDir(rel string, ignore *Ignore) error {
	if rel == SpaceDir || strings.HasPrefix(rel, SpaceDir+"/") {
		return filepath.SkipDir
	}
	if ignore.Match(rel, true) {
		return filepath.SkipDir
	}
	return nil
}

// relativePath turns an absolute walk path into a normalized Fibula path.
func relativePath(root, abs string) (string, error) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("relative path of %s: %w", abs, err)
	}
	if rel == "." {
		return "", nil
	}

	normalized, err := fpath.Normalize(filepath.ToSlash(rel))
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	return normalized, nil
}

// Status is what changed in the working directory since the last snapshot.
type Status struct {
	Added    []string
	Modified []string
	Removed  []string

	// Unchanged counts the files the cache vouched for without any hashing.
	Unchanged int
}

// Status compares the working directory against the status cache (E16).
//
// It is deliberately cache-backed and therefore heuristic: no file content is
// read, so an unchanged 200 GB tree is answered from metadata alone. mtime can
// lie, and for status that is an acceptable trade — for deleting data it is
// not, which is why ClearCheck ignores the cache entirely (E17).
func (s *Space) Status(ctx context.Context, ignore *Ignore) (Status, error) {
	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return Status{}, err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return Status{}, err
	}

	var status Status
	seen := make(map[string]struct{}, len(files))

	for _, f := range files {
		seen[f.Path] = struct{}{}

		entry, known := cache.Lookup(f.Path)
		switch {
		case !known:
			status.Added = append(status.Added, f.Path)
		case entry.Matches(f.Size, f.MTime):
			status.Unchanged++
		default:
			status.Modified = append(status.Modified, f.Path)
		}
	}

	for _, p := range cache.Paths() {
		if _, still := seen[p]; !still {
			status.Removed = append(status.Removed, p)
		}
	}
	return status, nil
}

// IsClean reports whether the working directory matches the cache.
func (st Status) IsClean() bool {
	return len(st.Added)+len(st.Modified)+len(st.Removed) == 0
}

// TotalSize adds up the working directory, which is what the storage budget is
// measured against (E15).
func TotalSize(files []ScannedFile) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}

// statFile reads the metadata the cache compares against.
func statFile(root, rel string) (int64, int64, error) {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return 0, 0, fmt.Errorf("stat %s: %w", rel, err)
	}
	return info.Size(), info.ModTime().UnixNano(), nil
}
