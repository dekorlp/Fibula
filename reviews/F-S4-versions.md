# F-S4-01 … F-S4-07 · Versions, refs, expiry, GC

**Date:** 2026-08-02
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Slice S4 complete, plus a dated addendum resolving a second contradiction in
the object model. This is the slice that turns Fibula from a backup tool into
version control: deliberate versions on `main`, promotion, log, checkout, diff,
the thinning schedule and garbage collection with a full reference check.

With it, **S0–S4 are done and the milestone is reached** — with one part of the
acceptance test outstanding, stated plainly below and in
`test-plans/TP-001-self-hosting-dry-run.md`.

## Spec reference

| Task | Implements |
|---|---|
| **F-S4-01** | E11 (parent list from day one), E12 |
| **F-S4-02** | E13 (CAS mandatory, local and remote namespaces separate) |
| **F-S4-03** | E12 (promotion creates a new object, the snapshot expires normally) |
| **F-S4-04** | E11, E12, F-S1-06; and F-S3-04, because checkout runs the dirty check |
| **F-S4-05** | E14 (thinning schedule) |
| **F-S4-06** | CLAUDE.md invariant 8, E14, E25 |
| **F-S4-07** | CLAUDE.md § Sequence — the milestone acceptance test |

**Addendum:** `refinements/2026-08-02-object-model.md` gains a dated addendum to
E12 — the snapshot timeline is a list of refs, not a parent chain.

## The contradiction the slice had to resolve first

**E14 requires expiring individual snapshots. Parent pointers make that
impossible.** If S3 names S2 as its parent, S2 stays reachable for as long as
S3 exists, so the middle of a timeline can never be thinned — only the newest
end could ever be dropped, which is the opposite of what a thinning schedule
does.

E12 already contains the answer in its own words: *"the snapshot chain is a
timeline, not a content graph"*. A timeline is an ordered list; a content graph
is parent pointers. So:

- An auto snapshot has **no parents**. It is a point in time.
- The timeline is the set of refs `local/snapshots/<timestamp>-<prefix>`.
  Expiring one is deleting its ref.
- Deliberate versions keep their parent list and stay the content graph that
  `log` walks and that is never thinned.

The payoff is that E14's hard coupling — expiring a snapshot never deletes a
chunk a reachable deliberate version references — **falls out of the ordinary
reference check** instead of needing a special case in the traversal. That is
the strongest argument for the change: the safety property stops being a rule
somebody has to remember.

This supersedes F-S3-03's phrasing, which is what S3 shipped. Phase 1 gives no
compatibility guarantee; a chain written by an S3 build is read as unrelated
snapshots, with the ordering taken from the ref names.

## Two defects the dry run found

Neither would have been caught by the unit tests as written. Both are recorded
in TP-001 as EC-001 and EC-002.

### 1. The CLI could not read back its own output

`log` and `snapshots` print twelve-character abbreviations; `checkout` and
`diff` accepted only the full 64-character hash. Every hash the tool printed
had to be looked up elsewhere before it could be used.

Fixed by resolving prefixes of at least eight hex characters against every
version the space knows. An ambiguous prefix is an error rather than a guess —
picking one of two versions to check out is the kind of helpfulness that loses
work.

### 2. Promotion silently replaced the working state

`promote` advanced `main` to the promoted snapshot. Nothing was lost — the
previous tip stayed in history — but the next `checkout main` produced a tree
without the newest asset, with no warning.

E12 says *"yesterday's 14:20 state was good, I'll keep that one"*. **Keep means
make permanent, not revert to.** Promotion now publishes under its own ref
(`promoted/<timestamp>-<prefix>`), which is what makes the state survive expiry
and collection, and leaves `main` alone. Wanting that state back is a checkout,
and a checkout is an explicit act.

## What to look at in review

### Garbage collection reaches around the store interface

`walkStore` reads the filesystem layout directly instead of going through
`ObjectStore`. That is deliberate: E24 gives the interface exactly Get, Put and
Exists, and adding a `List` for GC's sake would hand every caller a way to
enumerate the store. Keeping the enumeration in one backend-specific function
means a future backend supplies its own, and no client gains a capability it
should not have. It is the one place in `client` that knows a store is a
directory, and it is worth checking that I have not let that leak further.

### The grace period is the only thing between GC and an in-flight upload

A client that has written its chunks but not yet the manifest naming them looks
exactly like a client that wrote garbage. One hour is the default. Too short
and a slow upload loses its chunks mid-flight; too long and abandoned uploads
accumulate. Nothing measured says one hour — it is a judgement call, and the
test that covers it uses an artificially advanced clock rather than waiting.

### Checkout goes through the dirty check

F-S4-04 notes it and it matters: switching away from unsaved work destroys it
exactly as deleting it would. `Checkout` therefore calls `CheckClear` and, like
`Clear`, snapshots unversioned files rather than refusing. The half a plain
restore does not do — removing files the target state does not contain — is
precisely why the check has to come first.

### `Retain` sorts its own input

It decides what gets thrown away, and a function whose result silently depends
on the caller having sorted its input is one that will eventually be called
wrongly. `Timeline` already returns chronological order; `Retain` no longer
relies on it.

## What could still go wrong

- **The milestone is not fully discharged.** TP-001 ran against synthetic
  binary assets, not a real Blender project. The dedup rate on real asset
  formats is unmeasured — and that measurement is the main reason E36 left the
  door open to FastCDC. Scale (three files, not three thousand), network shares
  and genuinely long time spans are all untested.
- **The daily and weekly retention buckets have only unit-test coverage.** No
  snapshot in any run was ever really a week old; every expiry test advances
  the clock artificially.
- **GC has no locking.** A collection running while another process commits can
  see a half-published state: the chunks written, the manifest not yet. The
  grace window covers the common case, but a slow enough upload combined with a
  short enough grace window is still a hole. A proper answer needs the store to
  offer a lock, which S2's interfaces do not.
- **GC deletes one object per call.** `Delete` takes a slice and is handed a
  single key each time, which is fine on a filesystem and will be an N+1 the
  moment a network store exists.
- **Expiry never runs by itself.** There is no schedule, no daemon; `fibula
  expire` is manual. That is consistent with S3 having no timer either, and it
  means an unattended space accumulates snapshot refs indefinitely.
- **`Log` shows only the deliberate history.** A user looking for "the state I
  had yesterday afternoon" has to know to run `snapshots` instead. Correct, per
  E12, and quite possibly confusing.
- **Checkout of a bare version leaves the space in a detached-looking state**
  without saying so. The head version and the head ref disagree, and the next
  `commit` will go onto the ref, not onto what is checked out.
- **`promoted/` refs are never cleaned up.** They are permanent by design, but
  nothing lists them and nothing removes them, so a habit of promoting will
  quietly grow the ref namespace.

## Open questions

1. **Is the E12 addendum the right resolution?** It changes behaviour S3
   shipped. The alternative would be to keep parent chains and give up on
   thinning the middle of the timeline, which reads to me like giving up on
   E14 — but it is the maintainer's call.
2. **Is one hour the right grace window?** See above. It wants a number from
   observation rather than from me.
3. **Should `promote` also be able to move a ref**, for the user who genuinely
   does want to revert to that state? Today they must `checkout` and `commit`.
   That is two steps and no ambiguity; a `--checkout` flag would be one step
   and some.
4. **The GraphID addendum from S3 is still unanswered.** S4 now writes version
   objects for real, so the field's placement is live — though still unused,
   since nothing produces a graph yet.
