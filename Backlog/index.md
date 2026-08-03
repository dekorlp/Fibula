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
| S5a | [archive/S5a-multi-user.md](archive/S5a-multi-user.md) | Done | Multi-user on a shared store: transfer, conflicts, locking, `sync` |
| S5b | — | Deferred | Reference server, Postgres index, `s3.Store`, auth |
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
implementing it opened a data-loss path in GC — both fixed.

**Real project measured on 2026-08-03**
([TP-004](../test-plans/TP-004-real-project-run.md)): live Blender, real save
cycles, both compressed and uncompressed. Blender writes byte-deterministically,
so dedup works at all; an uncompressed `.blend` costs about what the edit was
worth, a compressed one about half the file every time. Found one defect
(a BOM disabled the first `.fibulaignore` rule) and one documentation item
(**F-B-03**).

**Network share measured on 2026-08-03**
([TP-005](../test-plans/TP-005-network-share.md)): the store mechanics work over
real SMB — `O_EXCL` locking, rename-committed refs, GC, byte-identical restore,
nothing left behind. Loopback only, so no latency, no dropped connections, and
critically **one clock**: `breakStaleLock` compares a server-stamped `ModTime`
against the client's own time, and skew beyond 30 s either expires live locks or
never expires dead ones. That is the first thing a real-NAS run should target.

**The milestone is discharged as far as this hardware allows.** What remains is
a real NAS, plus items that were never part of it: DCC formats besides Blender,
a project *tree* with external textures and linked libraries (which is what S6
exists for), and long time spans for the retention buckets.

> **TP-005 also found the most severe defect in the project so far: F-B-04.**
> A commit took its parent from head while describing the local working
> directory, so two clients sharing a store silently overwrote each other's
> content — no concurrency required, unrelated to SMB. **Option (a) shipped on
> 2026-08-03**: such a commit is now refused. The answer to "so what do I do
> now" is S5a.

## S5b and S6 are deliberately unplanned

Both are sketched below rather than broken into tasks. Writing detailed tickets
for them now would be exactly the drawing-board planning that CLAUDE.md rejects
— phase 1 is supposed to produce those requirements, not predict them.

S5a is the exception, and for a reason: it is not a prediction. Every one of its
decisions came out of a defect a test run actually hit.

### S5b — Remote (deferred, E41)

Reference server with REST `/v1`, Postgres index (batch existence queries, refs
with CAS per E13), `s3.Store` with presigned URLs (E28), token auth with project
roles, author validation on push (E30), partial sync over the network.

Deferred because TP-005 showed a shared `fs.Store` on a network share is a
complete deployment for a studio of two to five people.

### S6 — Dependency graph (sketch)

Graph object (E21), extractor interface (E19), Blender extractor as the first
implementation, extractor provenance (E23), delete protection, impact analysis,
partial checkout.
