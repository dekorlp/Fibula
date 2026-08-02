package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
)

func openStore(t *testing.T) (store.ObjectStore, string) {
	t.Helper()

	dir := t.TempDir()
	s, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s, dir
}

func TestRoundTrip(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	data := []byte("chunk content")
	key := store.ChunkKey(hash.Chunk(data))

	if err := s.Put(ctx, key, data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("Get returned %q, want %q", got, data)
	}
}

// TestPutIsIdempotent pins E29: writing the same hash twice is not an error.
// Content addressing means every writer of the same content writes the same
// bytes, so a second write is a no-op and not a conflict.
func TestPutIsIdempotent(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	data := []byte("written twice")
	key := store.ChunkKey(hash.Chunk(data))

	for i := range 3 {
		if err := s.Put(ctx, key, data); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("Get returned %q, want %q", got, data)
	}
}

// TestConcurrentPutsOfTheSameHash covers the case two clients uploading the
// same asset produce. Under -race this also catches any shared mutable state
// in the store.
func TestConcurrentPutsOfTheSameHash(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	data := []byte(strings.Repeat("concurrent content ", 1000))
	key := store.ChunkKey(hash.Chunk(data))

	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Put(ctx, key, data); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent Put: %v", err)
	}

	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content was corrupted by concurrent writes")
	}
}

// TestTamperedObjectIsDetected is the point of the verification decorator
// (E27). The store always potentially holds objects whose content does not
// match their hash — anyone with a presigned PUT URL can write arbitrary
// bytes — so this is the only defence against a faulty or malicious writer,
// not a precaution against bit rot.
func TestTamperedObjectIsDetected(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	data := []byte("honest content")
	key := store.ChunkKey(hash.Chunk(data))

	if err := s.Put(ctx, key, data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Tamper on disk, behind the store's back, the way corruption or a
	// malicious writer would.
	path := objectPath(t, dir, key)
	if err := os.WriteFile(path, []byte("tampered content"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	got, err := s.Get(ctx, key)
	if !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("err = %v, want errs.ErrCorruptObject", err)
	}
	if got != nil {
		t.Errorf("Get returned %q alongside the error, corrupt content must never reach the caller", got)
	}
}

// TestPutRejectsAMismatchedKey checks the other direction: a caller that
// computes the wrong key is told immediately rather than storing something
// that reads back as corrupt weeks later.
func TestPutRejectsAMismatchedKey(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	wrongKey := store.ChunkKey(hash.Chunk([]byte("something else")))

	if err := s.Put(ctx, wrongKey, []byte("actual content")); !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("err = %v, want errs.ErrCorruptObject", err)
	}
}

// TestNoHalfObjectIsVisible is the crash-mid-upload case. A temporary file
// left behind by a killed process must never be reachable as an object, and
// must not make Exists claim the object is there.
func TestNoHalfObjectIsVisible(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()
	data := []byte("complete content")
	key := store.ChunkKey(hash.Chunk(data))

	// Simulate a process killed between writing the temporary file and the
	// rename that publishes it.
	tmp := filepath.Join(dir, tempDir, "put-interrupted")
	if err := os.WriteFile(tmp, data[:5], 0o644); err != nil {
		t.Fatalf("stage a partial write: %v", err)
	}

	present, err := s.Exists(ctx, []store.Key{key})
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if present[0] {
		t.Error("Exists reports an object that was never committed")
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, errs.ErrObjectNotFound) {
		t.Errorf("Get err = %v, want errs.ErrObjectNotFound", err)
	}
}

func TestExistsIsBatched(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	stored := []byte("stored")
	storedKey := store.ChunkKey(hash.Chunk(stored))
	missingKey := store.ChunkKey(hash.Chunk([]byte("missing")))

	if err := s.Put(ctx, storedKey, stored); err != nil {
		t.Fatalf("Put: %v", err)
	}

	present, err := s.Exists(ctx, []store.Key{missingKey, storedKey, missingKey})
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}

	want := []bool{false, true, false}
	for i := range want {
		if present[i] != want[i] {
			t.Errorf("Exists = %v, want %v", present, want)
			break
		}
	}
}

func TestGetOfAMissingObject(t *testing.T) {
	s, _ := openStore(t)

	key := store.ChunkKey(hash.Chunk([]byte("never stored")))

	if _, err := s.Get(context.Background(), key); !errors.Is(err, errs.ErrObjectNotFound) {
		t.Errorf("err = %v, want errs.ErrObjectNotFound", err)
	}
}

