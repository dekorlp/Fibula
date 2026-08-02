package format

import (
	"strings"
	"testing"
)

// TestDeriveKeyContextsAreUnique guards the domain separation from E4: if two
// object types shared a context, a chunk and a file object with identical bytes
// would collide - the normal case for a small file that fits into one chunk.
func TestDeriveKeyContextsAreUnique(t *testing.T) {
	contexts := map[string]string{
		"chunk":     ContextChunk,
		"file":      ContextFile,
		"manifest":  ContextManifest,
		"version":   ContextVersion,
		"graph":     ContextGraph,
		"signature": ContextSignature,
	}

	seen := make(map[string]string, len(contexts))
	for kind, ctx := range contexts {
		if other, dup := seen[ctx]; dup {
			t.Errorf("context %q is shared by %q and %q", ctx, other, kind)
		}
		seen[ctx] = kind
	}
}

// TestContextsCarryTypeAndVersion pins the shape "fibula <type> v1" (E4): the
// object type and the format version must both be inside the context, so that
// a future version 2 cannot produce the same ID for the same bytes.
func TestContextsCarryTypeAndVersion(t *testing.T) {
	tests := []struct {
		name    string
		kind    ObjectType
		context string
	}{
		{"chunk", TypeChunk, ContextChunk},
		{"file", TypeFile, ContextFile},
		{"manifest", TypeManifest, ContextManifest},
		{"version", TypeVersion, ContextVersion},
		{"graph", TypeGraph, ContextGraph},
		{"signature", TypeSignature, ContextSignature},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := "fibula " + string(tc.kind) + " v1"
			if tc.context != want {
				t.Errorf("context = %q, want %q", tc.context, want)
			}
		})
	}
}

// TestHeadersCarryTypeAndVersion pins the framing line from E33. Chunks are
// absent on purpose: they are stored as raw content bytes and have no framing.
func TestHeadersCarryTypeAndVersion(t *testing.T) {
	tests := []struct {
		name   string
		kind   ObjectType
		header string
	}{
		{"file", TypeFile, HeaderFile},
		{"manifest", TypeManifest, HeaderManifest},
		{"version", TypeVersion, HeaderVersion},
		{"graph", TypeGraph, HeaderGraph},
		{"signature", TypeSignature, HeaderSignature},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := "fibula-" + string(tc.kind) + " v1"
			if tc.header != want {
				t.Errorf("header = %q, want %q", tc.header, want)
			}
			if strings.ContainsAny(tc.header, "\r\n\t") {
				t.Errorf("header %q contains a control character", tc.header)
			}
		})
	}
}

// TestSerializationRulesAreIndependentOfThePlatform asserts that the canonical
// rules (E33) are the literal values the format demands, not whatever the host
// happens to use.
func TestSerializationRulesAreIndependentOfThePlatform(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"field separator is tab", FieldSeparator, "\t"},
		{"line terminator is LF", LineTerminator, "\n"},
		{"path separator is slash", PathSeparator, "/"},
		{"timestamp is RFC 3339 UTC with second resolution", TimestampLayout, "2006-01-02T15:04:05Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestHashHexLenMatchesHashSize(t *testing.T) {
	if HashSize != 32 {
		t.Errorf("HashSize = %d, want 32 (BLAKE3, 256 bit)", HashSize)
	}
	if HashHexLen != 64 {
		t.Errorf("HashHexLen = %d, want 64", HashHexLen)
	}
}
