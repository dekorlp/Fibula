# Fibula

Version control for binary assets (3D models, textures, audio) — the half Git
never handled. Named after the Roman brooch: what belongs together is held
together. Written in Go.

**Target audience:** game developers, above all small studios and solo devs for
whom Perforce is too expensive and too much to operate. Secondary: European
studios with sovereignty requirements (self-hosted, EU, open standard).

**Core model — fully self-contained (decided 2026-08-02):**

Fibula is a version control system for binary data in its own right, not a layer
on top of another one. **No dependency on Git, in any layer** — neither
technical nor conceptual, neither mandatory nor optional. A project can be
versioned completely with Fibula alone.

- Assets → Fibula store. Content-addressed, chunk-based, globally deduplicated.
- State → manifest pointing at chunk/blob hashes.
- History → Fibula's own version graph.

Comparing against Git remains useful for **orientation** (object model,
content addressing, separation of core and hosting) and as positioning towards
the target audience. It is never an integration promise: there is no Git mode,
no manifest export to Git, no special paths.

Analogy for the cut: Git core vs. Gitea/Forgejo. The core knows nothing about
auth, UI or multi-tenancy.

## Sequence — where we stand

The project is **specification-driven, but not spec-first**. That is the
deliberate difference from the sister projects:

1. **Phase 1 (now): build it and use it.** Explicitly unstable, *no*
   compatibility guarantees. Real Blender projects get versioned with it — that
   is how the actual requirements surface.
2. **Phase 2: `PROTOCOL.md` as a distillation** of what worked — not drawn up in
   advance. Readable and independently implementable. Compatibility guarantees
   start here.
3. **Phase 3: backend adapters.** Connecting further storage systems as a Fibula
   backend, each in its own repo and built against the then-stable spec — never
   against internals.

**Consequence for the work:** do not specify a wire protocol before the object
model layer exists and has been used. Once chunk store, manifest and dependency
graph are right, the wire protocol is nearly a formality — that is how Git did
it too.

## Specification — the source of truth

Architectural decisions live as refinement entries in
[refinements/](refinements/) — overview in
[refinements/index.md](refinements/index.md). If a decision changes a normative
guarantee (format, data model, invariant), the affected entry gets a **dated
addendum** — never a silent reinterpretation.

### Pinned early (changing these breaks the format)

These decisions are expensive to reverse. A deviation in code is a bug; a
deviation in concept needs a refinement addendum before any code is written:

1. **Hash: BLAKE3.** Fast, parallelizable, its tree structure fits chunking.
2. **Content-defined chunking** (rolling hash, Rabin/Buzhash), target size
   **1–4 MB**. No fixed blocking — otherwise a single insertion shifts every
   boundary. Per [object model E3](refinements/2026-08-02-object-model.md) the
   concrete parameters (min/avg/max, window, mask/polynomial) are **not** part
   of the format but an efficiency question: object identity depends on content,
   not on where it was cut. Keep them centralized as constants anyway — deviation
   costs dedup rate, not correctness.
3. **Determinism:** manifest and object serialization are byte-identical across
   platforms, architectures and versions — Windows/macOS/Linux in particular.
   For chunk boundaries determinism is highly desirable (dedup) but not a
   correctness condition.
4. **Manifest structure:** stable, canonical serialization (sorted entries,
   normalized paths — `/` as separator, defined Unicode normalization, explicit
   case handling). Two identical working directories produce byte-identical
   manifests.
5. **Deduplication is global**, not merely between versions of one file — across
   projects.

### Invariants (always in force, regardless of phase)

6. **No data loss.** Before any local deletion (`space clear`) a **dirty check**:
   every local file must demonstrably have its hash in the store. This is the
   trust question of the entire feature — no best effort, no heuristics, abort
   when in doubt.
7. **Content addressing is verified**, not assumed: whatever comes out of the
   store is checked against its hash before it counts as valid.
