package path

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
)

// newFolder returns a caser doing full Unicode case folding, which is a good
// deal more than ToLower: it is what makes "STRASSE" and "strasse" compare
// equal.
//
// It is constructed per call rather than kept in a package variable. x/text
// states that "a Caser may be stateful and should therefore not be shared
// between goroutines"; the fold caser happens to be stateless today, so a
// shared one would work — but only by implementation detail, and manifest
// parsing is exactly the code path a sync engine will run in parallel. The
// caser is one small allocation and is reused across all paths within a call.
func newFolder() cases.Caser { return cases.Fold() }

// Normalize brings a path into the canonical Fibula form and validates it
// (E8). The separator must already be "/" — see Validate for why converting
// from the host separator is the caller's job and not this package's.
//
// The only thing Normalize changes is the Unicode form: macOS hands out
// NFD-decomposed names from APFS, so without this the same file would produce
// a different manifest entry on macOS than on Windows. Everything else it
// rejects rather than repairs.
func Normalize(p string) (string, error) {
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("%w: not valid UTF-8", errs.ErrInvalidPath)
	}

	normalized := norm.NFC.String(p)
	if err := Validate(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// Validate reports whether p is already in canonical form. Parsing an existing
// object uses this rather than Normalize: a stored object that is not
// canonical must be rejected, not silently repaired, because its ID was
// computed over the bytes as they stand.
//
// The rules read as a checklist against E8 on purpose.
func Validate(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%w: empty", errs.ErrInvalidPath)
	case !utf8.ValidString(p):
		return fmt.Errorf("%w: not valid UTF-8", errs.ErrInvalidPath)
	case !norm.NFC.IsNormalString(p):
		return fmt.Errorf("%w: %q is not in Unicode NFC", errs.ErrInvalidPath, p)
	case strings.HasPrefix(p, format.PathSeparator):
		return fmt.Errorf("%w: %q is absolute, paths are relative to the manifest root", errs.ErrInvalidPath, p)
	case strings.Contains(p, `\`):
		// Rejected rather than converted: a backslash is a legal filename
		// character on Linux and macOS, so converting it here would corrupt
		// those names, while keeping it would produce an asset that cannot be
		// checked out on Windows at all. Converting the host separator is the
		// caller's job, where the host is actually known (filepath.ToSlash).
		return fmt.Errorf("%w: %q contains a backslash, use %q as the separator", errs.ErrInvalidPath, p, format.PathSeparator)
	}

	if err := validateCharacters(p); err != nil {
		return err
	}
	return validateSegments(p)
}

// validateCharacters enforces E8.6 and widens it slightly: the refinement
// forbids tab and LF because they are the field and line separators, and this
// also rejects the remaining C0 controls and DEL. None of them can occur in a
// Windows filename, NUL cannot be passed to a filesystem call at all, and CR
// would make a path indistinguishable from a CRLF line ending in a diff.
func validateCharacters(p string) error {
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("%w: %q contains the control character %#02x", errs.ErrInvalidPath, p, c)
		}
	}
	return nil
}

func validateSegments(p string) error {
	for _, segment := range strings.Split(p, format.PathSeparator) {
		switch segment {
		case "":
			return fmt.Errorf("%w: %q has an empty segment", errs.ErrInvalidPath, p)
		case ".":
			return fmt.Errorf("%w: %q contains a %q segment", errs.ErrInvalidPath, p, ".")
		case "..":
			return fmt.Errorf("%w: %q contains a %q segment", errs.ErrInvalidPath, p, "..")
		}
	}
	return nil
}

// FoldKey returns the case-folded form of p, the key two paths collide on
// under E8.4. It is not a path and must never be stored or restored — Fibula
// keeps the original case (E8.4), this only detects the clash.
func FoldKey(p string) string { return newFolder().String(p) }

// CheckCollisions reports the first pair of paths that differ only in case.
// The input does not have to be sorted.
func CheckCollisions(paths []string) error {
	folder := newFolder()

	seen := make(map[string]string, len(paths))
	for _, p := range paths {
		key := folder.String(p)
		if other, dup := seen[key]; dup && other != p {
			return fmt.Errorf("%w: %q and %q", errs.ErrCaseCollision, other, p)
		}
		seen[key] = p
	}
	return nil
}

// Less orders two paths the way a manifest is sorted: bytewise over their
// UTF-8 bytes, never locale-dependent (E8.5). Go's string comparison is
// already bytewise, which is exactly the point — this function exists so that
// call sites say what ordering they mean.
func Less(a, b string) bool { return a < b }

// Sort orders paths in place as a manifest requires.
func Sort(paths []string) {
	sort.Slice(paths, func(i, j int) bool { return Less(paths[i], paths[j]) })
}
