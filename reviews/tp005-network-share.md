# TP-005 · Network share — SMB store

**Date:** 2026-08-03
**Branch:** feature/tp005-network-share
**Status:** Ready for review

## What was done

Executed the last outstanding item of the S0–S4 milestone: running the store on
a network filesystem. Used an SMB path (`\\localhost\C$\...`) so that every
store operation passed through the real SMB protocol stack without requiring
elevation or changing system configuration.

The SMB mechanics passed. While verifying the concurrency case, a **critical
defect unrelated to SMB** surfaced: a commit takes its parent from head while
describing the local working directory, so one client's content silently
replaces another's. It is filed rather than fixed, because the fix is a design
decision about sync semantics.

## Spec reference

- **E13.3** — "a non-fast-forward is a conflict and therefore a user decision,
  never an automatic overwrite". EC-401 is an automatic overwrite.
- **E13.1/E13.2** — CAS and the lock file, which is what this plan set out to
  exercise on a network filesystem. Both hold.
- **E16** — the space records its checked-out VersionID, which is the state the
  missing check would use.
- **CLAUDE.md invariant 6** — no data loss.

## Context

Fourth test plan in the chain. TP-001, TP-002 and TP-004 each closed naming the
network share as untested; this closes it as far as loopback allows. It also
corrects TP-002's reading of its own TC-114, which recorded "CAS held" where
content had in fact been lost.

## Changed files

- `test-plans/TP-005-network-share.md` — the plan and its results
- `test-plans/TEST_PLANS.md` — index row for TP-005
- `Backlog/B-found-in-testing.md` — **F-B-04** filed with both fix options
- `Backlog/index.md` — milestone status; the defect called out above the slice
  table because it outranks everything else open
- `reviews/tp005-network-share.md` — this document

No source changes.

## What to look at in review

**F-B-04's two options** are the thing to actually decide. Option (a) — reject a
commit whose checked-out VersionID differs from the ref — is a few lines and
strictly better than today, but leaves the user with `checkout` as their only
exit, which discards their work. Option (b) is the real answer and needs the
conflict strategy that is still open. My recommendation is to ship (a) now and
let it force the S5 conflict discussion, but that is a judgement call about how
much friction is acceptable in phase 1.

**The correction to TP-002.** Test plans are never modified, so the correction
lives in TP-005 and in this document. If you would rather it were recorded
differently — an addendum file, a note in `TEST_PLANS.md` — say so; the
convention does not cover corrections and this is the first one.

**The "what is not real" section of TP-005.** Loopback SMB is a genuine protocol
test and a poor network test. The clock-skew case around `lockStale` is called
out as the most likely real-NAS defect precisely because this run structurally
cannot see it.

## What could still go wrong

- **The clock-skew case is untested and plausible.** `breakStaleLock` trusts
  that a file's `ModTime` and `time.Since` come from the same clock. Over SMB
  the server stamps the file. A NAS 60 s ahead makes every lock look stale
  immediately, which removes the cross-process mutex silently — and that turns
  F-B-04's category of problem into a genuine corruption risk rather than a
  content-loss one.
- **No interruption test.** `Put`'s atomicity over SMB is argued from
  temp-plus-rename, not demonstrated against a dropped connection.
- **One machine, one redirector.** No oplock contention, no Linux or macOS
  client.
- **F-B-04's blast radius: `snapshot` is clear, `promote` and `expire` are not
  checked.** TC-409 verified that snapshots cannot collide — they CAS against a
  zero old value, so each lands on its own ref. `promote` and `expire` were not
  exercised with two clients, and I make no claim about them.

## Open questions

1. **Which F-B-04 option, and when?** Blocking question for S5, since sync
   semantics cannot be designed around a commit path that silently overwrites.
2. ~~Does the same gap exist for `snapshot`?~~ **Answered: no** (TC-409).
   Snapshots CAS against a zero old value, so each lands on its own ref and two
   clients interleave correctly. The safety net holds where the deliberate
   path does not — which is worth knowing when weighing how urgent option (a)
   is.
3. **Assumption made:** that loopback SMB is a meaningful substitute for a NAS
   at the protocol level. I believe it for locking and rename semantics, and
   explicitly not for timing, clocks or connection loss. If you have a NAS
   available, the run is worth repeating there before S5.
