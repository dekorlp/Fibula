// Package fs implements the Fibula store on a plain directory.
//
// This is not a placeholder for a real backend. "One binary, one directory,
// done" is a first-class deployment (CLAUDE.md § 7): a second disk or a NAS
// share is a fully valid Fibula store, with no server, no S3 and no Postgres
// involved.
//
// Spec: object model E13, E24, E27, E29.
package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/store"
)

// Layout constants. The fan-out exists so that no single directory ends up
// holding a million entries, which several filesystems handle badly and every
// directory listing handles slowly.
const (
	objectsDir = "objects"
	tempDir    = "tmp"

	// fanOut is the number of hex characters per intermediate directory, and
	// fanOutDepth how many such levels there are. Two levels of two characters
	// give 65,536 buckets, created lazily: at ten million objects that is
	// about 150 entries per directory.
	fanOut      = 2
	fanOutDepth = 2

	dirPerm  = 0o755
	filePerm = 0o644
)

// Open returns an object store rooted at dir, with verification wired in.
//
// The verifying wrapper is applied here rather than left to the caller, so
// that there is no way to obtain an unverified store from this package — a
// backend cannot forget the check it is not able to skip (E27, F-S2-02).
func Open(dir string) (store.ObjectStore, error) {
	s, err := open(dir)
	if err != nil {
		return nil, err
	}
	return store.Verified(s), nil
}

// OpenGC returns the same store with the deletion capability, for the admin
// path only (E25). Whoever calls this takes on the reference check that must
// precede any delete.
func OpenGC(dir string) (store.GCStore, error) {
	s, err := open(dir)
	if err != nil {
		return nil, err
	}
	return store.VerifiedGC(s), nil
}

func open(dir string) (*objectStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", errs.ErrInvalidStore)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve store directory: %w", err)
	}
	for _, sub := range []string{objectsDir, tempDir} {
		if err := os.MkdirAll(filepath.Join(abs, sub), dirPerm); err != nil {
			return nil, fmt.Errorf("create store directory: %w", err)
		}
	}
	return &objectStore{root: abs}, nil
}

// objectStore is unexported on purpose: the only way out of this package is
// through Open, which wraps it in the verifier.
type objectStore struct{ root string }

// path returns the on-disk location of a key, "objects/<type>/<ab>/<cd>/<rest>".
func (s *objectStore) path(key store.Key) string {
	digest := key.Digest()

	parts := []string{s.root, objectsDir, string(key.Type())}
	for i := range fanOutDepth {
		parts = append(parts, digest[i*fanOut:(i+1)*fanOut])
	}
	return filepath.Join(append(parts, digest[fanOut*fanOutDepth:])...)
}

func (s *objectStore) Get(ctx context.Context, key store.Key) ([]byte, error) {
	if err := checkKey(ctx, key); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.path(key))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("%w: %s", errs.ErrObjectNotFound, key)
	case err != nil:
		return nil, fmt.Errorf("read object %s: %w", key, err)
	}
	return data, nil
}

// Put writes atomically: a temporary file is written and flushed, then renamed
// into place. A reader therefore sees either nothing or the complete object,
// never a partial one — a crash between write and rename leaves only a stray
// temporary file (E29).
//
// The flush is not optional. Without it a crash shortly after upload can leave
// a zero-length file that the store still reports as present, and the dirty
// check before "space clear" trusts exactly that answer before deleting local
// data. Durability here is part of the no-data-loss invariant, not a nicety.
func (s *objectStore) Put(ctx context.Context, key store.Key, data []byte) error {
	if err := checkKey(ctx, key); err != nil {
		return err
	}

	final := s.path(key)
	if err := os.MkdirAll(filepath.Dir(final), dirPerm); err != nil {
		return fmt.Errorf("create object directory for %s: %w", key, err)
	}

	temp, err := s.writeTemp(key, data)
	if err != nil {
		return err
	}
	//nolint:errcheck // a no-op once the rename succeeded, best effort otherwise
	defer os.Remove(temp)

	if err := os.Rename(temp, final); err != nil {
		// Windows refuses to rename onto an existing file, where POSIX
		// replaces it silently. Both mean the same thing here: somebody
		// already stored this exact content, and Put is idempotent (E29).
		if exists(final) {
			return nil
		}
		return fmt.Errorf("commit object %s: %w", key, err)
	}
	return nil
}

func (s *objectStore) writeTemp(key store.Key, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Join(s.root, tempDir), "put-*")
	if err != nil {
		return "", fmt.Errorf("create temporary file for %s: %w", key, err)
	}
	name := f.Name()

	if err := writeAndSync(f, data); err != nil {
		closeAndRemove(f, name)
		return "", fmt.Errorf("write object %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		remove(name)
		return "", fmt.Errorf("close object %s: %w", key, err)
	}
	if err := os.Chmod(name, filePerm); err != nil {
		remove(name)
		return "", fmt.Errorf("set permissions on %s: %w", key, err)
	}
	return name, nil
}

// closeAndRemove discards a temporary file whose write failed. Both errors are
// deliberately dropped: the operation has already failed and is being reported,
// and a leftover file in tmp/ is never visible as an object.
func closeAndRemove(f *os.File, name string) {
	_ = f.Close() //nolint:errcheck // the write already failed and is being reported
	remove(name)
}

// remove discards a temporary file. A leftover in tmp/ is never visible as an
// object, so failing to delete it is not worth surfacing over the real error.
func remove(name string) { _ = os.Remove(name) } //nolint:errcheck // see above

func writeAndSync(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// Exists answers the batch existence query partial sync is built on. At this
// size a stat per key is the right implementation: no index can beat a stat
// that never leaves the page cache, and an index would be a second source of
// truth to keep consistent.
func (s *objectStore) Exists(ctx context.Context, keys []store.Key) ([]bool, error) {
	present := make([]bool, len(keys))

	for i, key := range keys {
		if err := checkKey(ctx, key); err != nil {
			return nil, err
		}
		present[i] = exists(s.path(key))
	}
	return present, nil
}

// Delete implements store.GCStore. It is reachable only through OpenGC.
func (s *objectStore) Delete(ctx context.Context, keys []store.Key) error {
	for _, key := range keys {
		if err := checkKey(ctx, key); err != nil {
			return err
		}
		if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete object %s: %w", key, err)
		}
	}
	return nil
}

// checkKey rejects a key before it is turned into a path. The digest length is
// checked explicitly rather than assumed: a Key can only be built from a typed
// ID inside package store, but this is the function that concatenates it into
// a filesystem path, and that is not a place to rely on an invariant proven
// elsewhere.
func checkKey(ctx context.Context, key store.Key) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store operation: %w", err)
	}
	if key.IsZero() {
		return fmt.Errorf("%w: empty key", errs.ErrInvalidStore)
	}
	if err := checkDigest(key.Digest()); err != nil {
		return fmt.Errorf("%w: %s: %w", errs.ErrInvalidStore, key.Type(), err)
	}
	return nil
}

func checkDigest(digest string) error {
	if len(digest) != format.HashHexLen {
		return fmt.Errorf("digest has %d characters, want %d", len(digest), format.HashHexLen)
	}
	for i := 0; i < len(digest); i++ {
		c := digest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("digest contains %q, want lowercase hex", c)
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The store satisfies both interfaces; GCStore is only ever handed out
// through OpenGC.
var (
	_ store.ObjectStore = (*objectStore)(nil)
	_ store.GCStore     = (*objectStore)(nil)
)
