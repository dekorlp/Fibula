# TP-001 · Self-hosting dry run

**Date:** 2026-08-02
**Task:** F-S4-07
**Branch:** claude/kannst-loslegen-zbfu63
**Executed:** yes, against the built binary
**Result:** pass, with two defects found and fixed, and one deviation from the
task as written

## Scope

F-S4-07 is the acceptance test for the S0–S4 milestone: version a real project
against an `fs.Store` and confirm that the working directory can be reproduced
from the store at any point.

### Deviation from the task, stated plainly

**The task asks for a real Blender project. This run used synthetic binary
assets instead.** There is no Blender project available in the environment this
was executed in, and inventing one would not have made the run more truthful.

What that costs is not nothing, and it is worth naming:

- Real `.blend` files have internal structure — the chunker's dedup rate on
  them is still **unmeasured**, and that measurement is the main reason E36
  left the door open to FastCDC.
- Real editing sessions produce patterns synthetic writes do not: incremental
  saves, autosave files, texture re-exports, renamed asset trees.
- File counts in a real project are in the thousands, not the handful here.

**The milestone is therefore not fully discharged.** What this run does
establish is that the mechanism is sound end to end. What it does not establish
is how the mechanism behaves on the data it was built for. A second run against
a real project remains outstanding and belongs in phase 1 proper.

## Environment

- Binary built from this branch, `go build ./cmd/fibula`
- Store: a plain directory (`fs.Store`), no server, no S3, no Postgres
- Assets: 4 MB, 800 KB and 200 KB of random binary content, plus an ignored
  `.blend1` backup

## Test cases

| ID | Case | Result |
|---|---|---|
| TC-001 | `init` creates a space and a store | pass |
| TC-002 | `commit -m` records a deliberate version | pass |
| TC-003 | Editing one file and taking a snapshot re-reads only that file | pass — 1 of 3 files chunked |
| TC-004 | A second commit records the added asset | pass |
| TC-005 | `log` shows deliberate history, newest first | pass |
| TC-006 | `snapshots` lists the timeline with expiry dates | pass |
| TC-007 | `diff` between two versions reports added/removed/changed | pass |
| TC-008 | `checkout` of an older version removes what it does not contain | pass |
| TC-009 | `checkout main` restores the tip byte-identically | pass — md5 match |
| TC-010 | `promote` makes a snapshot permanent | pass, after EC-002 |
| TC-011 | `space clear` frees the working directory | pass |
| TC-012 | `restore` reproduces it byte-identically | pass — md5 match |
| TC-013 | `expire` applies the thinning schedule | pass |
| TC-014 | `gc --dry-run` changes nothing | pass |
| TC-015 | `gc` deletes nothing that is still reachable | pass — 0 deleted, 18 reachable |
| TC-016 | Clear and restore *after* a collection still reproduce the tree | pass — md5 match |

## Edge cases and defects found

### EC-001 · The CLI could not read back its own output — **defect, fixed**

`log` and `snapshots` print twelve-character abbreviations. `checkout` and
`diff` accepted only the full 64-character hash, so every hash the tool printed
had to be looked up somewhere else before it could be used.

    $ fibula diff main c8df8fbd8217
    fibula: ref not found: "c8df8fbd8217" is neither a ref nor a version

Fixed by resolving prefixes of at least eight hex characters. An ambiguous
prefix is an error rather than a guess: picking one of two versions to check
out is the kind of helpfulness that loses work.

### EC-002 · Promotion silently replaced the working state — **design defect, fixed**

`promote` advanced `main` to the promoted snapshot. Nothing was lost — the
previous tip stayed in history — but the next `checkout main` produced a tree
without the newest asset, and nothing had warned about it.

E12's wording is "yesterday's 14:20 state was good, **I'll keep that one**".
Keep means make permanent, not revert to. Promotion now publishes the new
version under its own ref (`promoted/<timestamp>-<prefix>`), which is what
makes it survive expiry and collection, and leaves `main` where it was. Wanting
that state back is a checkout, and a checkout is an explicit act.

This was found only by running the loop by hand; the unit tests as written
would have accepted either behaviour.

### EC-003 · Nothing was collected on a healthy store

`gc` reported `18 objects reachable, 18 scanned, deleted 0`. That is correct
and worth recording as the expected shape of a healthy run: everything in the
store is reachable, so a collection is a no-op. Deletion only happens after
snapshots have actually expired, which on a store minutes old they have not.

### EC-004 · Expiry kept the only snapshot

`expire` reported `1 snapshots kept, 0 expired` on a store created moments
before, which is the schedule working: a snapshot from the last hour is inside
the hourly window.

## What this run did not cover

- **Real asset formats**, as stated above. Nothing here says anything about
  dedup rates on `.blend`, `.exr`, `.psd` or `.wav`.
- **Scale.** Three files, not three thousand. Nothing about walk time, cache
  size or manifest chunking at project scale is confirmed.
- **A NAS share.** The store was a local directory. Network filesystems are
  where the ref locking and the `Put` flush actually earn their keep, and they
  were not exercised.
- **Long time spans.** Every expiry test used an artificially advanced clock.
  No snapshot in this run was ever genuinely a week old, so the daily and
  weekly buckets of the schedule have only unit-test coverage.
- **Concurrent clients.** One process throughout.

## Follow-up

Two defects were found and fixed in this branch; both are recorded in
`reviews/F-S4-versions.md`. No backlog entries are opened for them.

The outstanding work is the run this plan could not do: a real project, at
real scale, over a real time span, against a real share. Per CLAUDE.md that
run is what phase 1 is for, and its findings are the input for S5 and S6.