8. **Never GC/prune without a complete reference check.** A chunk is deleted only
   when no reachable manifest demonstrably references it.
9. **Offline-capable by design**, not retrofitted as a feature. Local operations
   (snapshot, diff, status) work without a server.

### Deliberately open / revisable

Wire protocol, index technology, auth model, server deployment, space manager UX,
CLI command names. Decide pragmatically here and correct later — nothing is set
in stone.

### Settled architecture

- **Storage backend as an interface.** S3 is just one implementation; `fs.Store`
  for "one binary, one directory, done" is an equal first-class citizen.
- **Partial sync:** the client fetches the manifest, checks which chunks exist
  locally, downloads only the difference. This is where Git fails structurally.
- **Existence queries via the index** (Postgres or similar), not via individual
  HEAD requests against S3.
- **Presigned URLs for transfers**, so the server never becomes the throughput
  bottleneck.
- **Two version levels, deliberately separated:** *auto snapshots* (time- or
  space-triggered, uncommented, expiring automatically — the safety net) vs.
  *deliberate versions* (named, commented, permanent). Perforce does not make
  this distinction — part of the usual criticism.
- **Space manager:** reserve → work → versioned automatically → "clear space" →
  gone locally, present in the store. The manifest state stays local (a few KB),
  which keeps restore trivial.
- **Dependency graph between assets** (level → mesh → texture). No established
  tool knows that a level breaks when a texture is replaced — that is the
  differentiator against Perforce.
- **The reference server must be production-grade**, not a demo toy.

### Repos

- **`fibula` (public, this repo):** object model, chunking, manifest format, wire
  protocol, reference server, CLI, client library.
- **Later:** backend adapters for further storage systems — separate repos,
  phase 3, strictly against the public spec.

### License (decided 2026-08-02)

**Client MIT, server AGPL-3.0.** Studios should be able to embed the client
library into their pipeline without a second thought; the reference server stays
copyleft.

A hard layering rule follows from this: **everything the client needs is MIT** —
core (`chunk`, `hash`, `manifest`, `graph`), the store abstraction including the
`fs` and `s3` implementations, `client` and the CLI. AGPL applies only to the
reference server (`internal/server`, `cmd/fibula-server`). **MIT code must never
import AGPL code** — the rule "dependencies point inwards" is now also the
license boundary, and the server sits on the outside.

Implementation: `LICENSE` (MIT) at the root, a separate `LICENSE` (AGPL-3.0) in
the server directory, the boundary explained in `README.md`. Every new file must
be unambiguously assignable to one side — when in doubt it belongs to MIT, since
moving from AGPL to MIT later is expensive while the reverse is not.

## Rules

- **"Workshop Session"** or **"Refinement Session"** at the start of a message
  (German: "Workshop Session", "Refinement Session"): we plan and discuss — no
  code. The result is an entry or addendum in `refinements/`. Method: sequential,
  brainstorm with a clear recommendation first, then spec text.
- Before every coding session: read [Backlog/index.md](Backlog/index.md) and
  [refinements/index.md](refinements/index.md).
- **Language: the repository is English throughout** — documentation,
  refinements, backlog, reviews, test plans, source comments, test descriptions,
  commit messages. Conversation with the maintainer may be German; anything
  written to a file is English. Structured output preferred.

## Open decisions (do not settle unilaterally)

- **CLA** needed before external contributions arrive — otherwise later
  commercial exploitation is blocked, particularly on the AGPL side. → No
  `CONTRIBUTING.md` inviting contributions before that is in place.

## Backlog

The project backlog lives in [Backlog/](Backlog/), overview in
[Backlog/index.md](Backlog/index.md). Completed items move to
`Backlog/archive/`.

## Task review documents

After finishing a task, **before committing**, create a review document in the
`reviews/` directory at the project root.

### Filename

`reviews/<TASK-ID>-<short-slug>.md` — example: `reviews/F-S1-02-chunker.md`. If
no task ID is known, use the branch name as the slug.

### Template

