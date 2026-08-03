# F-B-04 · Refuse a commit from a stale space

**Date:** 2026-08-03
**Branch:** feature/stale-commit-check
**Status:** Ready for review

## What was done

Implemented option (a) of F-B-04: a commit is refused when the working
directory does not descend from where the ref now points, instead of silently
replacing the other side's content.

Implementing it required separating two things the space had been conflating.
`Head` recorded one version, written by both commits and auto snapshots, so
"what is in the directory" and "what the next commit builds on" were the same
field. Since a snapshot carries no parent (E12), a space that snapshotted
between two commits had no way to say where it descended from. `Head` now
carries `Base` alongside `Version`.

The CLI prints an escape route with the error, because refusing without one
would have left `checkout` as the only move and that discards the user's work.

## Spec reference

- **E13.3** — "a non-fast-forward is a conflict and therefore a user decision,
  never an automatic overwrite". This is the check that makes it true.
- **E12** — snapshots are a timeline, not a chain, and carry no parent. That is
  precisely why `Base` has to exist separately from `Version`.
- **E16** — the space records its checked-out state locally. `Base` is an
  addition to that record, not a new kind of state.
- **CLAUDE.md invariant 6** — no data loss.

## Context

Direct follow-up to [TP-005](../test-plans/TP-005-network-share.md) EC-401,
which found the defect while verifying the network share, and to the decision
recorded in `Backlog/B-found-in-testing.md` to ship (a) now rather than wait
for S5's sync semantics.

## Changed files

- `errs/errs.go` — `ErrSpaceBehind`
- `client/space.go` — `Head.Base`; written by `SetHead`, read back optionally so
  that spaces created before this still load
- `client/snapshot.go` — `commitTarget.parentOf` performs the check;
  `recordTarget.advancesBase` distinguishes the two record paths; `recordHead`
  preserves the base across snapshots
- `client/version.go` — `baseVersion` replaces the head lookup
- `client/checkout.go` — checkout sets the base to what it checked out
- `cmd/fibula/main.go` — prints the escape route for `ErrSpaceBehind`
- `client/staleness_test.go` — five regression tests

## What to look at in review

**`client/snapshot.go`, `parentOf`.** The rule is that local base and ref must
agree exactly: both absent (first commit) or both present and equal. The three
rejection branches are distinguished only to produce a useful message. Worth
checking whether "space has no history yet, ref exists" should really be
refused — it is the `init` against a populated store case, and I decided that
committing over someone's history from a space that never checked it out is the
same defect wearing a different hat.

**`client/space.go`, reading `base`.** A space written before this change has no
`base` field and falls back to `version`. That is right when its last operation
was a commit and too permissive when it was a snapshot — i.e. exactly the old
behaviour, self-correcting at the next commit. The alternative was refusing to
load old spaces, which seemed disproportionate in phase 1.

**`cmd/fibula/main.go`, `behindAdvice`.** This is what makes option (a) livable
rather than a dead end, and it is only sound because TC-409 established that
snapshots cannot collide. Verified end to end: commit refused → `snapshot`
keeps bob's work → `checkout main` takes alice's → both exist.

## What could still go wrong

- **There is still no merge.** Bringing two diverged states together is manual,
  and the message says so. This makes the failure honest, not solved.
- **`promote` and `expire` were not exercised with two clients.** Named as
  unverified in TP-005 and still unverified. They share `record` but reach it by
  a different path.
- **The old-space fallback is untested against a real old space.** I reasoned
  about it from `parseFields`; there is no fixture of a pre-`base` head file.
  Worth adding if we care about phase-1 spaces surviving.
- **`Base` is local state with no store-side counterpart.** If `.fibula` is
  copied between machines — which is exactly what my own test setup does — the
  base travels with it. That is correct here but is an assumption S5 should
  revisit when a space can follow a remote.
- **The check compares identity, not ancestry.** It cannot tell "behind" from
  "diverged", so both produce the same message. With linear history in phase 1
  that distinction does not yet exist.

## Open questions

1. **Should `init` against a populated store refuse, or check out?** Today it
   creates a space with no head, and the first commit is then refused. Checking
   out automatically would be friendlier, but `init` silently populating a
   directory is its own surprise. Left as is deliberately.
2. **Assumption made:** that refusing is better than a `--force` escape hatch.
   I did not add one. If it turns out people need it, it should probably record
   both parents rather than drop one — which is the merge conversation, and
   therefore S5.
3. **Does `Base` belong in the head file at all, or should it be derived?** It
   could be reconstructed by walking the version chain to find the newest
   ancestor that is not a snapshot. That needs no new state but costs a store
   walk on every commit; I chose the field. Reversible either way.
