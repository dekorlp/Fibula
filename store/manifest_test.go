package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// serializedManifest builds manifest bytes of a given entry count without
// importing the manifest package, which would be a cycle. The shape only has
// to be sorted and line based, which is what E6's argument rests on.
func serializedManifest(t *testing.T, entries int, changed int) []byte {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("fibula-manifest v1\n")
	for i := range entries {
		content := fmt.Sprintf("asset-%d", i)
		if i == changed {
			content = "asset-changed"
		}
		fmt.Fprintf(&buf, "assets/pack_%03d/asset_%05d.png %s %d\n",
			i/100, i, hash.File([]byte(content)), i+1)
	}
	return buf.Bytes()
}

func TestManifestRoundTrip(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		entries int
	}{
		{"empty", 0},
		{"one entry", 1},
		{"below one chunk", 100},
		{"many chunks", 50_000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newMemStore()
			data := serializedManifest(t, tc.entries, -1)

			id, err := PutManifest(ctx, s, data)
			if err != nil {
				t.Fatalf("PutManifest: %v", err)
			}
			if id != hash.Manifest(data) {
				t.Errorf("PutManifest returned %s, want the hash of the content %s", id, hash.Manifest(data))
			}

			got, err := GetManifest(ctx, s, id)
			if err != nil {
				t.Fatalf("GetManifest: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("round trip changed the manifest: %d bytes in, %d out", len(data), len(got))
			}
		})
	}
}

// TestOneChangedLineCostsOneChunkInTheStore is E6's whole justification,
// measured where it is actually paid: the store. manifest/chunking_test.go
// proves the property at the chunker; this proves the client's writer uses it.
//
// Before manifests were chunked, the second snapshot of a 50,000-asset project
// wrote a second ~7 MB object. The point of E6 is that it writes ~64 KB.
func TestOneChangedLineCostsOneChunkInTheStore(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	before := serializedManifest(t, 50_000, -1)
	if _, err := PutManifest(ctx, s, before); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	baseline := s.bytesStored()

	after := serializedManifest(t, 50_000, 24_000)
	if _, err := PutManifest(ctx, s, after); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	growth := s.bytesStored() - baseline

	// The manifest object itself is rewritten in full — it is a list of every
	// chunk ID — so the budget is one manifest chunk plus one manifest object,
	// not one chunk alone.
	manifestObject := int64(len(before) / int(64<<10) * 70)
	budget := int64(2*128<<10) + manifestObject
	if growth > budget {
		t.Errorf("a single changed line grew the store by %d bytes, want at most %d "+
			"(manifest is %d bytes; E6 requires the change to be local)",
			growth, budget, len(before))
	}
	if growth == 0 {
		t.Error("a changed manifest stored nothing new, which cannot be right")
	}
}

func TestGetManifestRejectsATamperedChunk(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	data := serializedManifest(t, 5_000, -1)
	id, err := PutManifest(ctx, s, data)
	if err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	encoded, err := s.Get(ctx, ManifestKey(id))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	file, err := object.UnmarshalFile(encoded)
	if err != nil {
		t.Fatalf("UnmarshalFile: %v", err)
	}
	if len(file.Chunks) < 2 {
		t.Fatalf("the fixture produced %d chunks, want several", len(file.Chunks))
	}

	// Replace a chunk's content with same-length garbage, keeping the key. A
	// store that verified nothing would hand this back as a valid manifest.
	victim := ChunkKey(file.Chunks[0].ID)
	s.overwrite(victim, bytes.Repeat([]byte("x"), int(file.Chunks[0].Length)))

	if _, err := GetManifest(ctx, s, id); !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("err = %v, want errs.ErrCorruptObject", err)
	}
}

func TestManifestChunkKeysListsTheObjectAndItsChunks(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	data := serializedManifest(t, 5_000, -1)
	id, err := PutManifest(ctx, s, data)
	if err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	encoded, err := s.Get(ctx, ManifestKey(id))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	keys, err := ManifestChunkKeys(encoded, id)
	if err != nil {
		t.Fatalf("ManifestChunkKeys: %v", err)
	}
	if keys[0] != ManifestKey(id) {
		t.Errorf("keys[0] = %s, want the manifest object %s", keys[0], ManifestKey(id))
	}

	// Every key must resolve, and together they must be all of them: a key
	// missing here is a chunk garbage collection would delete.
	present, err := s.Exists(ctx, keys)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	for i, ok := range present {
		if !ok {
			t.Errorf("key %s is not in the store", keys[i])
		}
	}
	if len(keys) != s.count() {
		t.Errorf("ManifestChunkKeys listed %d keys, the store holds %d objects", len(keys), s.count())
	}
}
