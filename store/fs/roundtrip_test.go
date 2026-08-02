package fs

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dekorlp/fibula/chunk"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/manifest"
	"github.com/dekorlp/fibula/object"
	"github.com/dekorlp/fibula/store"
)

// smallChunking keeps the round trip fast while still producing multi-chunk
// files, so that reassembly is genuinely exercised rather than degenerating to
// one chunk per file.
func smallChunking() chunk.Params {
	return chunk.Params{MinSize: 512, MaxSize: 2048, MaskBits: 9}
}

// sourceTree is the working directory the round trip starts from. The entries
// are chosen for the cases that break naive implementations: an empty file, a
// file spanning many chunks, a nested path, a non-ASCII name, and two files
// with identical content at different paths.
func sourceTree(t *testing.T) map[string][]byte {
	t.Helper()

	r := rand.New(rand.NewSource(7))
	large := make([]byte, 30_000)
	for i := range large {
		large[i] = byte(r.Intn(256))
	}

	return map[string][]byte{
		"assets/char/hero_mesh.fbx":    large,
		"assets/char/hero_diffuse.png": []byte(strings.Repeat("texture bytes ", 300)),
		"assets/env/gr\u00fcn.png":     []byte("a non-ASCII name in NFC"),
		"levels/level_01.blend":        []byte("level data"),
		"docs/notes.txt":               []byte("shared content"),
		"docs/copy_of_notes.txt":       []byte("shared content"),
		"empty.bin":                    {},
	}
}

