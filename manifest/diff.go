package manifest

import (
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// Change is an entry whose content differs between two manifests.
type Change struct {
	Before object.Entry
	After  object.Entry
}

// Path is the path the change happened at; it is the same on both sides.
func (c Change) Path() string { return c.After.Path }

// Rename is one file object that moved from one path to another.
type Rename struct {
	Before object.Entry
	After  object.Entry
}

// Changes is the difference between two manifests. Every slice is ordered by
// path, so that a diff of the same two manifests always reads identically.
type Changes struct {
	Added   []object.Entry
	Removed []object.Entry
	Changed []Change
	Renamed []Rename
}

// IsEmpty reports whether the two manifests describe the same state.
func (c Changes) IsEmpty() bool {
	return len(c.Added)+len(c.Removed)+len(c.Changed)+len(c.Renamed) == 0
}

// Diff compares two manifests.
//
// A rename is detected as the same FileID appearing at a different path, which
// is possible precisely because the file object sits between manifest and
// chunks (E2): moving a file changes only the manifest line and costs zero
// bytes of transfer. Without that indirection a rename would be
// indistinguishable from a delete plus an add.
//
// A file that was copied rather than moved shows up as Added, because its
// original path is still there.
func Diff(before, after object.Manifest) Changes {
	beforeByPath := byPath(before)
	afterByPath := byPath(after)

	var changes Changes
	var removedCandidates, addedCandidates []object.Entry

	for _, entry := range before.Entries {
		other, stillThere := afterByPath[entry.Path]
		switch {
		case !stillThere:
			removedCandidates = append(removedCandidates, entry)
		case other.File != entry.File:
			changes.Changed = append(changes.Changed, Change{Before: entry, After: other})
		}
	}
	for _, entry := range after.Entries {
		if _, wasThere := beforeByPath[entry.Path]; !wasThere {
			addedCandidates = append(addedCandidates, entry)
		}
	}

	changes.Added, changes.Removed, changes.Renamed = pairRenames(removedCandidates, addedCandidates)
	return changes
}

// pairRenames matches added against removed entries by FileID. Both inputs
// arrive in path order and are consumed in that order, so the pairing is
// deterministic even when the same content sits at several paths.
func pairRenames(removed, added []object.Entry) (stillAdded, stillRemoved []object.Entry, renamed []Rename) {
	queues := make(map[hash.FileID][]int, len(removed))
	for i, entry := range removed {
		queues[entry.File] = append(queues[entry.File], i)
	}
	consumed := make([]bool, len(removed))

	for _, entry := range added {
		queue := queues[entry.File]
		if len(queue) == 0 {
			stillAdded = append(stillAdded, entry)
			continue
		}

		queues[entry.File] = queue[1:]
		consumed[queue[0]] = true
		renamed = append(renamed, Rename{Before: removed[queue[0]], After: entry})
	}

	for i, entry := range removed {
		if !consumed[i] {
			stillRemoved = append(stillRemoved, entry)
		}
	}
	return stillAdded, stillRemoved, renamed
}

func byPath(m object.Manifest) map[string]object.Entry {
	index := make(map[string]object.Entry, len(m.Entries))
	for _, entry := range m.Entries {
		index[entry.Path] = entry
	}
	return index
}
