package object

import (
	"fmt"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
)

// ChunkRef is one entry of a file's chunk list: the chunk's ID and its length.
//
// The length costs about eight bytes per chunk and buys two things (E35):
// seeking into a file without loading every preceding chunk, and preallocating
// the target file on restore, which is a real advantage against fragmentation
// for multi-GB assets. Retrofitting it would be a format break.
type ChunkRef struct {
	ID     hash.ChunkID
	Length int64
}

// File is the object between chunk and manifest (E2). It exists so that a
// version does not have to repeat the full chunk list of every unchanged file:
// for 500 assets of 200 MB at 2 MB chunks that is 500 file IDs instead of
// 50,000 chunk hashes.
//
// The File carries no ID of its own, and that is not an oversight. The FileID
// is the hash of the file *content*, not of this object (E3), which is why the
// chunking parameters are an efficiency question rather than a format
// constraint: two clients with different parameters produce the same FileID
// with different chunk lists.
type File struct {
	// Size is the length of the file content in bytes. It must equal the sum
	// of the chunk lengths.
	Size int64

	// Chunks is the content in order. The order is meaningful and is
	// therefore never sorted. An empty file has no chunks at all.
	Chunks []ChunkRef
}

// Marshal renders the file object in its canonical form (E33, E35).
func (f File) Marshal() ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}

	e := newEncoder(format.HeaderFile)
	e.line("size", formatInt(f.Size))
	for _, c := range f.Chunks {
		e.line(c.ID.String(), formatInt(c.Length))
	}
	return e.bytes(), nil
}

// UnmarshalFile parses a canonical file object and rejects anything else.
func UnmarshalFile(data []byte) (File, error) {
	d, err := newDecoder(data, format.HeaderFile)
	if err != nil {
		return File{}, err
	}

	sizeField, err := d.keyed("size", 1)
	if err != nil {
		return File{}, err
	}
	size, err := parseInt(sizeField[0])
	if err != nil {
		return File{}, fmt.Errorf("file size: %w", err)
	}

	f := File{Size: size}
	for !d.done() {
		fields, err := d.next(2)
		if err != nil {
			return File{}, err
		}
		ref, err := parseChunkRef(fields)
		if err != nil {
			return File{}, err
		}
		f.Chunks = append(f.Chunks, ref)
	}

	return f, f.validate()
}

func parseChunkRef(fields []string) (ChunkRef, error) {
	id, err := hash.ParseChunkID(fields[0])
	if err != nil {
		return ChunkRef{}, fmt.Errorf("chunk id: %w", err)
	}
	length, err := parseInt(fields[1])
	if err != nil {
		return ChunkRef{}, fmt.Errorf("chunk length: %w", err)
	}
	return ChunkRef{ID: id, Length: length}, nil
}

// validate enforces the invariants that make a file object usable at all. The
// size is deliberate redundancy against the chunk list, so the two disagreeing
// is corruption rather than a case to reconcile (E7).
func (f File) validate() error {
	if f.Size < 0 {
		return fmt.Errorf("%w: file size %d is negative", errs.ErrInconsistentObject, f.Size)
	}

	var total int64
	for i, c := range f.Chunks {
		if c.Length <= 0 {
			return fmt.Errorf("%w: chunk %d has length %d, want a positive length",
				errs.ErrInconsistentObject, i, c.Length)
		}
		if c.ID.IsZero() {
			return fmt.Errorf("%w: chunk %d has no id", errs.ErrInconsistentObject, i)
		}
		total += c.Length
	}

	if total != f.Size {
		return fmt.Errorf("%w: chunk lengths add up to %d, file size is %d",
			errs.ErrInconsistentObject, total, f.Size)
	}
	return nil
}
