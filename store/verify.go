package store

import (
	"context"
	"fmt"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// Verified wraps an ObjectStore so that nothing leaves it unchecked (E27).
//
// The check belongs in one wrapper that every backend passes through, not in
// each implementation. Otherwise verification is a convention that the next
// backend overlooks while being written, and the mistake surfaces months later
// as a corrupted asset.
//
// This is not a precaution against bit rot. Anyone holding a presigned PUT URL
// can write arbitrary bytes under an object key, so the store always
// potentially contains objects whose content does not match their hash — this
// wrapper is the only defence against a faulty or malicious writer (object
// model § 8, trust boundary).
func Verified(inner ObjectStore) ObjectStore { return &verified{inner: inner} }

// VerifiedGC is Verified for the admin path, keeping the Delete capability.
func VerifiedGC(inner GCStore) GCStore { return &verifiedGC{verified{inner: inner}, inner} }

type verified struct{ inner ObjectStore }

type verifiedGC struct {
	verified
	inner GCStore
}

func (v *verifiedGC) Delete(ctx context.Context, keys []Key) error {
	return v.inner.Delete(ctx, keys)
}

// Get fetches and verifies. A mismatch is reported as an error and the bytes
// are discarded — corrupt content never reaches the caller as data.
func (v *verified) Get(ctx context.Context, key Key) ([]byte, error) {
	data, err := v.inner.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := Verify(key, data); err != nil {
		return nil, err
	}
	return data, nil
}

// Put verifies before writing. Checking here as well as on read is cheap — the
// bytes are already in hand — and it turns a caller that computes the wrong
// key into an immediate error instead of an object that reads back as corrupt
// some time later.
func (v *verified) Put(ctx context.Context, key Key, data []byte) error {
	if err := Verify(key, data); err != nil {
		return err
	}
	return v.inner.Put(ctx, key, data)
}

func (v *verified) Exists(ctx context.Context, keys []Key) ([]bool, error) {
	return v.inner.Exists(ctx, keys)
}

// Verify checks object bytes against the key they are stored under.
//
// It covers four of the six object types completely. Two cannot be verified
// this way at all, and that is a consequence of the object model rather than a
// gap here: the FileID is the hash of the file *content*, not of the serialized
// file object (E3), and since E6 stores a manifest "as an ordinary file object"
// the same holds for the ManifestID. Hashing the object bytes would produce
// something unrelated to the key.
//
// What is done instead is stated openly, because CLAUDE.md § 4 requires the
// trust boundary to be documented wherever verification does not happen:
//
//   - The file object is parsed and checked for self-consistency — the chunk
//     lengths must add up to the recorded size, every chunk ID must be
//     present. That catches truncation and garbage, which is most of what goes
//     wrong in practice.
//   - It does **not** prove that the object is the one belonging to the
//     FileID. A writer who deliberately stores a different but well-formed
//     chunk list under a FileID will not be caught here.
//
// The complete check requires reassembling the chunks and hashing the result;
// VerifyFileContent does that, and the paths where it actually matters — the
// dirty check before deleting local data, and an asynchronous scrubber — must
// call it rather than rely on this.
//
// Manifests are the exception that closes its own gap: GetManifest always
// reassembles and hashes, because a manifest is small enough that the complete
// check is affordable on every read. So the partial check below is the whole
// story only for asset file objects.
func Verify(key Key, data []byte) error {
	if key.IsZero() {
		return fmt.Errorf("%w: empty key", errs.ErrCorruptObject)
	}

	if key.kind == format.TypeFile || key.kind == format.TypeManifest {
		return verifyFileObject(key, data)
	}

	got, err := digestFor(key.kind, data)
	if err != nil {
		return err
	}
	if got != key.digest {
		return fmt.Errorf("%w: %s hashes to %s", errs.ErrCorruptObject, key, got)
	}
	return nil
}

func digestFor(kind format.ObjectType, data []byte) (string, error) {
	switch kind {
	case format.TypeChunk:
		return hash.Chunk(data).String(), nil
	case format.TypeVersion:
		return hash.Version(data).String(), nil
	case format.TypeGraph:
		return hash.Graph(data).String(), nil
	case format.TypeSignature:
		return hash.Signature(data).String(), nil
	case format.TypeFile, format.TypeManifest:
		// Handled by verifyFileObject; reaching here would be a bug.
		return "", fmt.Errorf("%w: file objects are not hashed over their bytes", errs.ErrCorruptObject)
	default:
		return "", fmt.Errorf("%w: unknown object type %q", errs.ErrCorruptObject, kind)
	}
}

// verifyFileObject applies the partial check described on Verify.
func verifyFileObject(key Key, data []byte) error {
	if _, err := object.UnmarshalFile(data); err != nil {
		return fmt.Errorf("%w: %s does not parse as a file object: %w", errs.ErrCorruptObject, key, err)
	}
	return nil
}

// VerifyFileContent is the complete check that Verify cannot perform: it reads
// every chunk the file object lists, reassembles the content and compares its
// hash against the FileID.
//
// It is deliberately a separate, explicitly called function rather than part
// of the Get path. Doing this on every read would turn one lookup into as many
// as the file has chunks, which for a 4 GB asset is a thousand round trips —
// on the read path that is unacceptable, and on the paths where the guarantee
// is actually needed it is exactly the price worth paying.
//
// The content is streamed through the hasher rather than assembled in memory,
// so the memory cost is one chunk at a time.
func VerifyFileContent(ctx context.Context, s ObjectStore, id hash.FileID, file object.File) error {
	hasher := hash.NewFileHasher()

	for i, ref := range file.Chunks {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("verify file content: %w", err)
		}

		data, err := s.Get(ctx, ChunkKey(ref.ID))
		if err != nil {
			return fmt.Errorf("verify file %s, chunk %d: %w", id, i, err)
		}
		if int64(len(data)) != ref.Length {
			return fmt.Errorf("%w: file %s chunk %d is %d bytes, the file object says %d",
				errs.ErrCorruptObject, id, i, len(data), ref.Length)
		}
		if _, err := hasher.Write(data); err != nil {
			return fmt.Errorf("verify file content: %w", err)
		}
	}

	if got := hasher.ID(); got != id {
		return fmt.Errorf("%w: file %s reassembles to %s", errs.ErrCorruptObject, id, got)
	}
	return nil
}
