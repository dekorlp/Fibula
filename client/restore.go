package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/object"
	fpath "github.com/dekorlp/fibula/path"
	"github.com/dekorlp/fibula/store"
)

// windowsReserved are the device names Windows refuses as a file name, with or
// without an extension: CON.txt is as impossible as CON.
var windowsReserved = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// SafeJoin turns a manifest path into an absolute path inside root, or refuses.
//
// Manifest paths are untrusted input even in single-user operation: a manifest
// can come from a store somebody else wrote to, and a store is exactly the
// place a presigned URL lets an attacker put things (object model § 8). So
// this is a security boundary and not a convenience helper.
//
// What it rejects:
//
//   - anything fpath.Validate rejects: absolute paths, "..", control
//     characters, backslashes, non-NFC forms
//   - a resolved location outside root, checked lexically after cleaning
//   - a path whose parent chain leaves root through a symlink
//   - on Windows, reserved device names and drive prefixes
//
// The symlink check is the one that needs the filesystem: a manifest entry
// "assets/textures/evil.png" is harmless on its own, but if "assets/textures"
// already exists as a link to /etc, writing it escapes the target directory
// without the path itself ever containing "..".
func SafeJoin(root, manifestPath string) (string, error) {
	if err := fpath.Validate(manifestPath); err != nil {
		return "", fmt.Errorf("%w: %w", errs.ErrUnsafePath, err)
	}
	if err := checkReservedNames(manifestPath, runtime.GOOS == "windows"); err != nil {
		return "", err
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve target directory: %w", err)
	}

	target := filepath.Join(absRoot, filepath.FromSlash(manifestPath))
	if !withinRoot(absRoot, target) {
		return "", fmt.Errorf("%w: %q resolves outside the target directory", errs.ErrUnsafePath, manifestPath)
	}
	if err := checkNoSymlinkEscape(absRoot, manifestPath); err != nil {
		return "", err
	}
	return target, nil
}

// withinRoot compares cleaned paths rather than strings, so that neither a
// trailing separator nor a "." segment can make an outside path look inside.
func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkNoSymlinkEscape walks the parent chain and refuses if any existing
// component is a symbolic link.
//
// Refusing outright is stricter than resolving the link and checking where it
// lands. That is deliberate: a working directory where a parent of an asset is
// a link is not a situation Fibula can restore into meaningfully, and the
// strict answer has no race between the check and the write that a
// resolve-then-compare would have.
func checkNoSymlinkEscape(root, manifestPath string) error {
	current := root

	segments := strings.Split(manifestPath, "/")
	for _, segment := range segments[:len(segments)-1] {
		current = filepath.Join(current, segment)

		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil // nothing exists from here on, so nothing can escape
		}
		if err != nil {
			return fmt.Errorf("%w: cannot inspect %q: %w", errs.ErrUnsafePath, segment, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q passes through the symbolic link %q",
				errs.ErrUnsafePath, manifestPath, segment)
		}
	}
	return nil
}

// checkReservedNames rejects Windows device names, on Windows only.
//
// Only there, because the name is legal everywhere else and a Linux studio
// must not be blocked from restoring a file they legitimately created. The
// consequence is honest rather than hidden: a manifest containing aux.png
// restores on Linux and fails with a clear error on Windows, which is a
// property of the filesystem and not something Fibula can paper over.
//
// The platform is a parameter rather than a package-level lookup, so that the
// rule can be tested on any host. A check that only ever runs on one CI runner
// is a check nobody watches.
func checkReservedNames(manifestPath string, windows bool) error {
	if !windows {
		return nil
	}

	for _, segment := range strings.Split(manifestPath, "/") {
		base, _, _ := strings.Cut(segment, ".")
		if _, reserved := windowsReserved[strings.ToUpper(base)]; reserved {
			return fmt.Errorf("%w: %q contains %q, which Windows reserves as a device name",
				errs.ErrUnsafePath, manifestPath, segment)
		}
		if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
			return fmt.Errorf("%w: %q has a segment ending in a space or dot, which Windows cannot create",
				errs.ErrUnsafePath, manifestPath)
		}
	}
	return nil
}

