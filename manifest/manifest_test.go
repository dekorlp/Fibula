package manifest

import (
	"errors"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

func fileID(content string) hash.FileID { return hash.File([]byte(content)) }

func TestBuilderSortsAndNormalizes(t *testing.T) {
	var b Builder

	// Added out of order, with a decomposed path as macOS would deliver it.
	mustAdd(t, &b, "zebra.png", fileID("z"), 3)
	mustAdd(t, &b, "assets/grün.png", fileID("g"), 2)
	mustAdd(t, &b, "Assets/hero.fbx", fileID("h"), 1)

	m, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []string{"Assets/hero.fbx", "assets/gr\u00fcn.png", "zebra.png"}
	if len(m.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(m.Entries), len(want))
	}
	for i, w := range want {
		if m.Entries[i].Path != w {
			t.Errorf("entry %d is %q, want %q", i, m.Entries[i].Path, w)
		}
	}
}

// TestBuildIsIndependentOfInsertionOrder is the determinism property from
// CLAUDE.md pinned point 4: two identical working directories must produce
// byte-identical manifests, whatever order the walker happened to visit them
// in.
func TestBuildIsIndependentOfInsertionOrder(t *testing.T) {
	entries := []struct {
		path string
		id   hash.FileID
		size int64
	}{
		{"assets/char/hero_diffuse.png", fileID("diffuse"), 8388608},
		{"assets/char/hero_mesh.fbx", fileID("mesh"), 24117248},
		{"levels/level_01.blend", fileID("level"), 4096},
		{"assets/env/tree.fbx", fileID("tree"), 512},
	}

	var forward, backward Builder
	for _, e := range entries {
		mustAdd(t, &forward, e.path, e.id, e.size)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		mustAdd(t, &backward, entries[i].path, entries[i].id, entries[i].size)
	}

	a, err := forward.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := backward.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	first, err := a.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	second, err := b.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("insertion order changed the manifest:\n%q\n%q", first, second)
	}
}

