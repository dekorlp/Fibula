package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// TestKeysCarryTheirObjectType checks that the same bytes hashed in different
// domains produce different keys, and that the key says which domain it is
// from. Without the type the verifier would not know which derive_key context
// to check against.
func TestKeysCarryTheirObjectType(t *testing.T) {
	data := []byte("the same bytes")

	tests := []struct {
		name string
		key  Key
		want format.ObjectType
	}{
		{"chunk", ChunkKey(hash.Chunk(data)), format.TypeChunk},
		{"file", FileKey(hash.File(data)), format.TypeFile},
		{"manifest", ManifestKey(hash.Manifest(data)), format.TypeManifest},
		{"version", VersionKey(hash.Version(data)), format.TypeVersion},
		{"graph", GraphKey(hash.Graph(data)), format.TypeGraph},
		{"signature", SignatureKey(hash.Signature(data)), format.TypeSignature},
	}

	seen := make(map[string]string, len(tests))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.key.Type() != tc.want {
				t.Errorf("Type = %q, want %q", tc.key.Type(), tc.want)
			}
			if tc.key.IsZero() {
				t.Error("a constructed key reports itself as zero")
			}
			if want := string(tc.want) + "/" + tc.key.Digest(); tc.key.String() != want {
				t.Errorf("String = %q, want %q", tc.key.String(), want)
			}
			if other, dup := seen[tc.key.Digest()]; dup {
				t.Errorf("%s and %s share a digest despite domain separation", other, tc.name)
			}
			seen[tc.key.Digest()] = tc.name
		})
	}
}

func TestZeroKey(t *testing.T) {
	if !(Key{}).IsZero() {
		t.Error("the zero Key does not report itself as zero")
	}
}

// TestVerifyAcceptsHonestObjects walks every type the wrapper can check
// completely.
func TestVerifyAcceptsHonestObjects(t *testing.T) {
	data := []byte("object bytes")

	tests := []struct {
		name string
		key  Key
	}{
		{"chunk", ChunkKey(hash.Chunk(data))},
		{"manifest", ManifestKey(hash.Manifest(data))},
		{"version", VersionKey(hash.Version(data))},
		{"graph", GraphKey(hash.Graph(data))},
		{"signature", SignatureKey(hash.Signature(data))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Verify(tc.key, data); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if err := Verify(tc.key, []byte("different bytes")); !errors.Is(err, errs.ErrCorruptObject) {
				t.Errorf("err = %v, want errs.ErrCorruptObject", err)
			}
		})
	}
}

func TestVerifyRejectsTheZeroKey(t *testing.T) {
	if err := Verify(Key{}, []byte("anything")); !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("err = %v, want errs.ErrCorruptObject", err)
	}
}

// TestVerifyOfAFileObjectIsStructuralOnly documents the one gap in the read
// path, so that the limitation is asserted rather than merely commented: the
// FileID is the hash of the content, not of the object bytes (E3), so Verify
// can only check that the object is a well-formed, self-consistent file
// object.
func TestVerifyOfAFileObjectIsStructuralOnly(t *testing.T) {
	chunkA := hash.Chunk([]byte("a"))
	file := object.File{Size: 10, Chunks: []object.ChunkRef{{ID: chunkA, Length: 10}}}
	data, err := file.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	t.Run("accepts a well-formed object under an unrelated FileID", func(t *testing.T) {
		unrelated := FileKey(hash.File([]byte("entirely different content")))

		if err := Verify(unrelated, data); err != nil {
			t.Errorf("Verify rejected a structurally valid file object: %v", err)
		}
	})

	t.Run("rejects bytes that are not a file object", func(t *testing.T) {
		key := FileKey(hash.File([]byte("whatever")))

		if err := Verify(key, []byte("not an object at all\n")); !errors.Is(err, errs.ErrCorruptObject) {
			t.Errorf("err = %v, want errs.ErrCorruptObject", err)
		}
	})

	t.Run("rejects an inconsistent file object", func(t *testing.T) {
		key := FileKey(hash.File([]byte("whatever")))
		broken := "fibula-file v1\nsize\t999\n" + chunkA.String() + "\t10\n"

		if err := Verify(key, []byte(broken)); !errors.Is(err, errs.ErrCorruptObject) {
			t.Errorf("err = %v, want errs.ErrCorruptObject", err)
		}
	})
}

// memStore is the smallest possible ObjectStore, used to drive
// VerifyFileContent without touching a filesystem.
type memStore struct{ objects map[string][]byte }

