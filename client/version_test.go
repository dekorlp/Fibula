package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/store"
)

// TestCommitNeedsAMessage pins E12: a message is what makes a version
// deliberate, so there is no such thing as one without.
func TestCommitNeedsAMessage(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")

	for _, message := range []string{"", "   ", "\t"} {
		_, err := space.Commit(ctx, &Ignore{}, SnapshotOptions{Author: "dennis", Message: message})
		if !errors.Is(err, errs.ErrInconsistentObject) {
			t.Errorf("Commit(%q) err = %v, want errs.ErrInconsistentObject", message, err)
		}
	}
}

// TestCommitNeverExpires: a deliberate version is permanent, which is the
// whole distinction from a snapshot (E12).
func TestCommitNeverExpires(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")

	id := commit(ctx, t, space, "permanent")
	version, err := space.readVersion(ctx, id)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if !version.Expiry.IsZero() {
		t.Errorf("a deliberate version has an expiry of %v", version.Expiry)
	}
	if version.Message != "permanent" {
		t.Errorf("Message = %q", version.Message)
	}
}

// TestCommitsFormAChain: the deliberate history is the content graph, and it
// is the one that is never thinned.
func TestCommitsFormAChain(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "a.bin", "one")
	first := commit(ctx, t, space, "first")
	writeFile(t, space.Root(), "a.bin", "two")
	second := commit(ctx, t, space, "second")

	version, err := space.readVersion(ctx, second)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if len(version.Parents) != 1 || version.Parents[0] != first {
		t.Errorf("parents = %v, want [%s]", version.Parents, first)
	}

	entries, err := space.Log(ctx, store.DefaultRef, 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 2 || entries[0].Version != second || entries[1].Version != first {
		t.Errorf("log = %v, want newest first", entries)
	}
	for _, e := range entries {
		if e.Snapshot {
			t.Error("log reported a deliberate version as a snapshot")
		}
	}
}

// TestLogDoesNotShowSnapshots: mixing the timeline into the history would make
// history look different depending on how recently somebody saved.
func TestLogDoesNotShowSnapshots(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "a.bin", "one")
	commit(ctx, t, space, "the only deliberate version")
	writeFile(t, space.Root(), "a.bin", "two")
	snapshot(ctx, t, space)

	entries, err := space.Log(ctx, store.DefaultRef, 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("log has %d entries, want just the deliberate version", len(entries))
	}
}

func TestLogLimit(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	for _, content := range []string{"one", "two", "three"} {
		writeFile(t, space.Root(), "a.bin", content)
		commit(ctx, t, space, "version "+content)
	}

	entries, err := space.Log(ctx, store.DefaultRef, 2)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("log returned %d entries, want 2", len(entries))
	}
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "a.bin", "content")
	id := commit(ctx, t, space, "a version")

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "a ref", input: store.DefaultRef, want: true},
		{name: "a full hash", input: id.String(), want: true},
		{name: "a twelve-character prefix", input: id.String()[:12], want: true},
		{name: "the shortest accepted prefix", input: id.String()[:8], want: true},
		{name: "too short to be a prefix", input: id.String()[:7]},
		{name: "not hex", input: "zzzzzzzzzz"},
		{name: "no such version", input: strings.Repeat("0", 64)},
		{name: "no such ref", input: "nowhere"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := space.Resolve(ctx, tc.input)

			if !tc.want {
				if err == nil {
					t.Errorf("Resolve(%q) = %s, want an error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.input, err)
			}
			if got != id {
				t.Errorf("Resolve(%q) = %s, want %s", tc.input, got, id)
			}
		})
	}
}

// TestCheckoutRunsTheDirtyCheck is the note F-S4-04 makes: switching away from
// unsaved work destroys it exactly as deleting it would, so it goes through
// the same gate.
func TestCheckoutRunsTheDirtyCheck(t *testing.T) {
	ctx := context.Background()
	space, storeDir := newSpace(t)
	writeFile(t, space.Root(), "a.bin", strings.Repeat("content ", 500))
	first := commit(ctx, t, space, "first")

	writeFile(t, space.Root(), "b.bin", "second")
	commit(ctx, t, space, "second")

	removeOneChunk(t, storeDir)

	if _, err := space.Checkout(ctx, &Ignore{}, first.String(), clearOpts()); !errors.Is(err, errs.ErrDirty) {
		t.Errorf("err = %v, want errs.ErrDirty", err)
	}
	// Nothing was touched.
	assertContent(t, space, "b.bin", "second")
}

// TestCheckoutRemovesWhatTheTargetDoesNotHave is the half a plain restore does
// not do, and the reason checkout needs the dirty check first.
func TestCheckoutRemovesWhatTheTargetDoesNotHave(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "keep.bin", "kept")
	first := commit(ctx, t, space, "before the extra file")

	writeFile(t, space.Root(), "extra.bin", "added later")
	commit(ctx, t, space, "with the extra file")

	result, err := space.Checkout(ctx, &Ignore{}, first.String(), clearOpts())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("Removed = %d, want 1", result.Removed)
	}
	assertGone(t, space, "extra.bin")
	assertContent(t, space, "keep.bin", "kept")

	// And back again, which must bring it back.
	if _, err := space.Checkout(ctx, &Ignore{}, store.DefaultRef, clearOpts()); err != nil {
		t.Fatalf("Checkout back: %v", err)
	}
	assertContent(t, space, "extra.bin", "added later")
}

// TestCheckoutOfAVersionKeepsTheCurrentRef: a version is a place to look, a
// ref is a place to work.
func TestCheckoutOfAVersionKeepsTheCurrentRef(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "a.bin", "one")
	first := commit(ctx, t, space, "first")
	writeFile(t, space.Root(), "a.bin", "two")
	commit(ctx, t, space, "second")

	if _, err := space.Checkout(ctx, &Ignore{}, first.String(), clearOpts()); err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	head, err := space.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.Ref.Name() != store.DefaultRef {
		t.Errorf("the space moved onto %q, want to stay on %q", head.Ref.Name(), store.DefaultRef)
	}
	if head.Version != first {
		t.Errorf("head version = %s, want %s", head.Version, first)
	}
}

func TestDiff(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "keep.bin", "unchanged")
	writeFile(t, space.Root(), "edit.bin", "before")
	writeFile(t, space.Root(), "gone.bin", "doomed")
	first := commit(ctx, t, space, "first")

	writeFile(t, space.Root(), "edit.bin", "after, longer")
	writeFile(t, space.Root(), "new.bin", "fresh")
	if err := removeFile(space, "gone.bin"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	second := commit(ctx, t, space, "second")

	changes, err := space.Diff(ctx, first.String(), second.String())
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes.Added) != 1 || changes.Added[0].Path != "new.bin" {
		t.Errorf("Added = %v", changes.Added)
	}
	if len(changes.Removed) != 1 || changes.Removed[0].Path != "gone.bin" {
		t.Errorf("Removed = %v", changes.Removed)
	}
	if len(changes.Changed) != 1 || changes.Changed[0].Path() != "edit.bin" {
		t.Errorf("Changed = %v", changes.Changed)
	}

	// A state against itself is empty.
	same, err := space.Diff(ctx, store.DefaultRef, second.String())
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !same.IsEmpty() {
		t.Errorf("diff of a state against itself = %+v", same)
	}
}

func removeFile(space *Space, rel string) error {
	return os.Remove(filepath.Join(space.Root(), filepath.FromSlash(rel)))
}
