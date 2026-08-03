package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// File modes used to mark a file as reserved by somebody else.
//
// os.Chmod is the portable spelling: on Unix it sets the permission bits, and
// on Windows the Go runtime maps write permission to the read-only attribute,
// which is precisely the flag a DCC tool checks before saving. One call covers
// both platforms.
const (
	readOnlyPerm  = 0o444
	writablePerm  = 0o644
	preserveOwner = os.FileMode(0)
)

// ApplyLockAttributes marks files held by somebody else read-only and leaves
// everything else writable (E49).
//
// This is a reminder, not a boundary. Anyone can clear the attribute, and a
// tool that saves by deleting and recreating the file drops it without meaning
// to - which is exactly why the commit check exists and why this must never be
// the thing relied upon.
//
// A project with locking switched off is left entirely alone: making files
// writable that Fibula never marked would be reaching into a working directory
// for no reason.
func (s *Space) ApplyLockAttributes(ctx context.Context, ignore *Ignore, owner string, now time.Time) (int, error) {
	settings, err := s.Settings(ctx)
	if err != nil || !settings.Locking {
		return 0, err
	}

	foreign, err := s.LockedBySomeoneElse(ctx, owner, now)
	if err != nil {
		return 0, err
	}
	reserved := make(map[string]struct{}, len(foreign))
	for _, lock := range foreign {
		reserved[lock.Path] = struct{}{}
	}

	files, err := s.Scan(ctx, ignore)
	if err != nil {
		return 0, err
	}

	marked := 0
	for _, f := range files {
		_, held := reserved[f.Path]
		changed, err := s.setReadOnly(f.Path, held)
		if err != nil {
			return marked, err
		}
		if held && changed {
			marked++
		}
	}
	return marked, nil
}

// setReadOnly sets or clears the attribute, reporting whether it had to change
// anything. It is a no-op when the file is already in the wanted state, so a
// scan over an unchanged tree does no writes at all.
func (s *Space) setReadOnly(rel string, readOnly bool) (bool, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))

	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", rel, err)
	}

	writable := info.Mode().Perm()&0o200 != 0
	if writable != readOnly {
		return false, nil // already as it should be
	}

	mode := os.FileMode(writablePerm)
	if readOnly {
		mode = readOnlyPerm
	}
	if err := os.Chmod(abs, mode); err != nil {
		return false, fmt.Errorf("mark %s: %w", rel, err)
	}
	return true, nil
}

// makeWritable clears a read-only attribute so the file can be removed.
//
// On Windows a read-only file cannot be deleted at all, and space clear runs
// through the dirty check - the one path where a failure to delete would leave
// the user believing the space was cleared when it was not (E17).
func (s *Space) makeWritable(rel string) error {
	_, err := s.setReadOnly(rel, false)
	return err
}