func writeTree(t *testing.T, root string, tree map[string][]byte) {
	t.Helper()

	for p, content := range tree {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("create %s: %v", p, err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

// TestFullRoundTrip is the done-criterion of F-S2-05: a complete file tree is
// written to a store and restored byte-identically into an empty directory,
// through the ref, the version, the manifest, the file objects and the chunks.
//
// It exercises every object type that phase 1 writes, and it is the first
// point at which the pieces of S1 and S2 have to agree with each other rather
// than only with their own tests.
func TestFullRoundTrip(t *testing.T) {
	ctx := context.Background()
	storeDir := t.TempDir()
	sourceDir := t.TempDir()
	restoreDir := t.TempDir()

	tree := sourceTree(t)
	writeTree(t, sourceDir, tree)

	objects, err := Open(storeDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	refs, err := OpenRefs(storeDir)
	if err != nil {
		t.Fatalf("OpenRefs: %v", err)
	}

	versionID := ingest(ctx, t, objects, refs, sourceDir, tree)
	restore(ctx, t, objects, refs, restoreDir)

	compareTrees(t, sourceDir, restoreDir, tree)

	// The version is in the store as an object too, not only behind the ref.
	if _, err := objects.Get(ctx, store.VersionKey(versionID)); err != nil {
		t.Errorf("version object not readable from the store: %v", err)
	}
}

// ingest walks the source tree, writes every object and publishes the ref.
func ingest(ctx context.Context, t *testing.T, objects store.ObjectStore,
	refs store.RefStore, sourceDir string, tree map[string][]byte,
) hash.VersionID {
	t.Helper()

	var builder manifest.Builder
	for _, p := range sortedPaths(tree) {
		f, err := os.Open(filepath.Join(sourceDir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}

		// The sink writes each chunk as it is cut, so the content is never
		// held in memory twice.
		result, err := chunk.BuildFile(ctx, f, smallChunking(),
			func(ctx context.Context, id hash.ChunkID, data []byte) error {
				return objects.Put(ctx, store.ChunkKey(id), data)
			})
		f.Close() //nolint:errcheck // read-only, already fully consumed
		if err != nil {
			t.Fatalf("chunk %s: %v", p, err)
		}

		fileObject, err := result.File.Marshal()
		if err != nil {
			t.Fatalf("marshal file object for %s: %v", p, err)
		}
		if err := objects.Put(ctx, store.FileKey(result.ID), fileObject); err != nil {
			t.Fatalf("put file object for %s: %v", p, err)
		}
		if err := builder.Add(p, result.ID, result.File.Size); err != nil {
			t.Fatalf("add %s to the manifest: %v", p, err)
		}
	}

	m, err := builder.Build()
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	manifestBytes, err := m.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestID := hash.Manifest(manifestBytes)
	if err := objects.Put(ctx, store.ManifestKey(manifestID), manifestBytes); err != nil {
		t.Fatalf("put manifest: %v", err)
	}

	version := object.Version{
		Manifest: manifestID,
		Author:   "dennis",
		Time:     time.Date(2026, 8, 2, 14, 20, 31, 0, time.UTC),
		Message:  "Round trip through the store",
	}
	versionBytes, err := version.Marshal()
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}
	versionID := hash.Version(versionBytes)
	if err := objects.Put(ctx, store.VersionKey(versionID), versionBytes); err != nil {
		t.Fatalf("put version: %v", err)
	}

	name, err := store.LocalRef(store.DefaultRef)
	if err != nil {
		t.Fatalf("LocalRef: %v", err)
	}
	if err := refs.CompareAndSwap(ctx, name, hash.VersionID{}, versionID); err != nil {
		t.Fatalf("publish ref: %v", err)
	}
	return versionID
}

// restore reads the ref and rebuilds the whole tree from the store.
func restore(ctx context.Context, t *testing.T, objects store.ObjectStore,
	refs store.RefStore, target string,
) {
	t.Helper()

	name, err := store.LocalRef(store.DefaultRef)
	if err != nil {
		t.Fatalf("LocalRef: %v", err)
	}
	versionID, err := refs.Get(ctx, name)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}

	versionBytes, err := objects.Get(ctx, store.VersionKey(versionID))
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	version, err := object.UnmarshalVersion(versionBytes)
	if err != nil {
		t.Fatalf("parse version: %v", err)
	}

	manifestBytes, err := objects.Get(ctx, store.ManifestKey(version.Manifest))
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	m, err := object.UnmarshalManifest(manifestBytes)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	for _, entry := range m.Entries {
		restoreEntry(ctx, t, objects, target, entry)
	}
}

func restoreEntry(ctx context.Context, t *testing.T, objects store.ObjectStore,
	target string, entry object.Entry,
) {
	t.Helper()

	fileBytes, err := objects.Get(ctx, store.FileKey(entry.File))
	if err != nil {
		t.Fatalf("get file object for %s: %v", entry.Path, err)
	}
	file, err := object.UnmarshalFile(fileBytes)
	if err != nil {
		t.Fatalf("parse file object for %s: %v", entry.Path, err)
	}
	if file.Size != entry.Size {
		t.Errorf("%s: manifest says %d bytes, file object says %d", entry.Path, entry.Size, file.Size)
	}

	// The full identity check the Get path cannot do: reassemble and hash.
	if err := store.VerifyFileContent(ctx, objects, entry.File, file); err != nil {
		t.Fatalf("verify %s: %v", entry.Path, err)
	}

	full := filepath.Join(target, filepath.FromSlash(entry.Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", entry.Path, err)
	}

	out, err := os.Create(full)
	if err != nil {
		t.Fatalf("create %s: %v", entry.Path, err)
	}
	defer out.Close() //nolint:errcheck // the content is compared afterwards

	for i, ref := range file.Chunks {
		data, err := objects.Get(ctx, store.ChunkKey(ref.ID))
		if err != nil {
			t.Fatalf("get chunk %d of %s: %v", i, entry.Path, err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatalf("write %s: %v", entry.Path, err)
		}
	}
}

func compareTrees(t *testing.T, sourceDir, restoreDir string, tree map[string][]byte) {
	t.Helper()

	for _, p := range sortedPaths(tree) {
		want, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("read source %s: %v", p, err)
		}
		got, err := os.ReadFile(filepath.Join(restoreDir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("read restored %s: %v", p, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s restored to %d bytes, want %d identical", p, len(got), len(want))
		}
	}

	// Nothing beyond the manifest may appear in the restored tree.
	var restored []string
	err := filepath.WalkDir(restoreDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(restoreDir, p)
		if err != nil {
			return err
		}
		restored = append(restored, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk the restored tree: %v", err)
	}
	if len(restored) != len(tree) {
		t.Errorf("restored %d files, the manifest has %d: %v", len(restored), len(tree), restored)
	}
}

func countFiles(t *testing.T, root string) int {
	t.Helper()

	var n int
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count files under %s: %v", root, err)
	}
	return n
}

func sortedPaths(tree map[string][]byte) []string {
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
