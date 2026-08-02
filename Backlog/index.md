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
| S4 | [S4-versions.md](S4-versions.md) | Open | Version graph, refs, checkout, expiry, GC |
| S5 | — | Sketch | Reference server, Postgres index, `s3.Store`, auth |
| S6 | — | Sketch | Dependency graph, extractors, partial checkout |

## Milestone: self-hosting after S4

**With S0–S4 complete, Fibula is usable for its own purpose.** An `fs.Store` on
a second disk or a NAS share is a fully valid deployment (CLAUDE.md, single
binary path) — no server, no S3, no Postgres required.

That is the point where phase 1 actually begins: versioning real Blender
projects and discovering the requirements that no amount of planning would have
produced.

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
