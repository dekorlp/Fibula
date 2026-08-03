# F-S5a-03/04/09 · `fibula sync`, conflict copies, and the advice text

**Date:** 2026-08-03
**Branch:** feature/s5a-manifest-merge
**Status:** Ready for review

## What was done

`fibula sync` brings a working directory onto the current state of its ref,
merging local changes rather than refusing them. With it, the situation F-B-04
could only reject now runs to completion: alice commits, bob syncs, bob commits,
and neither side's work is lost.

Three backlog items in one change, because they are one feature: the command
(F-S5a-03), the conflict copies it writes (F-S5a-04), and pointing the staleness
message at it (F-S5a-09).

## Spec reference

- **E52** — one command, no `fetch`/`pull` pair; on a shared store there is
  nothing to fetch
- **E45** — transfer rather than merge: the commit afterwards has one parent
- **E44** — the rule table, via `manifest.Merge` from F-S5a-02
- **E46** — conflict copies under `.fibula/conflicts/`, original name and
  extension
- **E47** — a deletion never silently beats an edit
- **E53** — `commit` does not sync by itself; `status` reports leftover copies

## Context

Follows F-S5a-02 (`manifest.Merge`) and closes the gap F-B-04 opened. See
`refinements/2026-08-03-multi-user.md`.

## Changed files

- `client/sync.go` — `Sync`, the merge application, conflict copies,
  `PendingConflicts`
- `client/snapshot.go` — `scanWorkingTree` extracted from `record`, so sync and
  snapshot share one scan
- `client/cache.go` — `Cache.Remove`
- `client/sync_test.go` — eight tests
- `client/staleness_test.go` — `secondSpaceOn` now checks files out
- `cmd/fibula/sync.go` — the command and the status reporting
- `cmd/fibula/main.go` — registration, usage, revised `behindAdvice`

## What to look at in review

**`applyMerged` writes the difference, not the manifest.** That is not an
optimisation. Our own uncommitted entries are already correct on disk and some
are not in the store at all, so a wholesale restore would fail on exactly the
files that matter most.

**The cache handling, which an end-to-end run caught.** My first version called
`refreshCacheFrom(merged)` after the sync. That made `status` report a clean
directory over work nobody had recorded — `Status` compares against the cache,
so refreshing it wholesale erases the fact that local changes are uncommitted.
Only files the sync actually wrote may move forward. `TestSyncLeaves\
OurWorkLookingUncommitted` covers it.

The cost is that our own changed files fall out of the cache's coverage and get
re-hashed on the next commit. Correct over fast, and small in practice.

**`settleAfterSync` records *their* state, not the merged one.** The merged
state is not a version; recording it as head would claim that uncommitted work
had been published.

**Conflict copies are cleared at the start of each sync.** A copy from an
earlier run describes a state that has moved on. Nothing is lost — every copy is
reconstructible from the store.

## What could still go wrong

- **No test for a conflict inside a subdirectory.** `SafeJoin` handles the path,
  but the conflict copies all live at the top level in the tests.
- **`writeConflictCopies` clears the directory before writing.** If the sync then
  fails partway, the previous copies are gone and the new ones incomplete. The
  data is all in the store, so this is recoverable by re-running, but it is a
  window.
- **`scanWorkingTree` uploads chunks as a side effect.** That is inherited from
  the snapshot path and it means a sync writes objects nothing references yet.
  GC's grace window covers them, but on a slow remote it is work the user did
  not ask for. Worth revisiting when S5b makes uploads expensive.
- **Renames still pass through as delete-plus-add** (E45's open list).
- **No large-scale run.** Everything here is unit-level plus a manual
  end-to-end. How a sync behaves at TP-002's 3,050 files is unmeasured.

## Open questions

1. **Should `sync` snapshot first, on its own?** It changes the working
   directory, and the safety net exists precisely for that. I left it out
   because `behindAdvice` offers the snapshot explicitly and doing it silently
   would make every sync write a version. Reasonable either way.
2. **Assumption made:** that clearing `.fibula/conflicts/` at the start of a
   sync is right. The alternative is to keep copies until the user removes them,
   which would preserve an unfinished decision across syncs at the cost of
   showing stale content.
3. **`status` now reports conflicts even when clean.** That reads slightly
   oddly — "clean, 4 files unchanged" followed by an unresolved conflict — but
   the alternative is hiding the only trace of an unfinished decision behind a
   clean directory.
