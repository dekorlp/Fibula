package client

import (
	"context"
	"strings"
	"testing"
)

func TestIgnoreMatch(t *testing.T) {
	rules := strings.Join([]string{
		"# Blender leaves these behind",
		"*.blend1",
		"",
		"# Substance temporaries, anywhere",
		"temp/",
		"",
		"# anchored to the space root",
		"/build",
		"",
		"# a nested pattern is anchored by its separator",
		"cache/textures/*.tmp",
		"",
		"# everything under exports, but keep the finals",
		"exports/**",
		"!exports/final/",
		"",
		// Only trailing whitespace is trimmed; leading whitespace is part of
		// the pattern, as in gitignore.
		"trailing-space-is-trimmed  ",
	}, "\n")

	ignore, err := ParseIgnore(strings.NewReader(rules))
	if err != nil {
		t.Fatalf("ParseIgnore: %v", err)
	}

	tests := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		{name: "plain asset", path: "assets/hero.fbx"},
		{name: "backup at the root", path: "hero.blend1", want: true},
		{name: "backup nested", path: "assets/char/hero.blend1", want: true},
		{name: "a directory pattern matches a directory", path: "assets/temp", isDir: true, want: true},
		{name: "a directory pattern does not match a file", path: "assets/temp", want: false},
		{name: "temp nested anywhere", path: "a/b/temp", isDir: true, want: true},
		{name: "anchored pattern at the root", path: "build", isDir: true, want: true},
		{name: "anchored pattern does not match deeper", path: "assets/build", isDir: true},
		{name: "nested pattern matches exactly", path: "cache/textures/a.tmp", want: true},
		{name: "nested pattern is anchored", path: "deep/cache/textures/a.tmp"},
		{name: "double star matches at any depth", path: "exports/a/b/c.png", want: true},
		{name: "negation carves out an exception", path: "exports/final", isDir: true, want: false},
		{name: "trailing whitespace is trimmed from the rule", path: "trailing-space-is-trimmed", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ignore.Match(tc.path, tc.isDir); got != tc.want {
				t.Errorf("Match(%q, isDir=%v) = %v, want %v", tc.path, tc.isDir, got, tc.want)
			}
		})
	}
}

// TestLastMatchingRuleWins is what makes negation usable at all.
func TestLastMatchingRuleWins(t *testing.T) {
	ignore, err := ParseIgnore(strings.NewReader("*.png\n!keep.png\n*.tmp\n"))
	if err != nil {
		t.Fatalf("ParseIgnore: %v", err)
	}

	tests := map[string]bool{
		"a.png":    true,
		"keep.png": false,
		"a.tmp":    true,
		"a.fbx":    false,
	}
	for p, want := range tests {
		if got := ignore.Match(p, false); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIgnoreCommentsAndBlankLines(t *testing.T) {
	ignore, err := ParseIgnore(strings.NewReader("\n\n# just a comment\n\n   \n"))
	if err != nil {
		t.Fatalf("ParseIgnore: %v", err)
	}
	if !ignore.Empty() {
		t.Error("comments and blank lines produced rules")
	}
	if ignore.Match("anything", false) {
		t.Error("an empty ignore matched something")
	}
}

func TestNilIgnoreMatchesNothing(t *testing.T) {
	var ignore *Ignore

	if ignore.Match("anything", false) || !ignore.Empty() {
		t.Error("a nil Ignore is not inert")
	}
}

// TestIgnoredDirectoriesAreNotDescendedInto mirrors git: a file under an
// excluded directory cannot be re-included by a later negation, and the
// scanner never looks inside.
func TestIgnoredDirectoriesAreNotDescendedInto(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "assets/hero.fbx", "asset")
	writeFile(t, space.Root(), "build/output/big.bin", "generated")
	writeFile(t, space.Root(), IgnoreFile, "/build\n")

	ignore, err := LoadIgnore(space.Root())
	if err != nil {
		t.Fatalf("LoadIgnore: %v", err)
	}

	files, err := space.Scan(ctx, ignore)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	for _, f := range files {
		if strings.HasPrefix(f.Path, "build/") {
			t.Errorf("the scanner descended into an ignored directory: %s", f.Path)
		}
	}
	if len(files) != 2 { // the asset and .fibulaignore itself
		t.Errorf("scanned %d files, want the asset and the ignore file: %v", len(files), pathsOf(files))
	}
}

// TestTheIgnoreFileIsItselfVersioned: it is project configuration and belongs
// to everyone working on the project, so it is not ignored by default.
func TestTheIgnoreFileIsItselfVersioned(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), IgnoreFile, "*.blend1\n")

	ignore, err := LoadIgnore(space.Root())
	if err != nil {
		t.Fatalf("LoadIgnore: %v", err)
	}
	files, err := space.Scan(ctx, ignore)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if len(files) != 1 || files[0].Path != IgnoreFile {
		t.Errorf("scanned %v, want just %q", pathsOf(files), IgnoreFile)
	}
}

// TestTheSpaceDirectoryIsNeverScanned: .fibula/ holds the local state, which
// must never end up inside a manifest.
func TestTheSpaceDirectoryIsNeverScanned(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "asset")
	snapshot(ctx, t, space)

	files, err := space.Scan(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, SpaceDir) {
			t.Errorf("the scanner returned local state: %s", f.Path)
		}
	}
}

func TestLoadIgnoreWithoutAFile(t *testing.T) {
	ignore, err := LoadIgnore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadIgnore: %v", err)
	}
	if !ignore.Empty() {
		t.Error("a missing ignore file produced rules")
	}
}

func pathsOf(files []ScannedFile) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}