```markdown
# <TASK-ID> · <task title>

**Date:** <YYYY-MM-DD>
**Branch:** <current git branch>
**Status:** Ready for review

## What was done
<2-4 sentences: what was implemented, which problem was solved>

## Spec reference
<Which refinement decisions / pinned points / invariants does this task
implement? Justify deviations explicitly>

## Context
<References to previous reviews / related tasks, or "Standalone task">

## Changed files
<every changed file with a one-line explanation>

## What to look at in review
<specific lines, edge cases, decisions>

## What could still go wrong
<honest assessment: what is NOT covered, what was not tested>

## Open questions
<state assumptions explicitly; if none: "None">
```

### Rules

- **ALWAYS** create the review document before committing
- Be honest under "What could still go wrong" — do not hide uncertainties
- "Open questions" is mandatory whenever any assumption was made
- Never delete or modify existing review documents

## Test plans

Test plans live in `test-plans/` (directory plus `TEST_PLANS.md` overview are
created with the first plan). **"Test plan"** or **"Test"** at the start of a
message (German: "Testplan erstellen", "Test") means: create a test plan and
optionally execute it. Test cases as TC-001…, edge cases as EC-001…; bugs found
go straight into the backlog with a reference to the test plan. Never modify
existing test plans — retests get a new plan.

End-to-end tests run against the local compose environment (reference server +
PostgreSQL index + S3-compatible fake, e.g. MinIO) **and** against the
`fs.Store` backend without a server. Mandatory before every release: round trip
with real asset files (Blender, textures), the partial-sync case, and the
space-clear dirty check.

---

# Coding Standards & Rules

## 1. Go (core, server, CLI)

### Ground rules

- Current stable Go version; `gofmt` + `goimports` are non-negotiable
- `golangci-lint` as a CI gate (configuration in the repo)
- Source comments in **English**
- No `panic()` outside `main`/program startup — errors are returned
- Error wrapping with `fmt.Errorf("...: %w", err)`; sentinel errors as typed
  errors in a central package
- `context.Context` is always the first parameter of exported functions doing IO
- Never start a goroutine without a lifecycle: only with a clear shutdown path
  (`context` cancellation), no bare `go func()` in library code — applies
  especially to parallel chunk uploads, worker pools and the file watcher
- Concurrency: data races are bugs — `go test -race` is part of every test suite
- Structured logs (JSON, `log/slog`); never asset contents, credentials,
  presigned URLs or tokens in logs

### Architecture & layering

Dependencies point inwards, exclusively:

1. **Core** (`chunk`, `hash`, `manifest`, `graph`): deterministic, pure. Knows
   **no** network, **no** database, **no** filesystem layout — operates on
   `io.Reader`/`io.Writer`. The format lives here.
2. **Store abstraction** (`store`): chunk/blob store and index as interfaces;
   implementations (`store/fs`, `store/s3`, index) interchangeable behind them.
   No code outside knows S3 details.
3. **Client** (`client`): sync engine, local cache, space manager.
4. **Server** (`internal/server`) and **CLI** (`cmd/fibula`): thin, orchestration
   only — no format logic.

- **Choose the public surface deliberately:** whatever studios embed into their
  pipeline (core, store interfaces, client library) lives in importable
  packages — **not** under `internal/`. Server internals belong in `internal/`.
  Every new exported symbol is a promise; when in doubt, leave it unexported.
- No circular package dependencies
- Large files are **streamed**, never held in RAM in full — neither during
  chunking nor during upload/download. A 4 GB asset must pass through on an 8 GB
  machine.

### Naming

- Packages: short, lowercase, meaningful (`chunk`, `manifest`, `store`,
  `client`, `graph`)
- Exported names: Go conventions (`PascalCase`, no getter prefixes)
- Files: `snake_case.go`, tests `*_test.go`

## 2. Persistence

### Store (chunks/blobs)

- Strictly content-addressed storage; writes are **atomic** (write to temp,
  verify, then rename/commit) — no half chunk ever becomes visible
