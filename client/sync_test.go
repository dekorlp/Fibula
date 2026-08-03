package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dekorlp/fibula/manifest"
)

func syncSpace(ctx context.Context, t *testing.T, space *Space) SyncResult {
	t.Helper()

	result, err := space.Sync(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return result
}

// TestSyncCompletesTheStaleCommitScenario is the whole point of S5a. F-B-04
// could only refuse bob's commit; this runs the situation to its end and checks
// that neither side's work is lost.
func TestSyncCompletesTheStaleCommitScenario(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "shared.txt", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	writeFile(t, alice.Root(), "alice.txt", "alice worked here")
	commit(ctx, t, alice, "alice adds her file")

	writeFile(t, bob.Root(), "bob.txt", "bob worked here")
	result := syncSpace(ctx, t, bob)

	if len(result.Conflicts) != 0 {
		t.Fatalf("different files conflicted: %v", result.Conflicts)
	}
	if got := readAll(t, bob, "alice.txt"); got != "alice worked here" {
		t.Errorf("alice.txt = %q after sync", got)
	}
	if got := readAll(t, bob, "bob.txt"); got != "bob worked here" {
		t.Errorf("bob.txt = %q after sync - his own work was disturbed", got)
	}

	// And the commit that F-B-04 refused now goes through.
	commit(ctx, t, bob, "bob adds his file")

	for _, name := range []string{"shared.txt", "alice.txt", "bob.txt"} {
		if _, err := os.Stat(filepath.Join(bob.Root(), name)); err != nil {
			t.Errorf("%s is missing after bob's commit: %v", name, err)
		}
	}
}

// TestSyncFastForwardsWithACleanDirectory: no local changes means the result is
// exactly the other side, through the same code path as any other sync (E52).
func TestSyncFastForwardsWithACleanDirectory(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "shared.txt", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	writeFile(t, alice.Root(), "new.txt", "added by alice")
	writeFile(t, alice.Root(), "shared.txt", "changed by alice")
	commit(ctx, t, alice, "alice works")

	result := syncSpace(ctx, t, bob)
	if len(result.Conflicts) != 0 {
		t.Fatalf("a clean directory produced conflicts: %v", result.Conflicts)
	}
	if got := readAll(t, bob, "shared.txt"); got != "changed by alice" {
		t.Errorf("shared.txt = %q, want her version", got)
	}
	if got := readAll(t, bob, "new.txt"); got != "added by alice" {
		t.Errorf("new.txt = %q", got)
	}
}

// TestSyncRemovesWhatTheOtherSideDeleted: a deletion has to arrive too, or the
// directory quietly diverges.
func TestSyncRemovesWhatTheOtherSideDeleted(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "keep.txt", "keep")
	writeFile(t, alice.Root(), "gone.txt", "gone")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	if err := os.Remove(filepath.Join(alice.Root(), "gone.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	commit(ctx, t, alice, "alice removes a file")

	syncSpace(ctx, t, bob)

	if _, err := os.Stat(filepath.Join(bob.Root(), "gone.txt")); !os.IsNotExist(err) {
		t.Error("the deleted file is still in bob's directory")
	}
	if got := readAll(t, bob, "keep.txt"); got != "keep" {
		t.Errorf("keep.txt = %q", got)
	}
}

// TestSyncIsANoOpWhenAlreadyCurrent guards against a sync that does work it
// does not need to - including rewriting files that are already right.
func TestSyncIsANoOpWhenAlreadyCurrent(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "asset.txt", "content")
	commit(ctx, t, space, "baseline")

	result := syncSpace(ctx, t, space)
	if !result.AlreadyCurrent {
		t.Error("a current space reported work to do")
	}
	if result.Written != 0 || result.Removed != 0 {
		t.Errorf("wrote %d and removed %d files while already current", result.Written, result.Removed)
	}
}

// TestSyncOnAConflictKeepsOursAndCopiesTheirs covers E46: the working directory
// keeps our version, theirs lands under .fibula/conflicts/ with its original
// name so it can actually be opened.
func TestSyncOnAConflictKeepsOursAndCopiesTheirs(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "hero.blend", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	writeFile(t, alice.Root(), "hero.blend", "alice's version")
	commit(ctx, t, alice, "alice edits the hero")

	writeFile(t, bob.Root(), "hero.blend", "bob's version")
	result := syncSpace(ctx, t, bob)

	if len(result.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want one", len(result.Conflicts))
	}
	if result.Conflicts[0].Kind != manifest.BothChanged {
		t.Errorf("conflict kind = %v, want BothChanged", result.Conflicts[0].Kind)
	}

	if got := readAll(t, bob, "hero.blend"); got != "bob's version" {
		t.Errorf("the working directory holds %q, want bob's own version", got)
	}

	copyPath := filepath.Join(bob.Root(), SpaceDir, conflictsDir, "hero.blend")
	data, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("no conflict copy at %s: %v", copyPath, err)
	}
	if string(data) != "alice's version" {
		t.Errorf("conflict copy holds %q, want alice's version", data)
	}
}

