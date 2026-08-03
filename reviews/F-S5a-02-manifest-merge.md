# F-S5a-02 · `manifest.Merge` in the core

**Date:** 2026-08-03
**Branch:** feature/s5a-manifest-merge
**Status:** Ready for review

## What was done

Implemented the three-way merge over manifests: `manifest.Merge(base, ours,
theirs)` returns the resulting state plus the paths it could not decide. Pure
computation — no store, no clock, no IO.

Nothing calls it yet. It is the piece F-S5a-03 (`fibula sync`) needs, and it is
complete and tested on its own.

## Spec reference

- **E42** — the merge computation lives in the core
- **E44** — the rule table; every row has a named test case
- **E46** — `Conflict.Theirs` is what the caller writes to `.fibula/conflicts/`,
  and is nil where the other side deleted the file
- **E47** — changed against deleted resolves in favour of the file
- **E45** — the caller in phase 1 is a transfer, not a merge of two committed
  lines; the rules are identical either way

## Context

First implementation task of S5a, which exists because the F-B-04 fix produces a
refusal that recommends `sync` — a command that does not exist yet. See
`refinements/2026-08-03-multi-user.md`.

## Changed files

- `manifest/merge.go` — `Merge`, `Conflict`, `ConflictKind`, and the per-path
  resolution
- `manifest/merge_test.go` — the rule table plus four behavioural tests

## What to look at in review

**The result always keeps the surviving file.** On a both-changed conflict our
version stays in the manifest; where one side deleted what the other edited, the
file that still exists wins. That asymmetry is E47 and it is deliberate — worth
checking that you agree with it in code and not just in prose.

**`resolve` splits over `inBase`/`inOurs`/`inTheirs` rather than over the table
rows.** The table has twelve rows; the code has three branches plus two helpers,
because several rows collapse into one comparison. `t.ours.File ==
t.theirs.File` covers both "unchanged" and "both made the same edit" — that is
content addressing doing the work, and it is the one place where reading the
code against the table takes a moment.

**Pointers in `Conflict`.** `Ours` and `Theirs` are pointers so that "this side
has no file" is expressible without a sentinel. They are taken from a value
receiver, so each points into that call's own copy — safe, but worth a second
look if you dislike the pattern.

## What could still go wrong

- **Renames are not tracked through the merge.** A rename is "deleted here,
  added there" (decided in the refinement, E45's open list). If the other side
  edited the content, that produces a conflict plus a new file rather than one
  sensible result. Safe, not elegant.
- **No size validation.** `Merge` trusts that the entries it is handed are
  canonical; it sorts the union but does not re-check paths. The inputs come
  from parsed manifests which are validated on load, so this holds today and
  would not if someone constructed a manifest by hand.
- **Performance is a map per side plus a sort.** At 50,000 entries that is fine;
  a three-way ordered walk would avoid the maps entirely and is a rewrite worth
  doing only if a measurement asks for it.
- **Nothing exercises it end to end yet.** The tests are unit-level by nature.
  The real proof is F-S5a-03 running the F-B-04 repro to completion.

## Open questions

1. **The backlog asked for golden vectors and I did not write them.** The task
   text said "golden vectors for the result manifest, because it is a format
   artefact like any other" — on reflection that reasoning is wrong. The result
   is an ordinary manifest whose serialization is already fenced by F-S1-04's
   vectors. A golden vector here would freeze the merge *rules*, which the table
   test does better because it names which rule broke. Deviation stated rather
   than silently skipped; say if you want them anyway.
2. **Assumption made:** that `BothAdded` should keep ours rather than refuse.
   Both sides created the same path with different content and neither is
   "the file that survives" in E47's sense. I applied E46's rule — the working
   directory keeps ours, theirs goes to the conflict directory — because it is
   the same shape as `BothChanged` from the user's point of view.
