# F-S5a-05 (part 2) · Lock commands

**Date:** 2026-08-03
**Branch:** feature/s5a-lock-commands
**Status:** Ready for review

## What was done

The client and CLI side of file locks, completing F-S5a-05. Locking is now
reachable: switch it on for a project, reserve files by path or pattern, see who
holds what, give them back.

It is **not yet enforced** — a lock is visible and takes precedence when
somebody tries to take it, but nothing refuses a commit and no file is marked
read-only. Those are F-S5a-07 and F-S5a-06.

## Spec reference

- **E48** — opt-in per project; operations on a project without locking are
  refused rather than silently writing locks nobody honours
- **E50** — one lock per file; a pattern expands, it does not become a directory
  lock
- **E51** — an expired lock may be taken over by anyone; `--force` is the same
  operation applied to one that has not expired, and both record who lost it

## Context

Second half of F-S5a-05, after the store side shipped. Follows F-S5a-01, which
put the settings this reads in the store.

## Changed files

- `client/locks.go` — `Lock`, `Unlock`, `Locks`, `LockedBySomeoneElse`
- `client/locks_test.go` — nine tests
- `client/space.go` — the space carries a lock store
- `cmd/fibula/locks.go` — `lock`, `unlock`, `locks`, `locking`
- `cmd/fibula/usage.go` — the help text, moved out of `main.go`
- `cmd/fibula/main.go` — registration
- `errs/errs.go` — `ErrLockingDisabled`

## What to look at in review

**An expired lock needs no `--force`.** Taking over from a colleague who is ill
should not require the same flag as taking one from somebody actively working —
if it did, people would learn to use `--force` habitually and the distinction
would stop meaning anything. `lockOne` treats the two the same way at the store
level and differently at the decision level.

**`Lock` reports what it already took when it fails partway.** A pattern that
matches twenty files and fails on the fifteenth leaves fourteen locks held. I
chose to report them rather than roll back: a half-finished run the user cannot
see is exactly how files end up reserved by accident. Worth disagreeing with if
you would rather it were atomic.

**Patterns reuse the ignore matcher.** `levels/**` works because
`ParseIgnore` already implements that syntax and the audience learned it for
`.fibulaignore`. A second pattern language would be a gratuitous thing to
remember.

**`ErrLockingDisabled` exists because the first version reused `ErrLockHeld`**,
and the message read "file is locked: locking is not enabled for this project" —
which is wrong twice over. Caught by running the commands rather than by a test.

## What could still go wrong

- **Nothing enforces a lock yet.** `LockedBySomeoneElse` is written and tested
  and has no caller. Until F-S5a-06 and 07, a lock is advisory in the weakest
  sense: it stops another `lock`, and nothing else.
- **`Owner` is whatever `currentAuthor()` returns**, which is an OS username.
  Two people on the same machine account are the same person as far as locks
  are concerned.
- **The pattern is matched against the working directory**, so a file that is
  not there cannot be locked — including one you are about to create. Defensible
  but it will surprise somebody.
- **`unlock --force` releases rather than transfers.** The backlog said
  "transfers, showing who held it"; I read that as belonging to `lock --force`
  (take it over) and made `unlock --force` mean what its name says (give it
  back, whoever holds it). Deviation stated rather than silently made.
- **No CLI test.** The commands are covered end to end by hand, not by
  `cmd/fibula` tests.

## Open questions

1. **Should `lock` be atomic across a pattern?** See above. Rolling back on
   failure is the other reasonable answer, and it is a one-line change to
   `Lock`.
2. **Assumption made:** that `locking off` should leave existing locks in the
   store rather than clearing them. They become inert — `LockedBySomeoneElse`
   returns nothing — and reappear if locking is switched back on. Clearing them
   would be destructive for a setting somebody might flip by accident.
