# Backlog

Work items for Fibula, grouped into slices. Each slice is a file; a finished
slice moves to `Backlog/archive/` as a whole.

Read this together with [refinements/index.md](../refinements/index.md) before
every coding session.

## Conventions

- Task IDs: `F-S<slice>-<nn>`, e.g. `F-S1-02`. IDs are permanent — never reuse
  one, even after a task is dropped.
- Every task names the refinement decisions it implements (`E3`, `E17`, …).
  A task without a spec reference is a warning sign: either the refinement is
  missing or the task is not needed.
- Definition of done includes tests. The invariant tests listed in CLAUDE.md
  (§6 Testing) belong to the task that creates the code path, not to a separate
  "write tests" task.
- Bugs found during testing go straight in here with a reference to the test
  plan.

## Slices

| Slice | File | Status | Goal |
|---|---|---|---|
| S0 | [archive/S0-repo-setup.md](archive/S0-repo-setup.md) | Done | Repository skeleton, tooling, licenses |
| S1 | [archive/S1-core.md](archive/S1-core.md) | Done | Chunking, hashing, object serialization — the format |
| S2 | [archive/S2-store.md](archive/S2-store.md) | Done | Store interfaces + `fs.Store`, verification |
| S3 | [archive/S3-workspace.md](archive/S3-workspace.md) | Done | Local state, space, snapshots, dirty check |
| S4 | [archive/S4-versions.md](archive/S4-versions.md) | Done | Version graph, refs, checkout, expiry, GC |
| S5 | — | Sketch | Reference server, Postgres index, `s3.Store`, auth |
| S6 | — | Sketch | Dependency graph, extractors, partial checkout |
| B | [B-found-in-testing.md](B-found-in-testing.md) | Open | Defects found in testing, outside any slice |

## Milestone: self-hosting after S4

**With S0–S4 complete, Fibula is usable for its own purpose.** An `fs.Store` on
a second disk or a NAS share is a fully valid deployment (CLAUDE.md, single
binary path) — no server, no S3, no Postgres required.

That is the point where phase 1 actually begins: versioning real Blender
projects and discovering the requirements that no amount of planning would have
produced.

**Reached on 2026-08-02, with one part of it outstanding.** The mechanism works
end to end ([TP-001](../test-plans/TP-001-self-hosting-dry-run.md)), but the
acceptance run used synthetic assets rather than a real Blender project. The
dedup rate on real asset formats, the behaviour at project scale and the
behaviour on a network share are unmeasured. That run is the first task of
phase 1 proper, and its findings are the input for S5 and S6.

**Scale measured on 2026-08-03** ([TP-002](../test-plans/TP-002-scale-run.md)):
3,050 files and 3.7 GiB through the full loop, with memory bounded at 21 MiB and
a byte-identical restore. It found that E6 had never been implemented and that
implementing it opened a data-loss path in GC — both fixed. **Two gaps remain
from TP-001's list and one is now sharper:** real asset formats (the run can say
the dedup floor is about one chunk per changed region, but not whether that
floor is right for a `.blend`), and a network share. The real-project run is
still the first task of phase 1.

## S5 and S6 are deliberately unplanned

Both are sketched below rather than broken into tasks. Writing detailed tickets
for them now would be exactly the drawing-board planning that CLAUDE.md rejects
— phase 1 is supposed to produce those requirements, not predict them.

### S5 — Remote (sketch)

Reference server with REST `/v1`, Postgres index (batch existence queries, refs
with CAS per E13), `s3.Store` with presigned URLs (E28), token auth with project
roles, author validation on push (E30), partial sync over the network.

### S6 — Dependency graph (sketch)

Graph object (E21), extractor interface (E19), Blender extractor as the first
implementation, extractor provenance (E23), delete protection, impact analysis,
partial checkout.
