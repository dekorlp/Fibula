package client

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// collectIgnored measures what will be left behind. Ignored files stay in
// place when clearing and do not block it either (E18) - deleting them
// silently would be data loss on exactly the files that were never in the
// store.
func (s *Space) collectIgnored(ignore *Ignore, check *ClearCheck) error {
	if ignore.Empty() {
		return nil
	}

	err := filepath.WalkDir(s.root, func(abs string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return s.measureIgnored(abs, d, ignore, check)
	})
	if err != nil {
		return fmt.Errorf("measure ignored files: %w", err)
	}

	sort.Strings(check.Ignored)
	return nil
}

// measureIgnored records one entry if it is ignored. An ignored directory is
// measured whole and not descended into, which mirrors the scanner and means
// clearing can report ignored content without walking through it.
func (s *Space) measureIgnored(abs string, d os.DirEntry, ignore *Ignore, check *ClearCheck) error {
	rel, err := relativePath(s.root, abs)
	if err != nil || rel == "" {
		return nil //nolint:nilerr // a path we cannot normalize is not ours to touch
	}
	if rel == SpaceDir {
		return filepath.SkipDir
	}

	if d.IsDir() {
		if !ignore.Match(rel, true) {
			return nil
		}
		size, err := dirSize(abs)
		if err != nil {
			return err
		}
		check.IgnoredBytes += size
		check.Ignored = append(check.Ignored, rel)
		return filepath.SkipDir
	}

	if !ignore.Match(rel, false) {
		return nil
	}
	info, err := d.Info()
	if err != nil {
		return err
	}
	check.IgnoredBytes += info.Size()
	check.Ignored = append(check.Ignored, rel)
	return nil
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
