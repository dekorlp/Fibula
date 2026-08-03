package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dekorlp/fibula/store"
)

// TestCollectionKeepsTheChunksOfAChunkedManifest is the data-loss case E6
// introduces. Once the manifest is stored as a file object (E6), its chunks are
// ordinary chunk objects sitting in the store like any other. A reference walk
// that marks only the manifest object leaves them unreferenced, so the sweep
// deletes them — and with them every version that manifest describes, silently
// and permanently.
//
// The fixture needs a manifest larger than one manifest chunk (32 KiB minimum,
// 64 KiB expected), which is why it writes many small files rather than one
// large one: what has to span several chunks here is the file listing, not the
// content.
func TestCollectionKeepsTheChunksOfAChunkedManifest(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)

	const files = 800
	for i := range files {
		writeFile(t, space.Root(),
			fmt.Sprintf("assets/pack_%03d/asset_%05d_with_a_realistic_name.png", i/50, i),
			fmt.Sprintf("content of asset %d", i))
	}
	commit(ctx, t, space, "a project whose manifest does not fit in one chunk")

	head, err := space.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	version, err := space.readVersion(ctx, head.Version)
	if err != nil {
		t.Fatalf("readVersion: %v", err)
	}
	encoded, err := space.objects.Get(ctx, store.ManifestKey(version.Manifest))
	if err != nil {
		t.Fatalf("read manifest object: %v", err)
	}
	keys, err := store.ManifestChunkKeys(encoded, version.Manifest)
	if err != nil {
		t.Fatalf("ManifestChunkKeys: %v", err)
	}
	if len(keys) < 3 {
		t.Fatalf("the manifest occupies %d keys, want the object plus several chunks", len(keys))
	}

	result, err := Collect(ctx, openGC(t, storeDir), space.Refs(), storeDir, noGrace(fixedTime().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("garbage collection deleted %d objects from a store with nothing unreachable", result.Deleted)
	}

	// Every key the manifest occupies must still be there, checked directly
	// rather than inferred from the deletion count.
	present, err := space.objects.Exists(ctx, keys)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	for i, ok := range present {
		if !ok {
			t.Errorf("garbage collection deleted %s, which the reachable manifest needs", keys[i])
		}
	}

	// And the history it describes is still usable end to end.
	if _, err := space.Clear(ctx, &Ignore{}, clearOpts()); err != nil {
		t.Fatalf("Clear after GC: %v", err)
	}
	if _, err := space.Restore(ctx); err != nil {
		t.Fatalf("Restore after GC: %v", err)
	}
	assertContent(t, space, "assets/pack_000/asset_00000_with_a_realistic_name.png", "content of asset 0")
	assertContent(t, space, "assets/pack_015/asset_00799_with_a_realistic_name.png", "content of asset 799")
}
