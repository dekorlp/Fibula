package hash

import (
	"encoding/hex"
	"fmt"
	"io"

	"github.com/zeebo/blake3"

	"github.com/dekorlp/fibula/format"
)

// digest is the raw 256-bit BLAKE3 output every object ID is built from. It is
// unexported so that the typed IDs below cannot be constructed from arbitrary
// bytes by accident — only through a hash function or an explicit parse.
type digest [format.HashSize]byte

// String renders the digest as lowercase hex without a prefix (E34). The
// format version on line 1 of an object already determines the hash algorithm,
// so a prefix on every hash would be redundancy at a scale of millions.
func (d digest) String() string { return hex.EncodeToString(d[:]) }

// Bytes returns a copy of the raw digest.
func (d digest) Bytes() []byte {
	out := make([]byte, len(d))
	copy(out, d[:])
	return out
}

// IsZero reports whether the digest is the zero value, which no real object
// hashes to and which therefore marks an unset ID.
func (d digest) IsZero() bool { return d == digest{} }

// The six object IDs. They are distinct types on purpose: a ChunkID must not
// be assignable where a FileID is expected, because with domain separation
// (E4) the two are computed from different derive_key contexts and mixing them
// up would silently look up the wrong object.
type (
	// ChunkID identifies raw chunk content in the store.
	ChunkID struct{ digest }
	// FileID identifies a file object; it is the hash of the file content,
	// not of the chunk list (E3).
	FileID struct{ digest }
	// ManifestID identifies a manifest object (E9).
	ManifestID struct{ digest }
	// VersionID identifies a version object (E11).
	VersionID struct{ digest }
	// GraphID identifies a dependency graph object (E21).
	GraphID struct{ digest }
	// SignatureID identifies a signature object (E31).
	SignatureID struct{ digest }
)

// Chunk returns the ID of raw chunk content.
func Chunk(data []byte) ChunkID {
	return ChunkID{sum(format.ContextChunk, data)}
}

// Manifest returns the ID of a serialized manifest object.
func Manifest(data []byte) ManifestID {
	return ManifestID{sum(format.ContextManifest, data)}
}

// Version returns the ID of a serialized version object.
func Version(data []byte) VersionID {
	return VersionID{sum(format.ContextVersion, data)}
}

// Graph returns the ID of a serialized graph object.
func Graph(data []byte) GraphID {
	return GraphID{sum(format.ContextGraph, data)}
}

// Signature returns the ID of a serialized signature object.
func Signature(data []byte) SignatureID {
	return SignatureID{sum(format.ContextSignature, data)}
}

// File returns the ID of file content held in memory. For anything that does
// not comfortably fit in memory use FileHasher, which streams.
func File(data []byte) FileID {
	return FileID{sum(format.ContextFile, data)}
}

// FileHasher computes a FileID from a stream. It exists so that the ID can be
// produced in the same pass as chunking (E3, F-S1-05): BLAKE3 runs over the
// bytes the chunker reads anyway, so the hash falls out for free and a 4 GB
// asset never has to be held in memory or read twice.
type FileHasher struct {
	h *blake3.Hasher
}

// NewFileHasher returns a FileHasher in the file derive_key domain.
func NewFileHasher() *FileHasher {
	return &FileHasher{h: blake3.NewDeriveKey(format.ContextFile)}
}

// Write feeds content to the hasher. It never returns an error, so that it can
// be used as the second half of an io.MultiWriter without an error path that
// cannot fire.
func (f *FileHasher) Write(p []byte) (int, error) { return f.h.Write(p) }

// ID returns the FileID of everything written so far. It does not reset the
// hasher, so writing may continue afterwards.
func (f *FileHasher) ID() FileID {
	var d digest
	f.h.Digest().Read(d[:]) //nolint:errcheck // blake3's XOF read never fails
	return FileID{d}
}

// FileOf reads r to completion and returns the FileID of its content. The
// reader is streamed, never buffered in full.
func FileOf(r io.Reader) (FileID, error) {
	f := NewFileHasher()
	if _, err := io.Copy(f, r); err != nil {
		return FileID{}, fmt.Errorf("hash file content: %w", err)
	}
	return f.ID(), nil
}

// sum hashes data in the derive_key domain of the given context (E4). Without
// that separation a chunk and a file object with identical bytes would get the
// same ID — which for a small file that fits into a single chunk is the normal
// case, not an edge case.
func sum(context string, data []byte) digest {
	h := blake3.NewDeriveKey(context)
	//nolint:errcheck // an in-memory state update; blake3.Hasher.Write never fails
	_, _ = h.Write(data)

	var d digest
	h.Digest().Read(d[:]) //nolint:errcheck // blake3's XOF read never fails
	return d
}
