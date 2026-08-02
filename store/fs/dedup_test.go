package fs

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/chunk"
	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// TestIdenticalContentIsStoredOnce is dedup at file level (E2): two paths with
// the same content resolve to one FileID and therefore to one set of chunks.
func TestIdenticalContentIsStoredOnce(t *testing.T) {
	ctx := context.Background()
	objects, dir := openStore(t)

	content := []byte(strings.Repeat("shared asset content ", 200))
	first, err := chunk.BuildFile(ctx, bytes.NewReader(content), smallChunking(),
		func(ctx context.Context, id hash.ChunkID, data []byte) error {
			return objects.Put(ctx, store.ChunkKey(id), data)
		})
	if err != nil {
		t.Fatalf("BuildFile: %v", err)
	}
	second, err := chunk.BuildFile(ctx, bytes.NewReader(content), smallChunking(), nil)
	if err != nil {
		t.Fatalf("BuildFile: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("identical content produced %s and %s", first.ID, second.ID)
	}

	// The store must hold exactly the distinct chunks, no more. Note that the
	// count is below the number of chunk references even for a single file:
	// this content is repetitive enough that several of its chunks come out
	// byte-identical and deduplicate against each other. Global dedup is not
	// only a property across files (E2).
	distinct := make(map[hash.ChunkID]struct{}, len(first.File.Chunks))
	for _, c := range first.File.Chunks {
		distinct[c.ID] = struct{}{}
	}

	stored := countFiles(t, filepath.Join(dir, objectsDir, "chunk"))
	if stored != len(distinct) {
		t.Errorf("%d chunks on disk for %d distinct chunks (%d references), want exactly the distinct ones",
			stored, len(distinct), len(first.File.Chunks))
	}
	if len(distinct) == len(first.File.Chunks) {
		t.Logf("note: no intra-file dedup in this fixture (%d chunks)", len(first.File.Chunks))
	}
}

// TestVerifyFileContentDetectsASwappedChunk covers the gap the Get path
// cannot close: a file object whose chunk list was replaced with well-formed
// but wrong chunks passes every per-object check and is only caught by
// reassembling the content.
func TestVerifyFileContentDetectsASwappedChunk(t *testing.T) {
	ctx := context.Background()
	objects, _ := openStore(t)

	content := []byte(strings.Repeat("original ", 500))
	result, err := chunk.BuildFile(ctx, bytes.NewReader(content), smallChunking(),
		func(ctx context.Context, id hash.ChunkID, data []byte) error {
			return objects.Put(ctx, store.ChunkKey(id), data)
		})
	if err != nil {
		t.Fatalf("BuildFile: %v", err)
	}

	// Store an unrelated chunk and point the file object at it instead.
	decoy := []byte(strings.Repeat("decoy ", 200))
	decoyID := hash.Chunk(decoy)
	if err := objects.Put(ctx, store.ChunkKey(decoyID), decoy); err != nil {
		t.Fatalf("Put decoy: %v", err)
	}

	tampered := result.File
	tampered.Chunks = append([]object.ChunkRef(nil), result.File.Chunks...)
	tampered.Chunks[0] = object.ChunkRef{ID: decoyID, Length: int64(len(decoy))}
	tampered.Size = 0
	for _, c := range tampered.Chunks {
		tampered.Size += c.Length
	}

	// The file object itself is self-consistent, so Verify accepts it.
	tamperedBytes, err := tampered.Marshal()
	if err != nil {
		t.Fatalf("marshal tampered file object: %v", err)
	}
	if err := store.Verify(store.FileKey(result.ID), tamperedBytes); err != nil {
		t.Fatalf("the per-object check was expected to accept this: %v", err)
	}

	// Reassembling catches it.
	if err := store.VerifyFileContent(ctx, objects, result.ID, tampered); !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("err = %v, want errs.ErrCorruptObject", err)
	}
}
