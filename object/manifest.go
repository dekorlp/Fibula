package object

import (
	"fmt"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/path"
)

// Entry is one line of a manifest: a path, the file object it resolves to and
// the file's size.
//
// The size is deliberate redundancy - it is also in the file object - and buys
// progress display and plausibility checks without a store round trip. On
// divergence the content wins, since the FileID is its hash: a mismatch is
// corruption, not a case to reconcile (E7).
type Entry struct {
	Path string
	File hash.FileID
	Size int64
}

// Manifest is the state of a working tree: a flat, sorted mapping from
// normalized path to file object (E5).
//
// Flat rather than a Merkle tree on purpose. The only real advantage of a
// directory tree would be partial checkout of a subtree, but for assets the
// meaningful subsets follow from the dependency graph rather than from the
// directory structure - and the manifest is itself chunked (E6), which
// recovers the delta benefit without a second object type.
//
// Deliberately absent: mtime, which would destroy determinism because two
// identical trees would yield different manifests; the executable bit, because
// assets are data and not code; and any asset type, which is derivable from
// the extension (E7).
type Manifest struct {
	// Entries are sorted bytewise by path and are unique (E8.5).
	Entries []Entry
}

// Marshal renders the manifest in its canonical form.
func (m Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	e := newEncoder(format.HeaderManifest)
	for _, entry := range m.Entries {
		e.line(entry.Path, entry.File.String(), formatInt(entry.Size))
	}
	return e.bytes(), nil
}

// UnmarshalManifest parses a canonical manifest and rejects anything else -
// in particular entries that are not sorted, which would be a second spelling
// of the same state and therefore a second ManifestID for it.
func UnmarshalManifest(data []byte) (Manifest, error) {
	d, err := newDecoder(data, format.HeaderManifest)
	if err != nil {
		return Manifest{}, err
	}

	var m Manifest
	for !d.done() {
		fields, err := d.next(3)
		if err != nil {
			return Manifest{}, err
		}
		entry, err := parseEntry(fields)
		if err != nil {
			return Manifest{}, err
		}
		m.Entries = append(m.Entries, entry)
	}

	return m, m.Validate()
}

// parseEntry does not validate the path: Validate covers every entry once the
// manifest is assembled, and doing it here as well meant walking every path
// twice - a measurable cost on a 50,000-entry manifest, for a second opinion
// on the same string.
func parseEntry(fields []string) (Entry, error) {
	fileID, err := hash.ParseFileID(fields[1])
	if err != nil {
		return Entry{}, fmt.Errorf("file id for %q: %w", fields[0], err)
	}
	size, err := parseInt(fields[2])
	if err != nil {
		return Entry{}, fmt.Errorf("size of %q: %w", fields[0], err)
	}
	return Entry{Path: fields[0], File: fileID, Size: size}, nil
}

// ID returns the ManifestID, which is BLAKE3 over the canonical serialization
// (E9). It marshals rather than hashing whatever the caller happens to hold,
// so that an ID can never be computed over a non-canonical rendering.
func (m Manifest) ID() (hash.ManifestID, error) {
	data, err := m.Marshal()
	if err != nil {
		return hash.ManifestID{}, err
	}
	return hash.Manifest(data), nil
}

// Validate reports whether the manifest satisfies every invariant the format
// demands: valid, sorted, unique and collision-free paths, no negative sizes,
// no missing file IDs.
//
// It is exported so that a caller can check a manifest without serializing it.
// Marshaling a 50,000-entry manifest allocates several megabytes, and using
// that as a validity check - build it, throw it away, build it again at the
// call site - was exactly what manifest.Builder used to do.
func (m Manifest) Validate() error {
	paths := make([]string, 0, len(m.Entries))

	for i, entry := range m.Entries {
		if err := path.Validate(entry.Path); err != nil {
			return err
		}
		if entry.Size < 0 {
			return fmt.Errorf("%w: %q has size %d", errs.ErrInconsistentObject, entry.Path, entry.Size)
		}
		if entry.File.IsZero() {
			return fmt.Errorf("%w: %q has no file id", errs.ErrInconsistentObject, entry.Path)
		}
		if i > 0 && !path.Less(m.Entries[i-1].Path, entry.Path) {
			return fmt.Errorf("%w: %q follows %q, entries must be sorted and unique",
				errs.ErrInconsistentObject, entry.Path, m.Entries[i-1].Path)
		}
		paths = append(paths, entry.Path)
	}

	return path.CheckCollisions(paths)
}
