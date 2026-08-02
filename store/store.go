package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
)

// Key identifies one object in a store: what kind it is, and its hash.
//
// The kind travels with the hash rather than being inferred from it. Domain
// separation (E4) already makes the hashes unique across types, so the kind is
// not needed to avoid collisions — it is needed because the verifier has to
// know which derive_key context to check against, and because a store that can
// name its object types is a store whose contents can be walked and reasoned
// about.
//
// The fields are unexported: a Key can only be built from a typed ID, so there
// is no way to accidentally look up a manifest under a chunk's hash.
type Key struct {
	kind   format.ObjectType
	digest string
}

// ChunkKey returns the store key of raw chunk content.
func ChunkKey(id hash.ChunkID) Key { return Key{format.TypeChunk, id.String()} }

// FileKey returns the store key of a file object. Note that the FileID is the
// hash of the file content, not of the serialized object (E3) — see Verified
// for what that means for verification.
func FileKey(id hash.FileID) Key { return Key{format.TypeFile, id.String()} }

// ManifestKey returns the store key of a manifest object (E9).
func ManifestKey(id hash.ManifestID) Key { return Key{format.TypeManifest, id.String()} }

// VersionKey returns the store key of a version object.
func VersionKey(id hash.VersionID) Key { return Key{format.TypeVersion, id.String()} }

// GraphKey returns the store key of a dependency graph object.
func GraphKey(id hash.GraphID) Key { return Key{format.TypeGraph, id.String()} }

// SignatureKey returns the store key of a signature object.
func SignatureKey(id hash.SignatureID) Key { return Key{format.TypeSignature, id.String()} }

// Type reports which kind of object the key names.
func (k Key) Type() format.ObjectType { return k.kind }

// Digest is the object's hash in its canonical rendering (E34).
func (k Key) Digest() string { return k.digest }

// String renders the key as "<type>/<hash>", which is also how a filesystem
// backend lays it out.
func (k Key) String() string { return string(k.kind) + "/" + k.digest }

// IsZero reports whether the key was never initialized.
func (k Key) IsZero() bool { return k.kind == "" || k.digest == "" }

// ObjectStore is the immutable half of the storage abstraction (E24).
//
// There is deliberately no Delete. Deletion lives in GCStore, which only the
// admin path ever receives, so that the no-data-loss invariant is anchored in
// the type system rather than in discipline: a client holding an ObjectStore
// simply cannot delete, and the one place that may delete is thereby also the
// one place that has to implement the reference check (E25).
type ObjectStore interface {
	// Get returns the object's bytes, verified against its key (E26).
	//
	// It returns bytes rather than a stream on purpose: with an io.ReadCloser
	// the hash check could only happen after the last Read, by which point the
	// caller has long since acted on the data. Every object fits in memory
	// because chunks are capped at a few MB; streaming happens one level up,
	// where a large file is a sequence of chunks.
	Get(ctx context.Context, key Key) ([]byte, error)

	// Put writes an object. It is idempotent — writing the same hash twice is
	// not an error — and atomic, so no half object ever becomes visible (E29).
	Put(ctx context.Context, key Key, data []byte) error

	// Exists reports for each key whether the object is present, in the same
	// order as the input.
	//
	// The signature is batch-only by design. An Exists(key) taking a single
	// key would sooner or later end up inside a loop, which is exactly the N+1
	// the coding standards forbid — and the batch existence query is the whole
	// basis of partial sync.
	Exists(ctx context.Context, keys []Key) ([]bool, error)
}

// GCStore is an ObjectStore that can also delete. Only the admin path receives
// one (E25).
//
// Whoever holds this is responsible for the invariant that goes with it: a
// chunk may be deleted only when no reachable manifest demonstrably references
// it. The type does not enforce that — it only makes sure the capability is
// not handed out by accident.
type GCStore interface {
	ObjectStore

	// Delete removes objects. It is idempotent: deleting what is not there is
	// not an error.
	Delete(ctx context.Context, keys []Key) error
}

