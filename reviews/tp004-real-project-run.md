# TP-004 · Real project run — Blender editing cycles

**Date:** 2026-08-03
**Branch:** feature/tp004-real-project-run
**Status:** Ready for review

## What was done

Executed the outstanding half of the S0–S4 milestone: versioning a real Blender
project through real save cycles, rather than the synthetic corpora of TP-001
and TP-002. Measured dedup behaviour over six editing cycles in two scene sizes
and both `.blend` compression settings. Found and fixed one defect in the
ignore-file parser, and filed one documentation item.

## Spec reference

- **E18** (`.fibulaignore`, gitignore syntax) — the defect fixed here sits in
  its parser
- **E3** (chunking parameters are an efficiency question, not format) — EC-303
  is the first real-asset evidence about where that efficiency actually lands
- **CLAUDE.md § Sequence, phase 1** — "build it and use it": this is the run
  that phase 1 exists for
- **F-S4-07** (self-hosting dry run) — its real-project half, deferred by
  TP-001 and TP-002

## Context

Third test plan in a chain. TP-001 established the mechanism, TP-002 established
scale and found that E6 was unimplemented, TP-003 characterized the chunk
parameters. All three closed with the same outstanding item, which this plan
closes.

## Changed files

- `test-plans/TP-004-real-project-run.md` — the plan and its results
- `test-plans/TEST_PLANS.md` — index row for TP-004
- `client/ignore.go` — strip a leading UTF-8 BOM in `ParseIgnore`
- `client/ignore_test.go` — two regression tests: the BOM is stripped on line 1,
  and *only* on line 1
- `Backlog/B-found-in-testing.md` — F-B-03 filed
- `Backlog/index.md` — milestone status updated; the real-project half is
  discharged, the network share remains
- `reviews/tp004-real-project-run.md` — this document

## What to look at in review

**`client/ignore.go:51-57`** — the BOM is stripped only on line 1. That
asymmetry is deliberate and the second test guards it: a BOM elsewhere in the
file is an ordinary character, and stripping it would silently alter a pattern
the user wrote. If you disagree with that reading, the test is where to argue.

**The escape sequence.** Both the fix and the tests write `"﻿"` rather than
a literal BOM. A literal one is invisible in an editor, which is precisely the
property that caused the defect — reproducing it in our own source would be
poor taste.

**TP-004's EC-303, the extrapolation.** The claim "uncompressed is O(edit),
compressed is O(file)" rests on six cycles at one scene size plus two cycles at
another. The direction is well supported by the mechanism (a stateful
compressor turns a local change global), but the crossover figure of ~29 cycles
is arithmetic on two data points, and the plan says so. Push back if that
framing is too confident.

**TC-301.** Worth a look because everything else depends on it and nobody had
checked it: Blender writes byte-identical files for an unchanged scene. Had
that been false, no chunker tuning could have rescued dedup for this format.

## What could still go wrong

- **One file is not a project.** The scenes here contain everything internally.
  A studio project is a tree with external textures, linked libraries and packed
  images — a structure this run says nothing about, and the one S6 exists for.
- **Blender only.** TC-301's determinism result must not be generalized.
  Whether `.psd`, `.exr`, `.fbx` or `.uasset` write deterministically is
  unknown, and it is exactly the kind of property that differs per application.
  If one of them embeds a timestamp, dedup for that format is capped no matter
  what Fibula does.
- **The BOM fix is narrow.** `.fibulaignore` is now BOM-tolerant; nothing else
  is. If another user-authored file gets parsed later, it will have the same
  problem, and there is no shared helper to prevent that.
- **Scripted edits are not an editing session.** Six deliberate changes are not
  how an artist works. Real sessions save far more often with many
  near-identical states, which should favour Fibula more than this shows — but
  that is an expectation, not a measurement.
- **Windows only.** The run never touched Linux or macOS. The macOS NFD path
  case (E8.3) remains covered by unit tests alone.

## Open questions

1. **Should the BOM tolerance be central rather than local?** I fixed it where
   it broke. If more user-authored text files appear (a config file, a sidecar
   dependency declaration per E19), each will need the same treatment, and doing
   it three times means doing it wrong once. A shared reader is the obvious
   answer but felt like speculative generality for a single call site.
2. **Is the `.blend1` situation worth more than a documentation note?** After
   `space clear` the backup still occupies as much as the asset did. The
   behaviour is correct (E18) and the message names `--include-ignored`, but for
   this audience "clear freed half of what I expected" is predictable. Filed as
   documentation under F-B-03; it could equally be a UX decision.
3. **Assumption made:** that a BOM on line 1 is always unintended. I cannot
   construct a case where a user means to match a filename beginning with U+FEFF
   *and* puts that rule first, but the fix does make it unexpressible.
