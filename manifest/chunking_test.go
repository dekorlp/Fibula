package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/dekorlp/fibula/chunk"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// largeManifest builds a manifest of the size E6 argues about: 50,000 assets,
// roughly 7 MB serialized.
func largeManifest(t *testing.T, changedEntry int) object.Manifest {
	t.Helper()

	var b Builder
	for i := range 50_000 {
		content := fmt.Sprintf("asset-%d", i)
		if i == changedEntry {
			content = "asset-changed"
		}
		p := fmt.Sprintf("assets/pack_%03d/asset_%05d.png", i/100, i)
		mustAdd(t, &b, p, hash.File([]byte(content)), int64(i)+1)
	}

	m, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

func chunkIDs(t *testing.T, data []byte) []hash.ChunkID {
	t.Helper()

	s, err := chunk.NewSplitter(bytes.NewReader(data), chunk.ManifestParams())
	if err != nil {
		t.Fatalf("NewSplitter: %v", err)
	}

	var ids []hash.ChunkID
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			return ids
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		ids = append(ids, hash.Chunk(c))
	}
}

// TestOneChangedLineCostsOneChunk is the done-criterion of F-S1-07 and the
// whole justification of E6: the manifest is sorted and line based, so a
// changed line must hit exactly one chunk. Without this the auto snapshots
// would push hundreds of MB of manifests per day on a large project.
func TestOneChangedLineCostsOneChunk(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two 50,000-entry manifests")
	}

	before, err := largeManifest(t, -1).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	after, err := largeManifest(t, 25_000).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if len(before) < 5_000_000 {
		t.Fatalf("the manifest is only %d bytes, the case E6 argues about is around 7 MB", len(before))
	}

	oldChunks := chunkIDs(t, before)
	newChunks := chunkIDs(t, after)

	shared := make(map[hash.ChunkID]int, len(oldChunks))
	for _, id := range oldChunks {
		shared[id]++
	}

	var fresh int
	for _, id := range newChunks {
		if shared[id] > 0 {
			shared[id]--
			continue
		}
		fresh++
	}

	t.Logf("manifest %d bytes in %d chunks, one changed line transfers %d chunk(s)",
		len(before), len(oldChunks), fresh)

	if fresh != 1 {
		t.Errorf("a single changed line produced %d new chunks, want exactly 1", fresh)
	}
}

// TestManifestChunksStayNearTheTarget checks that the manifest parameters
// really do produce the roughly 64 KB E6 promises, rather than the 1 to 4 MB
// of asset content.
func TestManifestChunksStayNearTheTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 50,000-entry manifest")
	}

	data, err := largeManifest(t, -1).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	ids := chunkIDs(t, data)
	average := len(data) / len(ids)

	if average < 32*1024 || average > 128*1024 {
		t.Errorf("average manifest chunk is %d bytes, want it near the 64 KiB target", average)
	}
}