- Writing is idempotent: writing the same hash twice is not an error
- Never reimplement store semantics in callers — they live behind the interface

### Index (PostgreSQL)

- Minimum **3rd normal form**; exceptions only with justification in the review
  document
- Tables `snake_case` plural, columns `snake_case`, FKs `{table}_id`, indexes
  `idx_{table}_{column}`
- Every table: `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`, `created_at`,
  `updated_at` (justified exceptions in the review document). Hashes are natural
  keys with a unique constraint, not the primary key.
- FKs always with `ON DELETE` behaviour; no nullable columns without
  justification
- Every schema change as a versioned migration in `migrations/` (no manual
  ALTER)
- Indexes on all FKs and frequently filtered columns — in particular the batch
  existence query (chunk hash set) and manifest/version listings
- For all content-addressed objects the index is a **cache over the store**, not
  a second source of truth — reconstructible from the store, divergence is a bug.
- **Exception: refs.** They are mutable state, do not live in the blob store and
  are not reconstructible from it — the index is authoritative for them
  ([object model E13](refinements/2026-08-02-object-model.md)). Accordingly they
  need atomic compare-and-swap and backup consideration, which the cache part of
  the index does not.

## 3. API design (reference server)

- REST, plural resources, correct HTTP verbs and status codes
- Versioning in the path: `/v1`
- Batch endpoints for chunk existence and URL issuance — **never** one request
  per chunk
- All endpoints authenticated (exception: `health`); key comparison in constant
  time
- Uniform error format (machine-readable code + message); `X-Request-ID`
  correlation
- Pagination on all list endpoints (cursor-based)
- Rate limiting on auth-sensitive paths
- In phase 1 the HTTP API is **downstream**, not contract-first: it follows the
  object model. `api/openapi.yaml` is maintained once the endpoints exist and
  becomes the normative source with phase 2.

## 4. Security

- No default credentials, anywhere
- Secrets exclusively via environment variables or KMS; never commit `.env`
- All credential comparisons in constant time
- Validate inputs; SQL only parameterized
- **Path safety on restore:** manifest paths are untrusted input — no `..`, no
  absolute paths, no symlink escapes, no writing outside the target directory.
  Consider Windows reserved names and drive prefixes.
- Presigned URLs: short-lived, minimally scoped, never logged, never persisted
- The server does not blindly trust client-supplied hashes — wherever
  verification does not happen, the trust boundary is documented in the code
- `govulncheck` + dependency audit on every release

## 5. Git & versioning

### Branching (early phase)

**Trunk-based** until the first release: `develop` as the trunk plus short-lived
`feature/<slug>` branches, squash-merged via PR. With the first release candidate
we switch to GitFlow (`develop`, `release/*`, `hotfix/*`, tags only on `main`).

### Commits (Conventional Commits)

```
feat: content-defined chunker with buzhash rolling window
fix: manifest path normalization on windows
spec: refinement addendum — chunk size bounds
docs: README object model overview
refactor: extract store interface from s3 backend
test: golden vectors for chunk boundary determinism
chore: golangci-lint config
```

`spec:` is the commit type for changes to refinements, to `PROTOCOL.md` or to
`api/openapi.yaml`. An addendum to a decided refinement is always its own commit
with a justification.

### Further rules

- No direct pushes to the trunk (branch protection)
- PR descriptions reference the task ID and the affected
  refinements/invariants
- CHANGELOG.md in Keep-a-Changelog format from the first release onwards
- While phase 1 lasts: the README carries a visible instability notice

## 6. Testing

- Unit tests: table-driven, Arrange/Act/Assert, descriptions in English
- `go test -race` everywhere; minimum coverage **80 %** on core packages
  (`chunk`, `manifest`, `store`, `client` sync)
- No tests against external services — PostgreSQL via testcontainer/compose, S3
  via a local fake (MinIO), `fs.Store` directly
