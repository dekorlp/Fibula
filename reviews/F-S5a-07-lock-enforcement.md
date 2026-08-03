# F-S5a-07 · Lock enforcement on commit

**Date:** 2026-08-03
**Branch:** feature/s5a-lock-enforcement
**Status:** Ready for review

## What was done

A commit is refused when it would publish a change to a file somebody else
holds. This is the point at which a lock stops meaning anything — until now it
only prevented another `lock`.

Also adds `FIBULA_AUTHOR`, which was not planned and turned out to be necessary
(see below).

## Spec reference

- **E49** — editing is always allowed, committing is checked; the commit check
  is the boundary and the read-only attribute is only a reminder
- **E12** — a snapshot is never blocked: it lands on its own ref and is the
  safety net that has to work when things are going wrong
- **E51** — an expired lock stops nothing, because anyone may take it over

## Context

Built before F-S5a-06 (the read-only attribute), against the backlog order.
The attribute is a reminder that some tools drop; this is the actual boundary,
and without it a lock is decoration.

## Changed files

- `client/locks.go` — `checkLocksForCommit`, `lockedError`
- `client/target.go` — `enforcesLocks` on the record target
- `client/snapshot.go` — the check runs for commits only
- `client/lock_enforcement_test.go` — seven tests
- `cmd/fibula/main.go` — `FIBULA_AUTHOR`, and advice printed with `ErrLockHeld`

## What to look at in review

**Only changed paths are checked.** Holding a lock on a file you are not
touching has to be free, or one reservation would block everyone from
committing anything. Added, changed, removed and both sides of a rename all
count — a locked path can be deleted and recreated, and recreating it is
exactly what the holder reserved it against.

**A snapshot is never blocked**, expressed as `enforcesLocks()` on the target
rather than as a condition inside the check. A contested file is when a
snapshot matters most; blocking it would take the safety net away at the worst
moment.

**`FIBULA_AUTHOR` is new and was not in the plan.** The end-to-end run silently
passed where it should have failed: `currentAuthor()` returns the OS username,
so two working directories on one machine are the same person and no lock ever
applies between them. That is not only a test problem — a studio with a shared
machine account would have the same experience — but it is worth noting that
the unit tests were green throughout, because they pass authors explicitly.

**The error names every blocking lock**, not just the first, so one refusal
tells the user everything they have to sort out.

## What could still go wrong

- **`Owner` is still a claim.** With `FIBULA_AUTHOR` anyone can be anyone. This
  is a coordination aid, not a permission system (E51), and it will stay that
  way until there is authentication to attach it to.
- **No read-only attribute yet** (F-S5a-06), so the refusal arrives at commit
  time rather than when the file is opened. That is the late discovery E49
  accepts but would rather avoid.
- **The message cannot say "locked since your last sync".** F-S5a-07's backlog
  entry asks for that distinction, and the head records no timestamp to compare
  against. It reports when the lock was taken instead, which the user can judge
  themselves. Deviation stated rather than silently dropped.
- **The check runs after the scan.** Chunks are already in the store by the time
  a commit is refused. They are unreferenced and GC collects them, but a refused
  commit still costs the upload.
- **No `cmd/fibula` test.** Verified by hand.

## Open questions

1. **Should `FIBULA_AUTHOR` be a config field rather than an environment
   variable?** The store has settings now, and the space has local config. An
   env var is right for a test override and probably wrong as the only way a
   studio sets identities.
2. **Assumption made:** that a commit removing a locked file should be refused.
   Nothing in E49 says so explicitly — it says "a foreign lock covers a changed
   path" — and deleting is the most destructive way to interfere with a
   reservation, so I read it as covered.
