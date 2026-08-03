package manifest

import (
	"sort"

	"github.com/dekorlp/fibula/object"
)

// ConflictKind says why a path could not be resolved without asking.
type ConflictKind int

const (
	// BothChanged - both sides edited the same file differently.
	BothChanged ConflictKind = iota
	// ChangedDeleted - we edited it, they deleted it.
	ChangedDeleted
	// DeletedChanged - we deleted it, they edited it.
	DeletedChanged
	// BothAdded - both sides added the same path with different content.
	BothAdded
)

func (k ConflictKind) String() string {
	switch k {
	case BothChanged:
		return "both changed"
	case ChangedDeleted:
		return "changed here, deleted there"
	case DeletedChanged:
		return "deleted here, changed there"
	case BothAdded:
		return "both added"
	default:
		return "unknown"
	}
}

// Conflict is a path the merge left for the user to decide.
//
// Ours and Theirs are nil where that side has no file - which side that is
// follows from Kind. Theirs is what a caller writes to .fibula/conflicts/ so
// the file can actually be opened and compared (E46); where it is nil there is
// nothing to write, because the other side deleted it.
type Conflict struct {
	Path   string
	Kind   ConflictKind
	Ours   *object.Entry
	Theirs *object.Entry
}

// Merge applies the rules of E44 to three manifests and returns the resulting
// state plus everything it could not decide.
//
// The result always keeps the file that still exists: on a conflict our version
// stays in the manifest and theirs is reported, and where one side deleted what
// the other edited, the surviving file wins (E47). That asymmetry is
// deliberate - a file wrongly kept is tidying, a file wrongly dropped is data
// loss, and on an overlooked conflict the deletion would win in silence.
//
// This is pure computation: no store, no clock, no IO (E42). In phase 1 its
// caller is the transfer of uncommitted work onto a newer state rather than a
// merge of two committed lines, because the staleness check makes history
// unable to fork (E45) - the rules are the same either way.
func Merge(base, ours, theirs object.Manifest) (object.Manifest, []Conflict) {
	baseByPath := byPath(base)
	oursByPath := byPath(ours)
	theirsByPath := byPath(theirs)

	var merged []object.Entry
	var conflicts []Conflict

	for _, path := range unionOfPaths(baseByPath, oursByPath, theirsByPath) {
		sides := threeWay{}
		sides.base, sides.inBase = baseByPath[path]
		sides.ours, sides.inOurs = oursByPath[path]
		sides.theirs, sides.inTheirs = theirsByPath[path]

		entry, keep, conflict := sides.resolve(path)
		if keep {
			merged = append(merged, entry)
		}
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		}
	}

	return object.Manifest{Entries: merged}, conflicts
}

// threeWay is one path as it appears in the three manifests.
type threeWay struct {
	base, ours, theirs       object.Entry
	inBase, inOurs, inTheirs bool
}

// resolve decides a single path. It returns the entry to keep (if any) and the
// conflict to report (if any); a path both sides deleted yields neither.
func (t threeWay) resolve(path string) (object.Entry, bool, *Conflict) {
	switch {
	case !t.inOurs && !t.inTheirs:
		// Deleted on both sides, or never present. Nothing to carry over.
		return object.Entry{}, false, nil

	case !t.inBase:
		return t.resolveAdded(path)

	case t.inOurs && t.inTheirs:
		return t.resolveEdited(path)

	case t.inOurs:
		// They deleted it. If we did not touch it either, the deletion stands.
		if t.ours.File == t.base.File {
			return object.Entry{}, false, nil
		}
		return t.ours, true, &Conflict{Path: path, Kind: ChangedDeleted, Ours: &t.ours}

	default:
		// We deleted it. If they did not touch it, the deletion stands.
		if t.theirs.File == t.base.File {
			return object.Entry{}, false, nil
		}
		return t.theirs, true, &Conflict{Path: path, Kind: DeletedChanged, Theirs: &t.theirs}
	}
}

// resolveAdded handles a path that is not in the base: one or both sides
// created it.
func (t threeWay) resolveAdded(path string) (object.Entry, bool, *Conflict) {
	switch {
	case t.inOurs && t.inTheirs:
		if t.ours.File == t.theirs.File {
			// Both added the same content - a hash comparison, not a conflict.
			return t.ours, true, nil
		}
		return t.ours, true, &Conflict{Path: path, Kind: BothAdded, Ours: &t.ours, Theirs: &t.theirs}
	case t.inOurs:
		return t.ours, true, nil
	default:
		return t.theirs, true, nil
	}
}

// resolveEdited handles a path present on all three sides.
func (t threeWay) resolveEdited(path string) (object.Entry, bool, *Conflict) {
	switch {
	case t.ours.File == t.theirs.File:
		// Includes the unchanged case and the one where both made the same
		// edit. Content addressing makes those the same comparison.
		return t.ours, true, nil
	case t.ours.File == t.base.File:
		return t.theirs, true, nil
	case t.theirs.File == t.base.File:
		return t.ours, true, nil
	default:
		return t.ours, true, &Conflict{Path: path, Kind: BothChanged, Ours: &t.ours, Theirs: &t.theirs}
	}
}

// unionOfPaths returns every path in any of the three, sorted bytewise so that
// the result manifest is canonical without a second pass (E8.5).
func unionOfPaths(sides ...map[string]object.Entry) []string {
	seen := make(map[string]struct{})
	for _, side := range sides {
		for path := range side {
			seen[path] = struct{}{}
		}
	}

	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
