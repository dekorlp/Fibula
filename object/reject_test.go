package object

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
)

// TestRejectsNonCanonicalFraming covers the rules that hold for every object
// type (E33). Each case is a second spelling of a valid object: accepting any
// of them would mean one state with two IDs.
func TestRejectsNonCanonicalFraming(t *testing.T) {
	valid := string(mustRead(t, "manifest"))

	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"CRLF line endings", strings.ReplaceAll(valid, "\n", "\r\n")},
		{"byte order mark", "\ufeff" + valid},
		{"missing final LF", strings.TrimSuffix(valid, "\n")},
		{"blank line in the middle", strings.Replace(valid, "\n", "\n\n", 1)},
		{"trailing space on a line", strings.Replace(valid, "\n", " \n", 1)},
		{"trailing tab on a line", strings.Replace(valid, "\n", "\t\n", 1)},
		{"wrong framing line", strings.Replace(valid, "fibula-manifest v1", "fibula-manifest v2", 1)},
		{"framing line of another type", strings.Replace(valid, "fibula-manifest v1", "fibula-file v1", 1)},
		{"no framing line", strings.TrimPrefix(valid, "fibula-manifest v1\n")},
		{"invalid UTF-8", valid + "assets/x.png\t\xff\t1\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := UnmarshalManifest([]byte(tc.input)); !errors.Is(err, errs.ErrMalformedObject) {
				t.Errorf("err = %v, want errs.ErrMalformedObject", err)
			}
		})
	}
}

