package chunk

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// Result is what one pass over a file yields.
type Result struct {
	// ID is the FileID: the hash of the file content, not of the chunk list
	// (E3). That is what makes the chunking parameters an efficiency question
	// rather than a format constraint - two clients with different parameters
	// produce the same ID with different chunk lists, which is resolvable.
	// The inverse would not be.
	ID hash.FileID

	// File is the file object: the chunk list plus per-chunk lengths, an
	// attribute of the file rather than its identity.
	File object.File
}

// Sink receives each chunk as it is cut, before the next one is read. It
// exists so that the caller can write chunks to a store in the same pass,
// without the chunk list ever being materialized as content in memory.
//
// The slice passed to a Sink is only valid for the duration of the call.
type Sink func(ctx context.Context, id hash.ChunkID, data []byte) error

// BuildFile chunks r in a single streaming pass and returns the FileID
// together with the file object (F-S1-05).
//
// The FileID falls out of the same pass for free: BLAKE3 runs over exactly the
// bytes the chunker reads anyway (E3), so a 4 GB asset is neither held in
// memory nor read twice. If sink is non-nil it is called for every chunk in
// order, which is how the upload path avoids a second pass of its own.
func BuildFile(ctx context.Context, r io.Reader, p Params, sink Sink) (Result, error) {
	splitter, err := NewSplitter(r, p)
	if err != nil {
		return Result{}, err
	}

	hasher := hash.NewFileHasher()
	var file object.File

	for {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("chunk file: %w", err)
		}

		data, err := splitter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, err
		}

		id := hash.Chunk(data)
		if _, err := hasher.Write(data); err != nil {
			return Result{}, fmt.Errorf("hash file content: %w", err)
		}
		if sink != nil {
			if err := sink(ctx, id, data); err != nil {
				return Result{}, fmt.Errorf("chunk sink: %w", err)
			}
		}

		file.Chunks = append(file.Chunks, object.ChunkRef{ID: id, Length: int64(len(data))})
		file.Size += int64(len(data))
	}

	return Result{ID: hasher.ID(), File: file}, nil
}