func TestBuilderRejections(t *testing.T) {
	tests := []struct {
		name    string
		add     func(*Builder) error
		wantErr error
	}{
		{
			name:    "absolute path",
			add:     func(b *Builder) error { return b.Add("/etc/passwd", fileID("x"), 1) },
			wantErr: errs.ErrInvalidPath,
		},
		{
			name:    "parent segment",
			add:     func(b *Builder) error { return b.Add("../outside.png", fileID("x"), 1) },
			wantErr: errs.ErrInvalidPath,
		},
		{
			name:    "negative size",
			add:     func(b *Builder) error { return b.Add("a.png", fileID("x"), -1) },
			wantErr: errs.ErrInconsistentObject,
		},
		{
			name:    "missing file id",
			add:     func(b *Builder) error { return b.Add("a.png", hash.FileID{}, 1) },
			wantErr: errs.ErrInconsistentObject,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b Builder

			if err := tc.add(&b); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestAddingTheSamePathTwiceIsAnError guards against a walker that resolves a
// symlink into its target, or descends the same directory twice: keeping the
// last entry silently would hide it.
func TestAddingTheSamePathTwiceIsAnError(t *testing.T) {
	var b Builder
	mustAdd(t, &b, "a.png", fileID("first"), 1)

	if err := b.Add("a.png", fileID("second"), 2); !errors.Is(err, errs.ErrInconsistentObject) {
		t.Errorf("err = %v, want errs.ErrInconsistentObject", err)
	}
}

// TestCaseCollisionIsRejectedAtBuildTime pins E8.4: the error belongs here and
// not at restore time, where Windows and a case-insensitive macOS volume could
// no longer tell the two paths apart.
func TestCaseCollisionIsRejectedAtBuildTime(t *testing.T) {
	var b Builder
	mustAdd(t, &b, "assets/Textur.png", fileID("upper"), 1)
	mustAdd(t, &b, "assets/textur.png", fileID("lower"), 1)

	if _, err := b.Build(); !errors.Is(err, errs.ErrCaseCollision) {
		t.Errorf("err = %v, want errs.ErrCaseCollision", err)
	}
}

// TestMultipleManifestRootsAreNotPrecluded covers E10: paths are relative to
// the manifest root, so two manifests in one project are independent states
// rather than a conflict.
func TestMultipleManifestRootsAreNotPrecluded(t *testing.T) {
	var characters, environment Builder
	mustAdd(t, &characters, "hero/hero.fbx", fileID("hero"), 1)
	mustAdd(t, &environment, "hero/hero.fbx", fileID("hero"), 1)

	a, err := characters.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := environment.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	idA, err := a.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	idB, err := b.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if idA != idB {
		t.Errorf("identical content under two roots gave %s and %s", idA, idB)
	}
}

func TestDiff(t *testing.T) {
	before := build(t,
		entry{"assets/hero.fbx", "hero", 10},
		entry{"assets/villain.fbx", "villain", 20},
		entry{"assets/tree.png", "tree", 30},
		entry{"assets/rock.png", "rock", 40},
	)
	after := build(t,
		entry{"assets/hero.fbx", "hero", 10},          // unchanged
		entry{"assets/villain.fbx", "villain v2", 21}, // changed
		entry{"characters/tree.png", "tree", 30},      // renamed
		entry{"assets/water.png", "water", 50},        // added
		// assets/rock.png removed
	)

	got := Diff(before, after)

	if len(got.Changed) != 1 || got.Changed[0].Path() != "assets/villain.fbx" {
		t.Errorf("Changed = %+v, want assets/villain.fbx", got.Changed)
	}
	if len(got.Renamed) != 1 ||
		got.Renamed[0].Before.Path != "assets/tree.png" ||
		got.Renamed[0].After.Path != "characters/tree.png" {
		t.Errorf("Renamed = %+v, want assets/tree.png to characters/tree.png", got.Renamed)
	}
	if len(got.Added) != 1 || got.Added[0].Path != "assets/water.png" {
		t.Errorf("Added = %+v, want assets/water.png", got.Added)
	}
	if len(got.Removed) != 1 || got.Removed[0].Path != "assets/rock.png" {
		t.Errorf("Removed = %+v, want assets/rock.png", got.Removed)
	}
}

func TestDiffOfIdenticalManifestsIsEmpty(t *testing.T) {
	m := build(t, entry{"a.png", "a", 1}, entry{"b.png", "b", 2})

	if got := Diff(m, m); !got.IsEmpty() {
		t.Errorf("Diff = %+v, want empty", got)
	}
}

// TestDiffTreatsACopyAsAnAddition: the original path is still there, so the
// second occurrence is not a move.
func TestDiffTreatsACopyAsAnAddition(t *testing.T) {
	before := build(t, entry{"a.png", "same", 1})
	after := build(t, entry{"a.png", "same", 1}, entry{"b.png", "same", 1})

	got := Diff(before, after)

	if len(got.Renamed) != 0 {
		t.Errorf("Renamed = %+v, want none", got.Renamed)
	}
	if len(got.Added) != 1 || got.Added[0].Path != "b.png" {
		t.Errorf("Added = %+v, want b.png", got.Added)
	}
}

// TestDiffPairsDuplicateContentDeterministically covers the awkward case: the
// same content at several paths, all moved at once. The pairing must not
// depend on map iteration order.
func TestDiffPairsDuplicateContentDeterministically(t *testing.T) {
	before := build(t,
		entry{"old/a.png", "same", 1},
		entry{"old/b.png", "same", 1},
		entry{"old/c.png", "same", 1},
	)
	after := build(t,
		entry{"new/a.png", "same", 1},
		entry{"new/b.png", "same", 1},
		entry{"new/c.png", "same", 1},
	)

	first := Diff(before, after)
	for i := 0; i < 20; i++ {
		got := Diff(before, after)

		if len(got.Renamed) != 3 {
			t.Fatalf("got %d renames, want 3", len(got.Renamed))
		}
		for j := range got.Renamed {
			if got.Renamed[j] != first.Renamed[j] {
				t.Fatalf("run %d paired %+v, first run paired %+v", i, got.Renamed[j], first.Renamed[j])
			}
		}
	}
}

func TestDiffAgainstAnEmptyManifest(t *testing.T) {
	m := build(t, entry{"a.png", "a", 1}, entry{"b.png", "b", 2})

	t.Run("everything added", func(t *testing.T) {
		if got := Diff(object.Manifest{}, m); len(got.Added) != 2 {
			t.Errorf("Added = %+v, want two entries", got.Added)
		}
	})

	t.Run("everything removed", func(t *testing.T) {
		if got := Diff(m, object.Manifest{}); len(got.Removed) != 2 {
			t.Errorf("Removed = %+v, want two entries", got.Removed)
		}
	})
}

type entry struct {
	path    string
	content string
	size    int64
}

func build(t *testing.T, entries ...entry) object.Manifest {
	t.Helper()

	var b Builder
	for _, e := range entries {
		mustAdd(t, &b, e.path, fileID(e.content), e.size)
	}

	m, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

func mustAdd(t *testing.T, b *Builder, p string, id hash.FileID, size int64) {
	t.Helper()

	if err := b.Add(p, id, size); err != nil {
		t.Fatalf("Add(%q): %v", p, err)
	}
}
