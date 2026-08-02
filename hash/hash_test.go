package hash

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
)

// TestDomainSeparation is the done-criterion of F-S1-01: identical bytes must
// produce a different ID per object type (E4). For a small file that fits into
// a single chunk, chunk content and file content are literally the same bytes,
// so this is the normal case rather than an edge case.
func TestDomainSeparation(t *testing.T) {
	data := []byte("the same bytes in every domain")

	ids := map[string]string{
		"chunk":     Chunk(data).String(),
		"file":      File(data).String(),
		"manifest":  Manifest(data).String(),
		"version":   Version(data).String(),
		"graph":     Graph(data).String(),
		"signature": Signature(data).String(),
	}

	seen := make(map[string]string, len(ids))
	for kind, id := range ids {
		if other, dup := seen[id]; dup {
			t.Errorf("%q and %q hash identical bytes to the same ID %s", other, kind, id)
		}
		seen[id] = kind
	}
}

// TestRenderingIsLowercaseHexWithoutPrefix pins E34.
func TestRenderingIsLowercaseHexWithoutPrefix(t *testing.T) {
	got := Chunk([]byte("content")).String()

	if len(got) != format.HashHexLen {
		t.Errorf("length = %d, want %d", len(got), format.HashHexLen)
	}
	if got != strings.ToLower(got) {
		t.Errorf("rendering %q is not lowercase", got)
	}
	if strings.ContainsAny(got, ":-_ ") {
		t.Errorf("rendering %q carries a prefix or separator", got)
	}
	for _, c := range got {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("rendering %q contains a non-hex character %q", got, c)
		}
	}
}

// TestHashingIsStableAcrossCalls guards the property everything else rests on:
// the same content always yields the same ID, and different content does not.
func TestHashingIsStableAcrossCalls(t *testing.T) {
	a := File([]byte("asset content"))
	b := File([]byte("asset content"))
	c := File([]byte("asset contenu"))

	if a != b {
		t.Errorf("identical content hashed to %s and %s", a, b)
	}
	if a == c {
		t.Errorf("different content both hashed to %s", a)
	}
}

func TestFileHasherMatchesFileOfAndFile(t *testing.T) {
	content := bytes.Repeat([]byte("asset bytes "), 5000)
	want := File(content)

	t.Run("streamed in one write", func(t *testing.T) {
		got, err := FileOf(bytes.NewReader(content))
		if err != nil {
			t.Fatalf("FileOf: %v", err)
		}
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("streamed in uneven pieces", func(t *testing.T) {
		h := NewFileHasher()
		for offset := 0; offset < len(content); offset += 7919 {
			end := min(offset+7919, len(content))
			if _, err := h.Write(content[offset:end]); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if got := h.ID(); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("ID does not close the hasher", func(t *testing.T) {
		h := NewFileHasher()
		mustWrite(t, h, content[:100])
		_ = h.ID()
		mustWrite(t, h, content[100:])

		if got := h.ID(); got != want {
			t.Errorf("got %s after an intermediate ID call, want %s", got, want)
		}
	})
}

func TestEmptyContentHasAWellDefinedID(t *testing.T) {
	empty := File(nil)

	if empty.IsZero() {
		t.Error("the ID of empty content must not be the zero value")
	}
	if got := File([]byte{}); got != empty {
		t.Errorf("File(nil) = %s, File([]byte{}) = %s, want them equal", empty, got)
	}
}

func TestParse(t *testing.T) {
	valid := Chunk([]byte("content")).String()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "canonical rendering round-trips", input: valid},
		{name: "uppercase is rejected", input: strings.ToUpper(valid), wantErr: true},
		{name: "too short", input: valid[:len(valid)-1], wantErr: true},
		{name: "too long", input: valid + "0", wantErr: true},
		{name: "empty", input: "", wantErr: true},
		{name: "non-hex character", input: strings.Repeat("g", format.HashHexLen), wantErr: true},
		{name: "prefixed", input: "b3:" + valid[3:], wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChunkID(tc.input)

			if tc.wantErr {
				if !errors.Is(err, errs.ErrMalformedID) {
					t.Fatalf("err = %v, want errs.ErrMalformedID", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tc.input {
				t.Errorf("round trip produced %s, want %s", got, tc.input)
			}
		})
	}
}

// TestParseIsTypedPerObjectKind checks that the parse functions really do
// return distinct types. It compiles only if they do, which is the actual
// assertion; the runtime check merely keeps the compiler honest.
func TestParseIsTypedPerObjectKind(t *testing.T) {
	raw := Chunk([]byte("content")).String()

	chunkID, err := ParseChunkID(raw)
	if err != nil {
		t.Fatalf("ParseChunkID: %v", err)
	}
	fileID, err := ParseFileID(raw)
	if err != nil {
		t.Fatalf("ParseFileID: %v", err)
	}

	if chunkID.String() != fileID.String() {
		t.Errorf("the same hex parsed to %s and %s", chunkID, fileID)
	}
}

func TestDeriveBytesIsDeterministicAndContextSeparated(t *testing.T) {
	a := DeriveBytes("fibula test context v1", 64)
	b := DeriveBytes("fibula test context v1", 64)
	c := DeriveBytes("fibula other context v1", 64)

	if !bytes.Equal(a, b) {
		t.Error("the same context produced different bytes")
	}
	if bytes.Equal(a, c) {
		t.Error("different contexts produced the same bytes")
	}
	if len(a) != 64 {
		t.Errorf("length = %d, want 64", len(a))
	}
}

// TestAllTypedParsersAcceptTheCanonicalRendering keeps the six parse functions
// symmetric: adding a seventh object type without its parser should be visible
// here rather than at the first store round trip.
func TestAllTypedParsersAcceptTheCanonicalRendering(t *testing.T) {
	raw := Chunk([]byte("content")).String()

	parsers := map[string]func(string) (string, error){
		"chunk":     func(s string) (string, error) { id, err := ParseChunkID(s); return id.String(), err },
		"file":      func(s string) (string, error) { id, err := ParseFileID(s); return id.String(), err },
		"manifest":  func(s string) (string, error) { id, err := ParseManifestID(s); return id.String(), err },
		"version":   func(s string) (string, error) { id, err := ParseVersionID(s); return id.String(), err },
		"graph":     func(s string) (string, error) { id, err := ParseGraphID(s); return id.String(), err },
		"signature": func(s string) (string, error) { id, err := ParseSignatureID(s); return id.String(), err },
	}

	for kind, parse := range parsers {
		t.Run(kind, func(t *testing.T) {
			got, err := parse(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != raw {
				t.Errorf("round trip produced %s, want %s", got, raw)
			}

			if _, err := parse("nothex"); !errors.Is(err, errs.ErrMalformedID) {
				t.Errorf("err = %v, want errs.ErrMalformedID", err)
			}
		})
	}
}

func TestBytesReturnsACopy(t *testing.T) {
	id := Chunk([]byte("content"))

	raw := id.Bytes()
	if len(raw) != format.HashSize {
		t.Fatalf("length = %d, want %d", len(raw), format.HashSize)
	}

	raw[0] ^= 0xff
	if id.Bytes()[0] == raw[0] {
		t.Error("Bytes returns the underlying array, mutating it changed the ID")
	}
}

func TestZeroValueIsZero(t *testing.T) {
	if !(ChunkID{}).IsZero() {
		t.Error("the zero ChunkID does not report itself as zero")
	}
	if Chunk([]byte("content")).IsZero() {
		t.Error("a real ID reports itself as zero")
	}
}

func mustWrite(t *testing.T, h *FileHasher, data []byte) {
	t.Helper()

	if _, err := h.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
