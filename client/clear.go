package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/dekorlp/fibula/errs"
)

// ClearResult reports what clearing removed and what it left behind.
type ClearResult struct {
	Deleted      int
	DeletedBytes int64

	// Snapshotted is set when unversioned files were found: they were
	// snapshotted and pushed before being deleted, rather than aborting the
	// operation (E17).
	Snapshotted int

	// Ignored files stayed in place. Reporting them is required: a user who
	// expects a cleared space and finds 12 GB still on disk needs to be told
	// why, not left to guess.
	Ignored      int
	IgnoredBytes int64
}

// ClearOptions controls one clear.
type ClearOptions struct {
	// Author is recorded on the snapshot taken for unversioned files.
	Author string

	// IncludeIgnored deletes ignored files too. Off by default: they were
	// never in the store, so deleting them silently would be data loss on
	// exactly the files Fibula never protected (E18).
	IncludeIgnored bool

	// Now is the timestamp for the snapshot of unversioned files.
	Now time.Time
}

// Clear deletes the asset files of a space and keeps its local state (E15).
//
// The space keeps its identity and its history; only content leaves. Restoring
// afterwards is a pure download, because the manifest of the current state
// stays in .fibula/ and is a few KB rather than a few hundred gigabytes.
//
// Nothing is deleted until CheckClear passes. Unversioned files do not abort
// it: they are snapshotted and pushed first, and only then deleted. Aborting
// with "there is something unversioned here" would be technically correct and
// would teach users to reach for --force, which is how a safety feature stops
// being one (E17).
func (s *Space) Clear(ctx context.Context, ignore *Ignore, opts ClearOptions) (ClearResult, error) {
	check, err := s.CheckClear(ctx, ignore)
	if err != nil {
		return ClearResult{}, err
	}
	if !check.OK() {
		return ClearResult{}, check.Err()
	}

	var result ClearResult
	if len(check.Unversioned) > 0 {
		result.Snapshotted = len(check.Unversioned)
		if check, err = s.snapshotUnversioned(ctx, ignore, opts); err != nil {
			return ClearResult{}, err
		}
	}

	deletable := check.Safe
	if opts.IncludeIgnored {
		deletable = append(append([]string(nil), deletable...), check.Ignored...)
		sort.Strings(deletable)
	} else {
		result.Ignored = len(check.Ignored)
		result.IgnoredBytes = check.IgnoredBytes
	}

	for _, p := range deletable {
		size, err := s.deleteFile(p)
		if err != nil {
			return result, err
		}
		result.Deleted++
		result.DeletedBytes += size
	}

	if err := s.pruneEmptyDirs(); err != nil {
		return result, err
	}
	return result, nil
}

// snapshotUnversioned records the files no version knows and then re-runs the
// check.
//
// Re-checking rather than trusting the snapshot is the point: the snapshot
// wrote to the store, and the only acceptable evidence that it arrived is
// asking the store again. Anything else would be assuming exactly what E17
// forbids assuming.
func (s *Space) snapshotUnversioned(ctx context.Context, ignore *Ignore, opts ClearOptions) (ClearCheck, error) {
	snapshot, err := s.Snapshot(ctx, ignore, SnapshotOptions{
		Author: opts.Author,
		Expiry: expiryFor(opts.Now),
		Now:    opts.Now,
	})
	if err != nil {
		return ClearCheck{}, fmt.Errorf("snapshot unversioned files before clearing: %w", err)
	}

	check, err := s.CheckClear(ctx, ignore)
	if err != nil {
		return ClearCheck{}, err
	}
	if !check.OK() {
		return ClearCheck{}, check.Err()
	}
	if len(check.Unversioned) > 0 {
		return ClearCheck{}, fmt.Errorf("%w: %d files are still unversioned after snapshot %s",
			errs.ErrDirty, len(check.Unversioned), snapshot.Version)
	}
	return check, nil
}

func (s *Space) deleteFile(rel string) (int64, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))

	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", rel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// The scanner never records links, so one here appeared after the
		// check. Removing it is not what was approved.
		return 0, fmt.Errorf("%w: %s became a symbolic link after the check", errs.ErrDirty, rel)
	}

	// A read-only file cannot be removed on Windows, and this path runs
	// through the dirty check: failing here would leave the user believing a
	// space was cleared when it was not (E17, F-S5a-06).
	if err := s.makeWritable(rel); err != nil {
		return 0, err
	}

	if err := os.Remove(abs); err != nil {
		return 0, fmt.Errorf("delete %s: %w", rel, err)
	}
	return info.Size(), nil
}

// pruneEmptyDirs removes directories left empty by clearing, deepest first, so
// that a cleared space looks cleared rather than like an empty skeleton. The
// space directory and anything still holding a file are left alone.
func (s *Space) pruneEmptyDirs() error {
	var dirs []string

	err := filepath.WalkDir(s.root, func(abs string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := relativePath(s.root, abs)
		if relErr != nil || rel == "" {
			return nil //nolint:nilerr // a path we cannot normalize is not ours to touch
		}
		if !d.IsDir() {
			return nil
		}
		if rel == SpaceDir {
			return filepath.SkipDir
		}
		dirs = append(dirs, abs)
		return nil
	})
	if err != nil {
		return fmt.Errorf("collect directories: %w", err)
	}

	// Deepest first, so that a parent becomes empty before it is visited.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read %s: %w", dir, err)
		}
		if len(entries) == 0 {
			if err := os.Remove(dir); err != nil {
				return fmt.Errorf("remove empty directory %s: %w", dir, err)
			}
		}
	}
	return nil
}

// Budget reports how much the working directory currently occupies and whether
// the configured budget is reached (E15).
//
// Reaching it triggers a snapshot and a suggestion to clear. It never triggers
// a deletion - the space manager suggests, the user decides.
func (s *Space) Budget(ctx context.Context, ignore *Ignore) (used int64, reached bool, err error) {
	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return 0, false, err
	}

	used = TotalSize(files)
	return used, s.config.Budget > 0 && used >= s.config.Budget, nil
}

// expiryFor is the retention of an auto snapshot (E14).
//
// The thinning schedule - hourly for a day, daily for a month, weekly for six
// months - is a property of expiry-driven pruning and belongs to S4. Until
// that exists, an auto snapshot is given the outer bound of that schedule, so
// that nothing created here can expire before the pruner that understands the
// schedule is written.
func expiryFor(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	return now.AddDate(0, 6, 0)
}