// RestoreResult reports what a restore wrote.
type RestoreResult struct {
	Files int
	Bytes int64
}

// Restore writes the checked-out manifest state back into the working
// directory (E15): after clearing, restoring is a pure download.
//
// Every path is checked before anything is written, so a manifest containing
// one hostile entry cannot write the harmless entries that precede it and only
// then fail. Aborting halfway would be its own kind of damage.
func (s *Space) Restore(ctx context.Context) (RestoreResult, error) {
	m, err := s.CheckedOutManifest()
	if err != nil {
		return RestoreResult{}, err
	}
	return s.RestoreManifest(ctx, m)
}

// RestoreManifest writes an arbitrary manifest into the space.
func (s *Space) RestoreManifest(ctx context.Context, m object.Manifest) (RestoreResult, error) {
	targets := make([]string, len(m.Entries))
	for i, entry := range m.Entries {
		target, err := SafeJoin(s.root, entry.Path)
		if err != nil {
			return RestoreResult{}, err
		}
		targets[i] = target
	}

	var result RestoreResult
	for i, entry := range m.Entries {
		written, err := s.restoreEntry(ctx, targets[i], entry)
		if err != nil {
			return result, err
		}
		result.Files++
		result.Bytes += written
	}

	if err := s.refreshCacheFrom(ctx, m); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Space) restoreEntry(ctx context.Context, target string, entry object.Entry) (int64, error) {
	fileData, err := s.objects.Get(ctx, store.FileKey(entry.File))
	if err != nil {
		return 0, fmt.Errorf("read file object for %s: %w", entry.Path, err)
	}
	file, err := object.UnmarshalFile(fileData)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", entry.Path, err)
	}

	if err := os.MkdirAll(filepath.Dir(target), statePerm); err != nil {
		return 0, fmt.Errorf("create directory for %s: %w", entry.Path, err)
	}

	// O_EXCL is not used: restoring over an existing file is the normal case
	// after a partial restore. What must not happen is following a link, which
	// SafeJoin has already refused for the parent chain; O_NOFOLLOW would be
	// the belt-and-braces answer but is not portable to Windows.
	out, err := os.Create(target) //nolint:gosec // target came from SafeJoin
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", entry.Path, err)
	}
	defer out.Close() //nolint:errcheck // the size is verified below

	var written int64
	for i, ref := range file.Chunks {
		data, err := s.objects.Get(ctx, store.ChunkKey(ref.ID))
		if err != nil {
			return 0, fmt.Errorf("read chunk %d of %s: %w", i, entry.Path, err)
		}
		n, err := out.Write(data)
		if err != nil {
			return 0, fmt.Errorf("write %s: %w", entry.Path, err)
		}
		written += int64(n)
	}

	if written != entry.Size {
		return 0, fmt.Errorf("%w: %s restored to %d bytes, the manifest says %d",
			errs.ErrCorruptObject, entry.Path, written, entry.Size)
	}
	return written, out.Sync()
}

// refreshCacheFrom rebuilds the status cache from what was just written, so
// that the next status call does not report the whole restored tree as
// modified.
func (s *Space) refreshCacheFrom(ctx context.Context, m object.Manifest) error {
	cache := NewCache()

	for _, entry := range m.Entries {
		size, mtime, err := statFile(s.root, entry.Path)
		if err != nil {
			return err
		}
		cache.Put(CacheEntry{Path: entry.Path, MTime: mtime, Size: size, File: entry.File})

		if file, err := s.fetchFileObject(ctx, entry); err == nil {
			if err := s.PutFileObject(ctx, entry.File, file); err != nil {
				return err
			}
		}
	}
	return s.SaveCache(cache)
}

func (s *Space) fetchFileObject(ctx context.Context, entry object.Entry) (object.File, error) {
	data, err := s.objects.Get(ctx, store.FileKey(entry.File))
	if err != nil {
		return object.File{}, err
	}
	return object.UnmarshalFile(data)
}
