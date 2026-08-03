package client

import (
	"context"
	"errors"
	"testing"

	"github.com/dekorlp/fibula/errs"
)

// secondSpaceOn returns another space sharing one store, standing exactly where
// the first one does - two people who checked out the same state.
//
// It checks the files out rather than only copying the head. Setting the head
// alone leaves an empty directory, which a staleness check does not notice but
// a merge reads as "this side deleted everything".
func secondSpaceOn(t *testing.T, storeDir string, first *Space) *Space {
	t.Helper()
	ctx := context.Background()

	second, err := Init(t.TempDir(), storeDir)
	if err != nil {
		t.Fatalf("Init second space: %v", err)
	}
	head, err := first.Head()
	if err != nil {
		t.Fatalf("Head of the first space: %v", err)
	}
	if _, err := second.Checkout(ctx, &Ignore{}, head.Version.String(), clearOpts()); err != nil {
		t.Fatalf("Checkout into the second space: %v", err)
	}
	return second
}

// TestCommitRefusesWhenTheRefHasMovedOn is TP-005 EC-401. Two clients on one
// store, no concurrency: alice commits, then bob commits from the state they
// both started at. Before the check, bob's commit named alice's as its parent
// and dropped her file - an automatic overwrite, which E13.3 forbids.
func TestCommitRefusesWhenTheRefHasMovedOn(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "shared.txt", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	writeFile(t, alice.Root(), "alice.txt", "alice worked here")
	commit(ctx, t, alice, "alice adds her file")

	writeFile(t, bob.Root(), "bob.txt", "bob worked here")
	_, err := bob.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author: "bob", Message: "bob adds his file", Now: fixedTime(),
	})

	if !errors.Is(err, errs.ErrSpaceBehind) {
		t.Fatalf("bob's stale commit returned %v, want ErrSpaceBehind", err)
	}
}

// TestSnapshotsDoNotMakeACommitLookStale guards the fix against itself. A
// snapshot records the working directory without moving what the space
// descends from (E12), so any number of them between two commits must leave
// the second one perfectly valid.
func TestSnapshotsDoNotMakeACommitLookStale(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "asset.txt", "one")
	commit(ctx, t, space, "first")

	writeFile(t, space.Root(), "asset.txt", "two")
	snapshot(ctx, t, space)
	writeFile(t, space.Root(), "asset.txt", "three")
	snapshot(ctx, t, space)

	if _, err := space.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author: "dennis", Message: "second", Now: fixedTime(),
	}); err != nil {
		t.Fatalf("a commit after two snapshots was refused: %v", err)
	}
}

// TestSnapshotBeforeAnyCommitStillCommits: a space whose first recorded state
// is a snapshot has no base yet, and must not be mistaken for one that is
// behind.
func TestSnapshotBeforeAnyCommitStillCommits(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	writeFile(t, space.Root(), "asset.txt", "content")
	snapshot(ctx, t, space)

	if _, err := space.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author: "dennis", Message: "first deliberate version", Now: fixedTime(),
	}); err != nil {
		t.Fatalf("the first commit after a snapshot was refused: %v", err)
	}
}

// TestConsecutiveCommitsStayValid: the base has to move with each commit, or
// the second one would look stale against the ref its predecessor just set.
func TestConsecutiveCommitsStayValid(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	for _, content := range []string{"one", "two", "three"} {
		writeFile(t, space.Root(), "asset.txt", content)
		commit(ctx, t, space, "version "+content)
	}
}

// TestCheckoutMakesTheSpaceCommittableAgain: checkout is the way out of a
// refused commit today, so it must leave the space in a state that can commit.
func TestCheckoutMakesTheSpaceCommittableAgain(t *testing.T) {
	ctx := context.Background()
	alice, storeDir := newSpace(t)

	writeFile(t, alice.Root(), "shared.txt", "baseline")
	commit(ctx, t, alice, "baseline")

	bob := secondSpaceOn(t, storeDir, alice)

	writeFile(t, alice.Root(), "alice.txt", "alice worked here")
	newest := commit(ctx, t, alice, "alice adds her file")

	if _, err := bob.Checkout(ctx, &Ignore{}, newest.String(), clearOpts()); err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	writeFile(t, bob.Root(), "bob.txt", "bob worked here")
	if _, err := bob.Commit(ctx, &Ignore{}, SnapshotOptions{
		Author: "bob", Message: "bob adds his file", Now: fixedTime(),
	}); err != nil {
		t.Fatalf("a commit after checking out the newest version was refused: %v", err)
	}

	// And alice's file survived, which is the whole point.
	if got := readAll(t, bob, "alice.txt"); got != "alice worked here" {
		t.Errorf("alice.txt = %q after bob's commit", got)
	}
}
