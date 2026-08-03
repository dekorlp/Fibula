// Package errs holds the sentinel errors of the Fibula core.
//
// They live centrally rather than in the package that returns them
// (CLAUDE.md § 1), so that a caller can classify a failure with errors.Is
// without importing the layer it came from — the CLI wants to know that a
// manifest was malformed, not which parser noticed.
//
// Everything here is a sentinel to be wrapped, never returned bare: the call
// site adds what was wrong via fmt.Errorf with %w, and these values say what
// kind of wrong it was.
package errs

import "errors"

var (
	// ErrMalformedID reports a hash that is not exactly 64 lowercase hex
	// characters (E34).
	ErrMalformedID = errors.New("malformed object id")

	// ErrInvalidPath reports a path that cannot appear in a Fibula object
	// (E8). It is always a hard error and never something that gets
	// repaired: paths are untrusted input.
	ErrInvalidPath = errors.New("invalid path")

	// ErrCaseCollision reports two paths in one manifest that differ only in
	// case (E8.4) - an error at creation time, because at restore time
	// Windows and macOS could no longer tell them apart.
	ErrCaseCollision = errors.New("case collision")

	// ErrMalformedObject reports serialized object bytes that do not meet the
	// canonical rules (E32, E33). Non-canonical input is rejected rather than
	// accepted leniently: the object's ID was computed over the bytes as they
	// stand, so accepting a second spelling would mean two IDs for one state.
	ErrMalformedObject = errors.New("malformed object")

	// ErrInconsistentObject reports an object that parses but contradicts
	// itself - a file object whose chunk lengths do not add up to its size,
	// a manifest whose entries are not sorted.
	ErrInconsistentObject = errors.New("inconsistent object")

	// ErrObjectNotFound reports a store lookup for an object that is not
	// there.
	ErrObjectNotFound = errors.New("object not found")

	// ErrCorruptObject reports store content whose hash does not match the key
	// it was stored under (E27). It is deliberately distinct from
	// ErrMalformedObject: malformed means someone wrote something that is not
	// a Fibula object, corrupt means the store handed back something other
	// than what was asked for.
	ErrCorruptObject = errors.New("corrupt object")

	// ErrInvalidRefName reports a ref name that cannot be stored safely (E13).
	ErrInvalidRefName = errors.New("invalid ref name")

	// ErrRefNotFound reports a ref that does not exist.
	ErrRefNotFound = errors.New("ref not found")

	// ErrInvalidStore reports a store that cannot be used as asked - an empty
	// directory name, a key that is not a well-formed object key.
	ErrInvalidStore = errors.New("invalid store")

	// ErrInvalidIgnore reports an unusable line in a .fibulaignore file (E18).
	ErrInvalidIgnore = errors.New("invalid ignore pattern")

	// ErrDirty reports that a space cannot be cleared because the data-loss
	// check did not pass (E17, CLAUDE.md invariant 6). It is always fatal to
	// the operation: when in doubt, abort.
	ErrDirty = errors.New("working directory is not safe to clear")

	// ErrNotASpace reports a directory that is not a Fibula space.
	ErrNotASpace = errors.New("not a fibula space")

	// ErrUnsafePath reports a manifest path that must not be written to disk
	// (CLAUDE.md section 4). Manifest paths are untrusted input even in
	// single-user operation.
	ErrUnsafePath = errors.New("unsafe path")

	// ErrRefConflict reports a compare-and-swap whose expected value did not
	// match the current one. It is never resolved by overwriting: a lost ref
	// update is a lost working state (E13).
	ErrRefConflict = errors.New("ref conflict")

	// ErrLockHeld reports an operation refused because somebody else holds the
	// file lock, or because the caller is not its owner (E49).
	ErrLockHeld = errors.New("file is locked")

	// ErrLockNotFound reports a lock that does not exist.
	ErrLockNotFound = errors.New("lock not found")

	// ErrSpaceBehind reports a commit whose working directory does not descend
	// from where the ref now points, so recording it would silently drop
	// whatever moved the ref (E13.3, TP-005 EC-401).
	//
	// The ref machinery cannot catch this: the compare-and-swap succeeds
	// because the value being replaced really is the one that was read. What
	// is wrong is one level up - the manifest describes a tree that never
	// contained the other side's work.
	ErrSpaceBehind = errors.New("space is behind the ref")
)
