package store

import (
	"bytes"
	"context"
	"fmt"

	"github.com/dekorlp/fibula/chunk"
	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// PutManifest stores a serialized manifest the way E6 requires: chunked, "as an
// ordinary file object, but with a smaller target size".
//
// The reason is transfer cost rather than storage. A manifest is
// content-addressed, so one changed line changes the whole object; on a project
// of 50,000 assets that is a 7 MB object rewritten by every auto snapshot. The
// manifest is sorted and line based, so at a ~64 KB target a changed line hits
// exactly one chunk and the snapshot costs 64 KB instead of 7 MB. Storing it
// whole — which is what the client did before this — makes every snapshot pay
// the full manifest again, and auto snapshots are supposed to be frequent.
//
// What lands in the store under ManifestKey is therefore a file object listing
// the manifest's chunks, not the manifest bytes. The ManifestID is unchanged:
// it stays the hash of the manifest *content*, exactly as a FileID is the hash
// of file content (E3), so the identity of a manifest does not depend on how it
// was cut.
func PutManifest(ctx context.Context, s ObjectStore, data []byte) (hash.ManifestID, error) {
	id := hash.Manifest(data)

	file, err := splitManifest(ctx, s, data)
	if err != nil {
		return hash.ManifestID{}, err
	}

	encoded, err := file.Marshal()
	if err != nil {
		return hash.ManifestID{}, fmt.Errorf("encode manifest object %s: %w", id, err)
	}
	if err := s.Put(ctx, ManifestKey(id), encoded); err != nil {
		return hash.ManifestID{}, fmt.Errorf("store manifest %s: %w", id, err)
	}
	return id, nil
}

// splitManifest cuts the manifest and writes the chunks, returning the file
// object that lists them.
//
// It goes through chunk.BuildFile rather than driving the splitter itself, so
// that the manifest takes the same tested path as an asset. The FileID
// BuildFile computes is discarded: it is derived with the file context, while a
// manifest is identified by hash.Manifest over the same bytes (E9).
func splitManifest(ctx context.Context, s ObjectStore, data []byte) (object.File, error) {
	sink := func(ctx context.Context, id hash.ChunkID, c []byte) error {
		return s.Put(ctx, ChunkKey(id), c)
	}

	result, err := chunk.BuildFile(ctx, bytes.NewReader(data), chunk.ManifestParams(), sink)
	if err != nil {
		return object.File{}, fmt.Errorf("split manifest: %w", err)
	}
	return result.File, nil
}

// GetManifest reads a manifest back and returns its bytes.
//
// Unlike an asset, a manifest is always reassembled and hashed against its ID
// here rather than through the separate VerifyFileContent path. VerifyFileContent
// is separate because reassembling a 4 GB asset on every read is unacceptable;
// a manifest is measured in megabytes at worst and its chunks have to be
// fetched anyway, so the complete check costs nothing extra. Manifests are
// therefore the one file-shaped object with no trust gap: Verify's partial
// check on the object bytes (see verify.go) is backed here by the full content
// check.
func GetManifest(ctx context.Context, s ObjectStore, id hash.ManifestID) ([]byte, error) {
	encoded, err := s.Get(ctx, ManifestKey(id))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", id, err)
	}
	file, err := object.UnmarshalFile(encoded)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", id, err)
	}

	data, err := readManifestChunks(ctx, s, id, file)
	if err != nil {
		return nil, err
	}
	if got := hash.Manifest(data); got != id {
		return nil, fmt.Errorf("%w: manifest %s reassembles to %s", errs.ErrCorruptObject, id, got)
	}
	return data, nil
}

func readManifestChunks(ctx context.Context, s ObjectStore, id hash.ManifestID,
	file object.File,
) ([]byte, error) {
	out := make([]byte, 0, file.Size)
	for i, ref := range file.Chunks {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read manifest %s: %w", id, err)
		}

		c, err := s.Get(ctx, ChunkKey(ref.ID))
		if err != nil {
			return nil, fmt.Errorf("read manifest %s, chunk %d: %w", id, i, err)
		}
		if int64(len(c)) != ref.Length {
			return nil, fmt.Errorf("%w: manifest %s chunk %d is %d bytes, the object says %d",
				errs.ErrCorruptObject, id, i, len(c), ref.Length)
		}
		out = append(out, c...)
	}
	return out, nil
}

// ManifestChunkKeys returns the keys a manifest object occupies: the object
// itself and every chunk it lists.
//
// Garbage collection needs this and must not be able to forget it. A manifest
// whose chunks are not marked reachable loses its chunks on the next
// collection, and with them the history they describe — so the enumeration
// lives next to the writer rather than being restated in the collector.
func ManifestChunkKeys(encoded []byte, id hash.ManifestID) ([]Key, error) {
	file, err := object.UnmarshalFile(encoded)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", id, err)
	}

	keys := make([]Key, 0, len(file.Chunks)+1)
	keys = append(keys, ManifestKey(id))
	for _, ref := range file.Chunks {
		keys = append(keys, ChunkKey(ref.ID))
	}
	return keys, nil
}