// TestObjectStoreCannotDelete is the done-criterion of F-S2-01, checked by the
// compiler rather than at runtime: the interface returned by Open has no
// Delete method, so a client that tries to delete does not compile. The
// assertion below states that in a form a reader can see.
func TestObjectStoreCannotDelete(t *testing.T) {
	s, _ := openStore(t)

	if _, isGC := s.(store.GCStore); isGC {
		t.Error("Open handed out a store that can delete; deletion belongs to OpenGC alone (E25)")
	}
}

func TestGCStoreDeletes(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir); err != nil {
		t.Fatalf("Create: %v", err)
	}
	gc, err := OpenGC(dir)
	if err != nil {
		t.Fatalf("OpenGC: %v", err)
	}

	ctx := context.Background()
	data := []byte("to be collected")
	key := store.ChunkKey(hash.Chunk(data))

	if err := gc.Put(ctx, key, data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := gc.Delete(ctx, []store.Key{key}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := gc.Get(ctx, key); !errors.Is(err, errs.ErrObjectNotFound) {
		t.Errorf("Get after Delete: err = %v, want errs.ErrObjectNotFound", err)
	}

	// Deleting again is not an error.
	if err := gc.Delete(ctx, []store.Key{key}); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}

// TestLayoutFansOut checks that no single directory collects every object.
func TestLayoutFansOut(t *testing.T) {
	s, dir := openStore(t)
	ctx := context.Background()

	for i := range 50 {
		data := []byte{byte(i), byte(i >> 8), 'x'}
		if err := s.Put(ctx, store.ChunkKey(hash.Chunk(data)), data); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	chunkRoot := filepath.Join(dir, objectsDir, "chunk")
	entries, err := os.ReadDir(chunkRoot)
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if len(entries) < 20 {
		t.Errorf("50 objects landed in %d top-level buckets, want them spread out", len(entries))
	}
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("%q is a file directly under the type directory, want the fan-out", e.Name())
		}
	}
}

func TestOpenRejectsAnEmptyDirectory(t *testing.T) {
	if _, err := Open(""); !errors.Is(err, errs.ErrInvalidStore) {
		t.Errorf("err = %v, want errs.ErrInvalidStore", err)
	}
}

// TestOpenRefusesADirectoryThatIsNotAStore is a safety property, not
// pedantry. If opening created the layout on demand, a store on an unmounted
// network share would come back as an empty but valid store - and "the store
// has none of your data" would be indistinguishable from "the store is gone".
// Those must never be confused before anything is deleted (E17).
func TestOpenRefusesADirectoryThatIsNotAStore(t *testing.T) {
	empty := t.TempDir()

	if _, err := Open(empty); !errors.Is(err, errs.ErrInvalidStore) {
		t.Errorf("Open err = %v, want errs.ErrInvalidStore", err)
	}
	if _, err := OpenGC(empty); !errors.Is(err, errs.ErrInvalidStore) {
		t.Errorf("OpenGC err = %v, want errs.ErrInvalidStore", err)
	}
	if _, err := OpenRefs(empty); !errors.Is(err, errs.ErrInvalidStore) {
		t.Errorf("OpenRefs err = %v, want errs.ErrInvalidStore", err)
	}
}

// TestCreateIsIdempotent: initializing over an existing store is not an error,
// because the layout is the same either way.
func TestCreateIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	for i := range 2 {
		if _, err := Create(dir); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if _, err := Open(dir); err != nil {
		t.Errorf("Open after Create: %v", err)
	}
}

func TestOperationsHonourContextCancellation(t *testing.T) {
	s, _ := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data := []byte("content")
	key := store.ChunkKey(hash.Chunk(data))

	if err := s.Put(ctx, key, data); !errors.Is(err, context.Canceled) {
		t.Errorf("Put err = %v, want context.Canceled", err)
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, context.Canceled) {
		t.Errorf("Get err = %v, want context.Canceled", err)
	}
	if _, err := s.Exists(ctx, []store.Key{key}); !errors.Is(err, context.Canceled) {
		t.Errorf("Exists err = %v, want context.Canceled", err)
	}
}

// objectPath rebuilds the on-disk path of a key, so that a test can reach
// behind the store the way corruption would.
func objectPath(t *testing.T, dir string, key store.Key) string {
	t.Helper()

	digest := key.Digest()
	parts := []string{dir, objectsDir, string(key.Type())}
	for i := range fanOutDepth {
		parts = append(parts, digest[i*fanOut:(i+1)*fanOut])
	}
	return filepath.Join(append(parts, digest[fanOut*fanOutDepth:])...)
}
