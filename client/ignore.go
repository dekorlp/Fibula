package client

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/dekorlp/fibula/errs"
)

// IgnoreFile is the name of the ignore file in the space root (E18).
const IgnoreFile = ".fibulaignore"

// Ignore decides which working-directory entries Fibula leaves alone.
//
// The syntax is gitignore's, because the target audience already knows it and
// there is no reason to invent a second one (E18). What is supported:
//
//   - blank lines and lines starting with # are skipped
//   - a leading ! negates, and the last matching rule wins
//   - a trailing / matches directories only
//   - a / anywhere but at the end anchors the pattern to the space root;
//     without one the pattern matches at any depth
//   - * and ? do not cross a path separator, ** matches any number of segments
//   - [abc] character classes, as in path.Match
//
// Deliberately not supported: backslash escaping of the special characters
// themselves. A backslash cannot occur in a Fibula path at all (E8), so a
// pattern needing one could never match anything.
type Ignore struct{ rules []ignoreRule }

type ignoreRule struct {
	negate   bool
	dirOnly  bool
	segments []string
	source   string
}

// ParseIgnore reads ignore rules in the order they appear. Order matters: the
// last rule that matches decides, which is what makes a negation able to carve
// an exception out of a broader rule.
func ParseIgnore(r io.Reader) (*Ignore, error) {
	ignore := &Ignore{}
	scanner := bufio.NewScanner(r)

	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if line == 1 {
			// A UTF-8 BOM is the default rather than the exception on Windows:
			// PowerShell's Out-File and several editors write one. Left in
			// place it becomes part of the first pattern, which then silently
			// matches nothing while every later rule works (TP-004 EC-301).
			text = strings.TrimPrefix(text, "\ufeff")
		}

		rule, ok, err := parseIgnoreLine(text)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", IgnoreFile, line, err)
		}
		if ok {
			ignore.rules = append(ignore.rules, rule)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", IgnoreFile, err)
	}
	return ignore, nil
}

// LoadIgnore reads the ignore file from a space root. A missing file is not an
// error - it means nothing is ignored.
func LoadIgnore(root string) (*Ignore, error) {
	f, err := os.Open(path.Join(root, IgnoreFile)) //nolint:gosec // the fixed ignore file of a space
	if os.IsNotExist(err) {
		return &Ignore{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", IgnoreFile, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	return ParseIgnore(f)
}

func parseIgnoreLine(line string) (ignoreRule, bool, error) {
	line = strings.TrimRight(line, " \t")
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false, nil
	}

	rule := ignoreRule{source: line}
	pattern := line

	if strings.HasPrefix(pattern, "!") {
		rule.negate = true
		pattern = pattern[1:]
	}
	if strings.HasSuffix(pattern, "/") {
		rule.dirOnly = true
		pattern = strings.TrimSuffix(pattern, "/")
	}
	if pattern == "" {
		return ignoreRule{}, false, fmt.Errorf("%w: %q has no pattern", errs.ErrInvalidIgnore, line)
	}

	// A pattern containing a separator is anchored to the space root; one
	// without matches at any depth, which is the same as prefixing "**/".
	anchored := strings.Contains(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	if !anchored {
		pattern = "**/" + pattern
	}

	rule.segments = strings.Split(pattern, "/")
	return rule, true, nil
}

// Match reports whether p is ignored. The path is relative to the space root
// and uses "/" as its separator, like every other Fibula path.
//
// Callers walking a tree should test directories too and skip the whole
// subtree when one matches. That mirrors git, where a file under an excluded
// directory cannot be re-included by a later negation - and it is the reason
// clearing a space can report ignored content without descending into it.
func (i *Ignore) Match(p string, isDir bool) bool {
	if i == nil {
		return false
	}

	segments := strings.Split(p, "/")
	ignored := false

	for _, rule := range i.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		if matchSegments(rule.segments, segments) {
			ignored = !rule.negate
		}
	}
	return ignored
}

// Empty reports whether any rule was configured at all.
func (i *Ignore) Empty() bool { return i == nil || len(i.rules) == 0 }

// matchSegments matches a segmented pattern against a segmented path, with
// "**" standing for any number of segments including none.
//
// It is a plain recursive matcher rather than a compiled regular expression:
// patterns are few and paths are short, and a hand-written matcher is
// something a reader can check against the gitignore documentation line by
// line.
func matchSegments(pattern, segments []string) bool {
	switch {
	case len(pattern) == 0:
		return len(segments) == 0
	case pattern[0] == "**":
		// Try consuming zero, one, two ... leading path segments.
		for i := 0; i <= len(segments); i++ {
			if matchSegments(pattern[1:], segments[i:]) {
				return true
			}
		}
		return false
	case len(segments) == 0:
		return false
	}

	ok, err := path.Match(pattern[0], segments[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pattern[1:], segments[1:])
}