// TestSyncKeepsAFileTheOtherSideDeleted covers E47: a deletion never silently
// wins over an edit.
func TestSyncKeepsAFileTheOtherSideDeleted(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "contested.txt", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	if err := os.Remove(filepath.Join(alice.Root(), "contested.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	commit(ctx, t, alice, "alice deletes it")

	writeFile(t, bob.Root(), "contested.txt", "bob edited it")
	result := syncSpace(ctx, t, bob)

	if len(result.Conflicts) != 1 || result.Conflicts[0].Kind != manifest.ChangedDeleted {
		t.Fatalf("conflicts = %+v, want one ChangedDeleted", result.Conflicts)
	}
	if got := readAll(t, bob, "contested.txt"); got != "bob edited it" {
		t.Errorf("the contested file is %q - the deletion won", got)
	}
}

// TestSyncLeavesOurWorkLookingUncommitted is the bug an end-to-end run caught:
// refreshing the whole cache after a sync made status report a clean directory
// while local changes sat in it unrecorded. Only files the sync actually wrote
// may move forward in the cache.
func TestSyncLeavesOurWorkLookingUncommitted(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "shared.txt", "baseline")
	writeFile(t, alice.Root(), "hero.blend", "hero v1")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	writeFile(t, alice.Root(), "alice.txt", "hers")
	commit(ctx, t, alice, "alice works")

	// Bob adds one file and edits another; neither is recorded anywhere.
	writeFile(t, bob.Root(), "bob.txt", "his")
	writeFile(t, bob.Root(), "hero.blend", "hero, bob's take")
	syncSpace(ctx, t, bob)

	status, err := bob.Status(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.IsClean() {
		t.Fatal("status reports clean over work that was never recorded")
	}

	inList := func(list []string, want string) bool {
		for _, p := range list {
			if p == want {
				return true
			}
		}
		return false
	}
	if !inList(status.Added, "bob.txt") {
		t.Errorf("bob.txt is not reported as added: %+v", status)
	}
	if !inList(status.Modified, "hero.blend") {
		t.Errorf("hero.blend is not reported as modified: %+v", status)
	}
	// The file the sync brought in is genuinely up to date and must not appear.
	if inList(status.Added, "alice.txt") || inList(status.Modified, "alice.txt") {
		t.Errorf("alice.txt was written by the sync and should be unchanged: %+v", status)
	}
}

// TestASecondSyncClearsTheEarlierConflictCopies: a copy from a resolved
// conflict describes a state that has moved on, and a stale one is worse than
// none. Nothing is lost by clearing - every copy is reconstructible from the
// store.
func TestASecondSyncClearsTheEarlierConflictCopies(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "hero.blend", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	// A genuine conflict, which leaves a copy behind.
	writeFile(t, alice.Root(), "hero.blend", "alice's version")
	commit(ctx, t, alice, "alice edits the hero")
	writeFile(t, bob.Root(), "hero.blend", "bob's version")

	if result := syncSpace(ctx, t, bob); len(result.Conflicts) != 1 {
		t.Fatalf("expected the setup to conflict, got %v", result.Conflicts)
	}
	copyPath := filepath.Join(bob.Root(), SpaceDir, conflictsDir, "hero.blend")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("no conflict copy to begin with: %v", err)
	}

	// Bob settles it and moves on; alice then changes something unrelated.
	commit(ctx, t, bob, "bob keeps his version")
	writeFile(t, alice.Root(), "notes.txt", "unrelated")
	syncSpace(ctx, t, alice)
	commit(ctx, t, alice, "alice adds notes")

	if result := syncSpace(ctx, t, bob); len(result.Conflicts) != 0 {
		t.Fatalf("the second sync conflicted: %v", result.Conflicts)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Error("the copy from the resolved conflict survived the next sync")
	}
}