- Integration tests against the compose environment (`docker-compose.dev.yml`)
  as a CI job
- Critical invariants deserve targeted, named tests — not just implicit
  coverage:
  - **Golden vectors** for object serialization (checked-in fixtures); if they
    fail it is a format break, not a test update
  - **Golden vectors for chunk boundaries** — if they fail it is not a format
    break (E3) but an unintended dedup regression: find the cause, then reset
    them deliberately
  - **Property tests** for the chunker: insertion/deletion at the start of a file
    may only shift boundaries locally; the round trip `chunk → reassemble` is
    byte-identical
  - **Cross-platform determinism**: the manifest for the same tree is identical
    on Windows and Linux (CI on both)
  - **Dirty check before space clear**, including the failure cases (store
    unreachable, hash missing, file changed since the snapshot)
  - **Corruption**: a tampered chunk in the store is detected on read
  - **Abort/crash mid-upload** leaves no half-visible chunk behind
  - **Large files** (> RAM) pass through, with a memory ceiling asserted in the
    test

## 7. Docker & deployment

- Multi-stage builds, small images, official base images
- No secrets in images or compose files
- Health checks for every service; set resource limits
- Volumes for all persistent data (PostgreSQL, `fs.Store`)
- The compose dev setup is the reference for local environments
- The single-binary path ("one binary, one directory, done") always stays
  runnable — Docker is an option, not a prerequisite

## 8. Code quality & review

- Functions at most **40 lines** (justify exceptions), files at most **400
  lines**
- Never commit commented-out blocks of code
- TODOs always with a task reference (`// TODO: F-S1-03`)
- Magic numbers/strings as named constants, centralized as a configuration
  catalogue with a refinement reference in the comment. Two classes, strictly
  separated:
  - **Format parameters** (object versions, `derive_key` contexts, serialization
    rules) — never runtime configuration
  - **Tuning parameters** (chunk min/avg/max, window size, mask) — changeable,
    they cost dedup rate rather than correctness
    ([object model E3](refinements/2026-08-02-object-model.md))
- Code is readable without additional explanation; error handling is complete

---

# Weekly automated reviews

**"Weekly Review"** at the start of a message (German: "Wöchentliches Review"):
run the check, write the result to
`weekly-reviews/YYYY-MM-DD-weekly-review.md`.

### Checks

1. **Data-loss paths:** is the space-clear dirty check airtight? GC/pruning
   without a full reference check? Local deletion without confirmed store state?
   Non-atomic writes?
2. **Format & determinism:** chunking/hash parameters unchanged and centralized?
   Manifest serialization canonical? Path normalization cross-platform? Golden
   vectors still green and not "adjusted"?
3. **Resources & leaks:** goroutine leaks (missing context cancellation in
   workers/watcher), unclosed readers/writers/HTTP bodies, PG connection pool,
   unbounded worker fan-outs
4. **Security:** input validation, path traversal/symlinks on restore, secrets or
   presigned URLs in logs/errors, constant-time comparisons, missing
   AuthN/AuthZ on new endpoints, rate limits
5. **Store & index:** index divergence against the store, missing indexes, N+1
   (existence query per chunk instead of batched), transaction boundaries,
   migration drift against the refinement data model
6. **Layering:** does the core suddenly know about network/DB/filesystem? S3
   details outside `store/s3`? Unintentionally exported API surface?
7. **Performance:** assets held in RAM unnecessarily, missing streaming, hot-path
   allocations in the rolling hash, connection reuse, unnecessary round trips
   during sync
8. **Docker/deploy:** health checks, limits, volumes, image currency,
   single-binary path still runnable
9. **Code quality:** `interface{}`/`any` without need, duplicated logic, dead
   configuration fields, refinement references in comments still correct

### Output format

```markdown
# Weekly Review — YYYY-MM-DD

## Summary

## Issues Found

### Critical
- ...

### Warning
- ...

### Info / Suggestions
- ...

## Recommended Actions
- [ ] ...
```
