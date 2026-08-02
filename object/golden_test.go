package object

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dekorlp/fibula/hash"
)

// There is deliberately no -update flag in this file. A golden vector for
// object serialization that stops matching is a format break (CLAUDE.md § 6),
// and the one thing that must not happen then is a convenient way to make the
// fixture agree again.

// fixtures builds the sample objects the golden vectors are taken from. The
// IDs are derived from fixed content so that the fixtures are reproducible
// from this file alone.
func fixtures(t *testing.T) map[string]interface{ Marshal() ([]byte, error) } {
	t.Helper()

	chunkA := hash.Chunk([]byte("chunk a"))
	chunkB := hash.Chunk([]byte("chunk b"))
	fileMesh := hash.File([]byte("mesh content"))
	fileTexture := hash.File([]byte("texture content"))
	manifestID := hash.Manifest([]byte("manifest content"))
	parentID := hash.Version([]byte("parent version"))
	graphID := hash.Graph([]byte("graph content"))
	versionID := hash.Version([]byte("signed version"))

	stamp := time.Date(2026, 8, 2, 14, 20, 31, 0, time.UTC)

	return map[string]interface{ Marshal() ([]byte, error) }{
		"file": File{
			Size: 3145728,
			Chunks: []ChunkRef{
				{ID: chunkA, Length: 2097152},
				{ID: chunkB, Length: 1048576},
			},
		},
		"file-empty": File{},
		"manifest": Manifest{Entries: []Entry{
			{Path: "assets/char/hero_diffuse.png", File: fileTexture, Size: 8388608},
			{Path: "assets/char/hero_mesh.fbx", File: fileMesh, Size: 24117248},
		}},
		"version-deliberate": Version{
			Manifest: manifestID,
			Parents:  []hash.VersionID{parentID},
			Author:   "dennis",
			Time:     stamp,
			Graph:    graphID,
			Message:  "Reworked hero rig, repacked UVs",
		},
		"version-snapshot": Version{
			Manifest: manifestID,
			Parents:  []hash.VersionID{parentID},
			Author:   "dennis",
			Time:     stamp,
			Expiry:   stamp.Add(24 * time.Hour),
		},
		"version-multiline-message": Version{
			Manifest: manifestID,
			Author:   "dennis",
			Time:     stamp,
			Message:  "Reworked hero rig\n\nUVs repacked, normals flipped on the left glove.",
		},
		"graph": Graph{
			Extractors: []Extractor{
				{Name: "blender", Version: "0.1.0", Scope: ".blend"},
			},
			Edges: []Edge{
				{Source: "levels/level_01.blend", Target: "assets/char/hero_diffuse.png", Type: "texture"},
				{Source: "levels/level_01.blend", Target: "assets/char/hero_mesh.fbx", Type: "mesh"},
			},
		},
		"signature": Signature{
			Version:   versionID,
			Key:       "ed25519:0f1e2d3c",
			Signature: []byte{0xde, 0xad, 0xbe, 0xef},
		},
	}
}

// TestGoldenVectors pins the byte-exact serialization of every object type
// (E32, E33). A failure here is a format break: find the cause and fix the
// code, never the fixture.
func TestGoldenVectors(t *testing.T) {
	for name, obj := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			got, err := obj.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			golden := filepath.Join("testdata", name+".golden")
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden vector: %v", err)
			}

			if string(got) != string(want) {
				t.Errorf("serialization changed - this is a format break, not a fixture to update\n got: %q\nwant: %q",
					got, want)
			}
		})
	}
}

// TestGoldenVectorsAreCanonical checks the framing rules on the fixtures
// themselves, so that a fixture cannot drift into a form the parser would
// reject.
func TestGoldenVectorsAreCanonical(t *testing.T) {
	for name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			golden := filepath.Join("testdata", name+".golden")
			data, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden vector: %v", err)
			}

			if len(data) == 0 || data[len(data)-1] != '\n' {
				t.Error("fixture does not end with LF")
			}
			for i, b := range data {
				if b == '\r' {
					t.Fatalf("fixture contains CR at offset %d", i)
				}
			}
			if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
				t.Error("fixture starts with a byte order mark")
			}
		})
	}
}
