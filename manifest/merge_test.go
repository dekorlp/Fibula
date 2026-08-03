package manifest

import (
	"testing"

	"github.com/dekorlp/fibula/object"
)

// side describes one manifest in a merge case. An empty content string means
// the path is absent from that side.
type side string

const absent side = ""

func manifestOf(path string, s side) object.Manifest {
	if s == absent {
		return object.Manifest{}
	}
	return object.Manifest{Entries: []object.Entry{
		{Path: path, File: fileID(string(s)), Size: int64(len(s))},
	}}
}

// TestMergeRules walks every row of the table in E44. The names are the rows,
// so a failure says which rule broke rather than which line number.
func TestMergeRules(t *testing.T) {
	const path = "assets/hero.blend"

	tests := []struct {
		name         string
		base         side
		ours         side
		theirs       side
		want         side // absent means the path is gone from the result
		wantConflict bool
		wantKind     ConflictKind
	}{
		{name: "unchanged on both sides", base: "X", ours: "X", theirs: "X", want: "X"},
		{name: "ours only", base: "X", ours: "Y", theirs: "X", want: "Y"},
		{name: "theirs only", base: "X", ours: "X", theirs: "Z", want: "Z"},
		{
			name: "both made the same edit", base: "X", ours: "Y", theirs: "Y", want: "Y",
		},
		{
			name: "both edited differently", base: "X", ours: "Y", theirs: "Z",
			want: "Y", wantConflict: true, wantKind: BothChanged,
		},
		{name: "we deleted it, they left it alone", base: "X", ours: absent, theirs: "X", want: absent},
		{name: "they deleted it, we left it alone", base: "X", ours: "X", theirs: absent, want: absent},
		{
			name: "we edited, they deleted", base: "X", ours: "Y", theirs: absent,
			want: "Y", wantConflict: true, wantKind: ChangedDeleted,
		},
		{
			name: "we deleted, they edited", base: "X", ours: absent, theirs: "Z",
			want: "Z", wantConflict: true, wantKind: DeletedChanged,
		},
		{name: "added by us", base: absent, ours: "Y", theirs: absent, want: "Y"},
		{name: "added by them", base: absent, ours: absent, theirs: "Z", want: "Z"},
		{name: "both added the same content", base: absent, ours: "Y", theirs: "Y", want: "Y"},
		{
			name: "both added different content", base: absent, ours: "Y", theirs: "Z",
			want: "Y", wantConflict: true, wantKind: BothAdded,
		},
		{name: "deleted by both", base: "X", ours: absent, theirs: absent, want: absent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merged, conflicts := Merge(
				manifestOf(path, tc.base),
				manifestOf(path, tc.ours),
				manifestOf(path, tc.theirs),
			)

			assertEntry(t, merged, path, tc.want)

			switch {
			case tc.wantConflict && len(conflicts) != 1:
				t.Fatalf("got %d conflicts, want exactly one", len(conflicts))
			case !tc.wantConflict && len(conflicts) != 0:
				t.Fatalf("got conflict %v, want none", conflicts[0].Kind)
			case !tc.wantConflict:
				return
			}

			if conflicts[0].Kind != tc.wantKind {
				t.Errorf("conflict kind = %v, want %v", conflicts[0].Kind, tc.wantKind)
			}
			if conflicts[0].Path != path {
				t.Errorf("conflict path = %q, want %q", conflicts[0].Path, path)
			}
		})
	}
}

func assertEntry(t *testing.T, m object.Manifest, path string, want side) {
	t.Helper()

	if want == absent {
		if len(m.Entries) != 0 {
			t.Fatalf("result kept %q, want it gone", m.Entries[0].Path)
		}
		return
	}

	if len(m.Entries) != 1 {
		t.Fatalf("result has %d entries, want one", len(m.Entries))
	}
	if got := m.Entries[0]; got.Path != path || got.File != fileID(string(want)) {
		t.Errorf("result entry = {%s %s}, want {%s %s}", got.Path, got.File, path, fileID(string(want)))
	}
}

