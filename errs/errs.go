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
)