// RefStore holds the refs — the only mutable structure in the system (E13).
//
// A ref is the only answer to "what is the current state", and a lost ref
// update is a lost working state with no second system that still knows it.
// That is why CompareAndSwap is mandatory rather than convenient: without
// atomic updates, two concurrent pushes silently lose one of the versions.
type RefStore interface {
	// Get returns the version a ref points at. It reports ErrRefNotFound if
	// the ref does not exist.
	Get(ctx context.Context, name RefName) (hash.VersionID, error)

	// CompareAndSwap moves a ref from old to next, atomically. A zero old
	// value means "the ref must not exist yet", which is how a ref is created
	// without a race against another creator.
	//
	// It reports ErrRefConflict when the current value is not old — never
	// silently overwrites. A non-fast-forward is a user decision (E13.3).
	CompareAndSwap(ctx context.Context, name RefName, old, next hash.VersionID) error

	// List returns every ref in the given scope, sorted by name.
	List(ctx context.Context, scope Scope) ([]RefName, error)

	// Delete removes a ref. Unlike objects, refs are mutable state and
	// deleting one loses no content — every version it pointed at stays in
	// the object store.
	Delete(ctx context.Context, name RefName) error
}

// Scope separates local from remote refs (E13.3). Offline work happens on
// local refs and is reconciled during sync; keeping them apart is what makes
// "my state" and "the server's state" distinguishable at all.
type Scope string

const (
	// ScopeLocal holds the refs this working copy advances on its own.
	ScopeLocal Scope = "local"

	// ScopeRemote holds the last known state of a remote, never advanced
	// except by a sync.
	ScopeRemote Scope = "remote"
)

// RefName is a validated ref name within a scope.
type RefName struct {
	scope Scope
	name  string
}

// DefaultRef is the ref a fresh space starts on (E13).
const DefaultRef = "main"

// LocalRef returns a name in the local scope.
func LocalRef(name string) (RefName, error) { return newRefName(ScopeLocal, name) }

// RemoteRef returns a name in the remote scope.
func RemoteRef(name string) (RefName, error) { return newRefName(ScopeRemote, name) }

// Scope reports which namespace the ref lives in.
func (r RefName) Scope() Scope { return r.scope }

// Name is the ref name without its scope.
func (r RefName) Name() string { return r.name }

// String renders the ref as "<scope>/<name>".
func (r RefName) String() string { return string(r.scope) + "/" + r.name }

// newRefName validates a ref name. The rules are deliberately narrow: a ref
// name reaches the filesystem in the fs backend, so anything that could escape
// a directory or collide case-insensitively is rejected here rather than
// defended against in each backend.
func newRefName(scope Scope, name string) (RefName, error) {
	switch {
	case scope != ScopeLocal && scope != ScopeRemote:
		return RefName{}, fmt.Errorf("%w: unknown ref scope %q", errs.ErrInvalidRefName, scope)
	case name == "":
		return RefName{}, fmt.Errorf("%w: empty", errs.ErrInvalidRefName)
	case len(name) > maxRefNameLen:
		return RefName{}, fmt.Errorf("%w: %d characters, at most %d", errs.ErrInvalidRefName, len(name), maxRefNameLen)
	}

	for _, segment := range strings.Split(name, "/") {
		if err := checkRefSegment(name, segment); err != nil {
			return RefName{}, err
		}
	}
	return RefName{scope: scope, name: name}, nil
}

// maxRefNameLen keeps a ref name inside what every filesystem accepts as a
// path component chain without surprises.
const maxRefNameLen = 200

func checkRefSegment(name, segment string) error {
	if segment == "" || segment == "." || segment == ".." {
		return fmt.Errorf("%w: %q has an empty, %q or %q segment", errs.ErrInvalidRefName, name, ".", "..")
	}
	if strings.HasSuffix(segment, ".lock") {
		return fmt.Errorf("%w: %q ends in .lock, which the backend uses internally", errs.ErrInvalidRefName, name)
	}

	for i := 0; i < len(segment); i++ {
		if !isRefChar(segment[i]) {
			return fmt.Errorf("%w: %q contains %q, allowed are a-z A-Z 0-9 - _ . and /",
				errs.ErrInvalidRefName, name, segment[i])
		}
	}
	return nil
}

func isRefChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '_', c == '.':
		return true
	default:
		return false
	}
}