// TestConflictCarriesTheirVersion: the caller writes Theirs to
// .fibula/conflicts/ so the file can be opened (E46), so it has to be there -
// and it has to be nil where the other side deleted it, because then there is
// nothing to write.
func TestConflictCarriesTheirVersion(t *testing.T) {
	const path = "assets/hero.blend"

	_, conflicts := Merge(manifestOf(path, "X"), manifestOf(path, "Y"), manifestOf(path, "Z"))
	if len(conflicts) != 1 || conflicts[0].Theirs == nil || conflicts[0].Ours == nil {
		t.Fatalf("a both-changed conflict must carry both sides: %+v", conflicts)
	}
	if conflicts[0].Theirs.File != fileID("Z") {
		t.Errorf("Theirs = %s, want the version from theirs", conflicts[0].Theirs.File)
	}

	_, conflicts = Merge(manifestOf(path, "X"), manifestOf(path, "Y"), object.Manifest{})
	if len(conflicts) != 1 || conflicts[0].Theirs != nil {
		t.Errorf("they deleted it, so there is nothing to write: %+v", conflicts)
	}
}

// TestMergeResultIsCanonical: the result goes straight into a manifest object,
// so it has to be sorted and valid without a second pass (E8.5).
func TestMergeResultIsCanonical(t *testing.T) {
	ours := object.Manifest{Entries: []object.Entry{
		{Path: "a.png", File: fileID("a"), Size: 1},
		{Path: "m.png", File: fileID("m"), Size: 1},
		{Path: "z.png", File: fileID("z"), Size: 1},
	}}
	theirs := object.Manifest{Entries: []object.Entry{
		{Path: "b.png", File: fileID("b"), Size: 1},
		{Path: "n.png", File: fileID("n"), Size: 1},
	}}

	merged, conflicts := Merge(object.Manifest{}, ours, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("disjoint additions conflicted: %v", conflicts)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("the merged manifest is not canonical: %v", err)
	}

	want := []string{"a.png", "b.png", "m.png", "n.png", "z.png"}
	for i, entry := range merged.Entries {
		if entry.Path != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, entry.Path, want[i])
		}
	}
}

// TestMergeOfIdenticalSidesChangesNothing is the fast-forward shape: if we have
// made no local change, the result is exactly theirs.
func TestMergeOfIdenticalSidesChangesNothing(t *testing.T) {
	state := object.Manifest{Entries: []object.Entry{
		{Path: "a.png", File: fileID("a"), Size: 1},
		{Path: "b.png", File: fileID("b"), Size: 1},
	}}

	merged, conflicts := Merge(state, state, state)
	if len(conflicts) != 0 {
		t.Fatalf("identical sides conflicted: %v", conflicts)
	}
	if len(merged.Entries) != len(state.Entries) {
		t.Fatalf("got %d entries, want %d", len(merged.Entries), len(state.Entries))
	}
}

// TestUnrelatedPathsAreIndependent: the common case in an asset project is two
// people touching different files, and it must produce no conflict at all.
func TestUnrelatedPathsAreIndependent(t *testing.T) {
	base := object.Manifest{Entries: []object.Entry{
		{Path: "hero.blend", File: fileID("h1"), Size: 1},
		{Path: "level.blend", File: fileID("l1"), Size: 1},
	}}
	ours := object.Manifest{Entries: []object.Entry{
		{Path: "hero.blend", File: fileID("h2"), Size: 1},
		{Path: "level.blend", File: fileID("l1"), Size: 1},
	}}
	theirs := object.Manifest{Entries: []object.Entry{
		{Path: "hero.blend", File: fileID("h1"), Size: 1},
		{Path: "level.blend", File: fileID("l2"), Size: 1},
	}}

	merged, conflicts := Merge(base, ours, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("independent edits conflicted: %v", conflicts)
	}

	got := map[string]string{}
	for _, e := range merged.Entries {
		got[e.Path] = e.File.String()
	}
	if got["hero.blend"] != fileID("h2").String() {
		t.Error("our edit to hero.blend was lost")
	}
	if got["level.blend"] != fileID("l2").String() {
		t.Error("their edit to level.blend was lost")
	}
}