func TestRejectsNonCanonicalManifestContent(t *testing.T) {
	fileID := hash.File([]byte("mesh content")).String()
	other := hash.File([]byte("texture content")).String()

	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{
			name:    "entries out of order",
			body:    "b.png\t" + fileID + "\t1\na.png\t" + other + "\t1\n",
			wantErr: errs.ErrInconsistentObject,
		},
		{
			name:    "duplicate path",
			body:    "a.png\t" + fileID + "\t1\na.png\t" + other + "\t1\n",
			wantErr: errs.ErrInconsistentObject,
		},
		{
			name:    "case collision",
			body:    "Textur.png\t" + fileID + "\t1\ntextur.png\t" + other + "\t1\n",
			wantErr: errs.ErrCaseCollision,
		},
		{
			name:    "size with a leading zero",
			body:    "a.png\t" + fileID + "\t0123\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "negative size",
			body:    "a.png\t" + fileID + "\t-1\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "uppercase hash",
			body:    "a.png\t" + strings.ToUpper(fileID) + "\t1\n",
			wantErr: errs.ErrMalformedID,
		},
		{
			name:    "absolute path",
			body:    "/a.png\t" + fileID + "\t1\n",
			wantErr: errs.ErrInvalidPath,
		},
		{
			name:    "parent segment in the path",
			body:    "../a.png\t" + fileID + "\t1\n",
			wantErr: errs.ErrInvalidPath,
		},
		{
			name:    "decomposed unicode path",
			body:    "gru\u0308n.png\t" + fileID + "\t1\n",
			wantErr: errs.ErrInvalidPath,
		},
		{
			name:    "missing size field",
			body:    "a.png\t" + fileID + "\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "extra field",
			body:    "a.png\t" + fileID + "\t1\textra\n",
			wantErr: errs.ErrMalformedObject,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := "fibula-manifest v1\n" + tc.body

			if _, err := UnmarshalManifest([]byte(input)); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRejectsInconsistentFileObject(t *testing.T) {
	chunkA := hash.Chunk([]byte("chunk a"))

	tests := []struct {
		name string
		file File
	}{
		{
			name: "chunk lengths do not add up to the size",
			file: File{Size: 100, Chunks: []ChunkRef{{ID: chunkA, Length: 99}}},
		},
		{
			name: "size without chunks",
			file: File{Size: 100},
		},
		{
			name: "chunks without size",
			file: File{Chunks: []ChunkRef{{ID: chunkA, Length: 1}}},
		},
		{
			name: "zero-length chunk",
			file: File{Chunks: []ChunkRef{{ID: chunkA, Length: 0}}},
		},
		{
			name: "negative size",
			file: File{Size: -1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.file.Marshal(); !errors.Is(err, errs.ErrInconsistentObject) {
				t.Errorf("Marshal err = %v, want errs.ErrInconsistentObject", err)
			}
		})
	}
}

func TestRejectsNonCanonicalVersion(t *testing.T) {
	manifestID := hash.Manifest([]byte("manifest content")).String()
	parentID := hash.Version([]byte("parent version")).String()
	head := "fibula-version v1\nmanifest\t" + manifestID + "\n"

	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:    "field order swapped",
			input:   head + "time\t2026-08-02T14:20:31Z\nauthor\tdennis\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "parent after author",
			input:   head + "author\tdennis\nparent\t" + parentID + "\ntime\t2026-08-02T14:20:31Z\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "timestamp with a numeric offset",
			input:   head + "author\tdennis\ntime\t2026-08-02T16:20:31+02:00\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "timestamp with a fractional second",
			input:   head + "author\tdennis\ntime\t2026-08-02T14:20:31.5Z\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "empty optional field written out",
			input:   head + "author\tdennis\ntime\t2026-08-02T14:20:31Z\ngraph\t\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "unknown field",
			input:   head + "author\tdennis\ntime\t2026-08-02T14:20:31Z\nmood\tgood\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "empty message block",
			input:   head + "author\tdennis\ntime\t2026-08-02T14:20:31Z\nmessage\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "message starting with a blank line",
			input:   head + "author\tdennis\ntime\t2026-08-02T14:20:31Z\nmessage\n\nreal text\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "message ending with a blank line",
			input:   head + "author\tdennis\ntime\t2026-08-02T14:20:31Z\nmessage\nreal text\n\n",
			wantErr: errs.ErrMalformedObject,
		},
		{
			name:    "duplicate parent",
			input:   "fibula-version v1\nmanifest\t" + manifestID + "\nparent\t" + parentID + "\nparent\t" + parentID + "\nauthor\tdennis\ntime\t2026-08-02T14:20:31Z\n",
			wantErr: errs.ErrInconsistentObject,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := UnmarshalVersion([]byte(tc.input)); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestRejectsAuthorThatWouldBreakTheFraming checks the free-text fields: a tab
// or a newline in an author name would silently turn into extra fields or
// extra lines.
func TestRejectsAuthorThatWouldBreakTheFraming(t *testing.T) {
	tests := []struct {
		name   string
		author string
	}{
		{"empty", ""},
		{"tab", "den\tnis"},
		{"newline", "den\nnis"},
		{"leading space", " dennis"},
		{"trailing space", "dennis "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Version{
				Manifest: hash.Manifest([]byte("m")),
				Author:   tc.author,
				Time:     time.Date(2026, 8, 2, 14, 20, 31, 0, time.UTC),
			}

			if _, err := v.Marshal(); !errors.Is(err, errs.ErrMalformedObject) {
				t.Errorf("Marshal err = %v, want errs.ErrMalformedObject", err)
			}
		})
	}
}

func TestRejectsUnsortedGraph(t *testing.T) {
	tests := []struct {
		name  string
		graph Graph
	}{
		{
			name: "edges out of order",
			graph: Graph{Edges: []Edge{
				{Source: "b.blend", Target: "t.png", Type: "texture"},
				{Source: "a.blend", Target: "t.png", Type: "texture"},
			}},
		},
		{
			name: "duplicate edge",
			graph: Graph{Edges: []Edge{
				{Source: "a.blend", Target: "t.png", Type: "texture"},
				{Source: "a.blend", Target: "t.png", Type: "texture"},
			}},
		},
		{
			name: "extractors out of order",
			graph: Graph{Extractors: []Extractor{
				{Name: "blender", Version: "0.2.0", Scope: ".blend"},
				{Name: "blender", Version: "0.1.0", Scope: ".blend"},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.graph.Marshal(); !errors.Is(err, errs.ErrInconsistentObject) {
				t.Errorf("Marshal err = %v, want errs.ErrInconsistentObject", err)
			}
		})
	}
}

// TestGraphEdgesMustComeAfterExtractors pins the field order of the graph
// object: provenance first, then the edges (E23, E33).
func TestGraphEdgesMustComeAfterExtractors(t *testing.T) {
	input := "fibula-graph v1\n" +
		"edge\ta.blend\tt.png\ttexture\n" +
		"extractor\tblender\t0.1.0\t.blend\n"

	if _, err := UnmarshalGraph([]byte(input)); !errors.Is(err, errs.ErrMalformedObject) {
		t.Errorf("err = %v, want errs.ErrMalformedObject", err)
	}
}

func TestRejectsNonCanonicalSignature(t *testing.T) {
	versionID := hash.Version([]byte("signed version")).String()
	head := "fibula-signature v1\nversion\t" + versionID + "\nkey\ted25519:0f1e2d3c\n"

	tests := []struct {
		name  string
		input string
	}{
		{"uppercase signature hex", head + "signature\tDEADBEEF\n"},
		{"odd-length signature hex", head + "signature\tdeadbee\n"},
		{"non-hex signature", head + "signature\tnothexatall\n"},
		{"missing signature line", head},
		{"trailing line", head + "signature\tdeadbeef\nextra\tline\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := UnmarshalSignature([]byte(tc.input)); !errors.Is(err, errs.ErrMalformedObject) {
				t.Errorf("err = %v, want errs.ErrMalformedObject", err)
			}
		})
	}
}
