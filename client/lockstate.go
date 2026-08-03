package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

// heldFile lists the locks this space believes it holds.
//
// It exists so that losing one is noticed by the tool rather than reported by
// a person who might forget (E51). The pattern is the same as Head.Base against
// the ref: keep what we believe locally, compare it against the store on
// contact, and report the difference.
const heldFile = "held"

// LostLock is a lock this space believed it held and no longer does.
type LostLock struct {
	Path string

	// Holder is who has it now, empty if the path is simply free again.
	Holder string

	// BrokenFrom is set when the store still records that it was taken over,
	// which is the case until the new holder releases it.
	BrokenFrom string
}

// LostLocks compares what this space believes it holds against the store.
//
// A lock can disappear because it expired and someone took it over, because
// someone forced it, or because it was released elsewhere. All three are worth
// telling the holder about, and none of them depend on that person being told
// by a colleague.
func (s *Space) LostLocks(ctx context.Context, owner string) ([]LostLock, error) {
	believed, err := s.heldLocks()
	if err != nil || len(believed) == 0 {
		return nil, err
	}

	var lost []LostLock
	for _, path := range believed {
		held, err := s.locks.Get(ctx, path)
		switch {
		case errors.Is(err, errs.ErrLockNotFound):
			lost = append(lost, LostLock{Path: path})
		case err != nil:
			return nil, err
		case held.Owner != owner:
			lost = append(lost, LostLock{
				Path: path, Holder: held.Owner, BrokenFrom: held.BrokenFrom,
			})
		}
	}
	return lost, nil
}

// ForgetLostLocks drops the given paths from what this space believes it holds,
// so the same loss is not reported forever.
func (s *Space) ForgetLostLocks(lost []LostLock) error {
	if len(lost) == 0 {
		return nil
	}

	believed, err := s.heldLocks()
	if err != nil {
		return err
	}

	drop := make(map[string]struct{}, len(lost))
	for _, l := range lost {
		drop[l.Path] = struct{}{}
	}

	kept := believed[:0]
	for _, path := range believed {
		if _, gone := drop[path]; !gone {
			kept = append(kept, path)
		}
	}
	return s.writeHeldLocks(kept)
}

// heldLocks reads what this space believes it holds.
func (s *Space) heldLocks() ([]string, error) {
	data, err := os.ReadFile(filepath.Join(s.stateDir(), heldFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read held locks: %w", err)
	}

	var paths []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

func (s *Space) writeHeldLocks(paths []string) error {
	sort.Strings(paths)

	var body strings.Builder
	for _, p := range paths {
		body.WriteString(p)
		body.WriteString("\n")
	}
	return writeFileAtomic(filepath.Join(s.stateDir(), heldFile), []byte(body.String()))
}

// rememberLocks adds paths to what this space believes it holds.
func (s *Space) rememberLocks(taken []store.Lock) error {
	believed, err := s.heldLocks()
	if err != nil {
		return err
	}

	known := make(map[string]struct{}, len(believed))
	for _, p := range believed {
		known[p] = struct{}{}
	}
	for _, lock := range taken {
		if _, seen := known[lock.Path]; !seen {
			believed = append(believed, lock.Path)
			known[lock.Path] = struct{}{}
		}
	}
	return s.writeHeldLocks(believed)
}

// forgetLocks removes paths this space deliberately gave up.
func (s *Space) forgetLocks(paths []string) error {
	lost := make([]LostLock, len(paths))
	for i, p := range paths {
		lost[i] = LostLock{Path: p}
	}
	return s.ForgetLostLocks(lost)
}
