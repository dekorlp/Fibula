package manifest

import (
	"fmt"
	"sort"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/path"
)

// Builder assembles a manifest from entries added in any order.
//
// It takes paths and file IDs rather than reading a directory, and that is
// deliberate: the core knows no filesystem layout (CLAUDE.md § Architecture).
// Walking a tree means knowing about symlinks, permissions, ignore rules and
// the host path separator, all of which belong to the client. What belongs
// here is everything that decides what the manifest *is*: normalization,
// sorting, collision detection.
//
// The zero Builder is ready to use.
type Builder struct {
	entries map[string]object.Entry
}

// Add records one file under its path. The path is normalized on the way in
// (E8), so a caller that hands over an NFD-decomposed name from macOS gets the
// same manifest as one on Windows.
//
// Adding the same path twice is an error rather than an overwrite: two entries
// for one path means the caller walked something twice or resolved a link into
// its target, and silently keeping the last one would hide that.
func (b *Builder) Add(p string, file hash.FileID, size int64) error {
	normalized, err := path.Normalize(p)
	if err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("%w: %q has size %d", errs.ErrInconsistentObject, normalized, size)
	}
	if file.IsZero() {
		return fmt.Errorf("%w: %q has no file id", errs.ErrInconsistentObject, normalized)
	}

	if b.entries == nil {
		b.entries = make(map[string]object.Entry)
	}
	if _, exists := b.entries[normalized]; exists {
		return fmt.Errorf("%w: %q added twice", errs.ErrInconsistentObject, normalized)
	}

	b.entries[normalized] = object.Entry{Path: normalized, File: file, Size: size}
	return nil
}

// Len reports how many entries have been added.
func (b *Builder) Len() int { return len(b.entries) }

// Build sorts the entries and returns the manifest. It fails on a case
// collision, which is the point at which E8.4 has to be enforced: at restore
// time Windows and a case-insensitive macOS volume could no longer tell the
// two paths apart, and by then the manifest is content-addressed and shared.
func (b *Builder) Build() (object.Manifest, error) {
	entries := make([]object.Entry, 0, len(b.entries))
	for _, e := range b.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return path.Less(entries[i].Path, entries[j].Path) })

	m := object.Manifest{Entries: entries}
	if _, err := m.Marshal(); err != nil {
		return object.Manifest{}, err
	}
	return m, nil
}
