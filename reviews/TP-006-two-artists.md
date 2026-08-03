# TP-006 · Two artists on one store

**Date:** 2026-08-03
**Branch:** feature/tp006-two-artists
**Status:** Ready for review

## What was done

Exercised S5a's transfer half against real Blender assets and two separate
working directories on one store, before building the locking half on top of
it. Six test cases, no defects.

The `.blend` states came out of the TP-004 store, so the differences between
them are what Blender actually writes rather than something constructed for the
test.

## Spec reference

- **E44/E45** — the merge rules and the fact that phase 1 transfers rather than
  merges two lines
- **E46** — the conflict copy has to be openable; TC-502 is the evidence
- **E47** — a deletion never silently beats an edit; TC-503
- **E18** — `.fibulaignore` through a merge; TC-504
- **CLAUDE.md § Sequence** — "build it and use it" before adding more

## Context

Follows F-S5a-02 and F-S5a-03/04/09. The manual run during F-S5a-03 found a
defect (a `status` reporting clean over unrecorded work) that eight unit tests
had missed, which is the argument for doing this at a realistic scale before
continuing.

## Changed files

- `test-plans/TP-006-two-artists.md` — the plan and its results
- `test-plans/TEST_PLANS.md` — index row
- `reviews/TP-006-two-artists.md` — this document

No source changes.

## What to look at in review

**TC-502's evidence for E46 is indirect.** The conflict copy is byte-identical
to a file Blender wrote, carries the `BLENDER` magic and the right extension —
but nobody opened it in Blender. I state that in the plan rather than claiming
more than the run showed. If you want it closed properly, it needs a live
Blender.

**The `.blend` states were replayed, not produced live.** Blender was not
connected during this run, so the states come from TP-004. For the merge that
makes no difference — it sees three manifests with differing FileIDs either way
— but it does mean nothing here tested a save landing while a sync is running.

**TC-501's store growth is worth a glance.** Two edited 16 MiB assets grew the
store by 10.97 MiB, because the states share most of their chunks. That is dedup
working across artists rather than only across versions, which is the first time
we have seen it in this shape.

## What could still go wrong

- **Nothing raced.** Anna and Ben take turns. TP-005 showed concurrency is where
  the interesting failures live, and this plan does not revisit it.
- **Five assets.** `Merge` builds a map per side and sorts the union. Fine here,
  unmeasured at TP-002's 3,050 files.
- **A save during a sync is untested** and is a realistic thing for an artist to
  do — the scan reads files that another process may be rewriting.
- **Local store only.** The network-share item from TP-005 is still open, and a
  merge over SMB has never run.
- **The run says nothing about locking**, which is the half still to be built.

## Open questions

1. **Should a sync refuse to run while files are open for writing?** Out of
   scope here and probably unanswerable portably, but the save-during-sync case
   above is the one I would want a decision on before a studio uses this.
2. **Assumption made:** that replayed `.blend` states are equivalent to live
   ones for the purpose of testing the merge. I believe that for the merge
   itself and explicitly not for the scan, which is where a live writer would
   matter.