func newMemStore() *memStore { return &memStore{objects: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key Key) ([]byte, error) {
	data, ok := m.objects[key.String()]
	if !ok {
		return nil, errs.ErrObjectNotFound
	}
	return data, nil
}

func (m *memStore) Put(_ context.Context, key Key, data []byte) error {
	m.objects[key.String()] = data
	return nil
}

func (m *memStore) Exists(_ context.Context, keys []Key) ([]bool, error) {
	present := make([]bool, len(keys))
	for i, k := range keys {
		_, present[i] = m.objects[k.String()]
	}
	return present, nil
}

func TestVerifyFileContent(t *testing.T) {
	ctx := context.Background()
	content := []byte(strings.Repeat("asset ", 100))
	id := hash.File(content)

	// One chunk holding the whole content keeps the fixture readable; the
	// multi-chunk case is covered by the round trip in store/fs.
	chunkID := hash.Chunk(content)
	file := object.File{Size: int64(len(content)), Chunks: []object.ChunkRef{
		{ID: chunkID, Length: int64(len(content))},
	}}

	t.Run("accepts content that reassembles to the FileID", func(t *testing.T) {
		s := newMemStore()
		if err := s.Put(ctx, ChunkKey(chunkID), content); err != nil {
			t.Fatalf("Put: %v", err)
		}

		if err := VerifyFileContent(ctx, s, id, file); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects a chunk of the wrong length", func(t *testing.T) {
		s := newMemStore()
		if err := s.Put(ctx, ChunkKey(chunkID), content[:10]); err != nil {
			t.Fatalf("Put: %v", err)
		}

		if err := VerifyFileContent(ctx, s, id, file); !errors.Is(err, errs.ErrCorruptObject) {
			t.Errorf("err = %v, want errs.ErrCorruptObject", err)
		}
	})

	t.Run("rejects content that hashes to something else", func(t *testing.T) {
		s := newMemStore()
		other := []byte(strings.Repeat("wrong ", 100))
		if err := s.Put(ctx, ChunkKey(chunkID), other); err != nil {
			t.Fatalf("Put: %v", err)
		}

		if err := VerifyFileContent(ctx, s, id, file); !errors.Is(err, errs.ErrCorruptObject) {
			t.Errorf("err = %v, want errs.ErrCorruptObject", err)
		}
	})

	t.Run("reports a missing chunk", func(t *testing.T) {
		if err := VerifyFileContent(ctx, newMemStore(), id, file); !errors.Is(err, errs.ErrObjectNotFound) {
			t.Errorf("err = %v, want errs.ErrObjectNotFound", err)
		}
	})

	t.Run("honours context cancellation", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if err := VerifyFileContent(cancelled, newMemStore(), id, file); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

// TestVerifiedWrapperChecksBothDirections: the decorator must reject a bad Put
// as well as a bad Get, so that a caller computing the wrong key is told at
// once rather than storing something that reads back as corrupt later.
func TestVerifiedWrapperChecksBothDirections(t *testing.T) {
	ctx := context.Background()
	inner := newMemStore()
	s := Verified(inner)

	data := []byte("honest content")
	key := ChunkKey(hash.Chunk(data))

	if err := s.Put(ctx, key, data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, ChunkKey(hash.Chunk([]byte("other"))), data); !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("Put with a mismatched key: err = %v, want errs.ErrCorruptObject", err)
	}

	// Corrupt the underlying store behind the wrapper's back.
	inner.objects[key.String()] = []byte("tampered")

	got, err := s.Get(ctx, key)
	if !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("Get: err = %v, want errs.ErrCorruptObject", err)
	}
	if got != nil {
		t.Error("Get returned data alongside the corruption error")
	}
}

func TestVerifiedExistsPassesThrough(t *testing.T) {
	ctx := context.Background()
	inner := newMemStore()
	s := Verified(inner)

	data := []byte("stored")
	key := ChunkKey(hash.Chunk(data))
	if err := s.Put(ctx, key, data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	present, err := s.Exists(ctx, []Key{key, ChunkKey(hash.Chunk([]byte("missing")))})
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !present[0] || present[1] {
		t.Errorf("Exists = %v, want [true false]", present)
	}
}

func TestRefNameAccessors(t *testing.T) {
	local, err := LocalRef("feature/rig")
	if err != nil {
		t.Fatalf("LocalRef: %v", err)
	}

	if local.Scope() != ScopeLocal {
		t.Errorf("Scope = %q, want %q", local.Scope(), ScopeLocal)
	}
	if local.Name() != "feature/rig" {
		t.Errorf("Name = %q, want %q", local.Name(), "feature/rig")
	}
	if want := "local/feature/rig"; local.String() != want {
		t.Errorf("String = %q, want %q", local.String(), want)
	}
}

func TestRefNameRejectsAnUnknownScope(t *testing.T) {
	if _, err := newRefName(Scope("elsewhere"), "main"); !errors.Is(err, errs.ErrInvalidRefName) {
		t.Errorf("err = %v, want errs.ErrInvalidRefName", err)
	}
}

func TestRefNameLengthLimit(t *testing.T) {
	if _, err := LocalRef(strings.Repeat("a", maxRefNameLen+1)); !errors.Is(err, errs.ErrInvalidRefName) {
		t.Errorf("err = %v, want errs.ErrInvalidRefName", err)
	}
}
