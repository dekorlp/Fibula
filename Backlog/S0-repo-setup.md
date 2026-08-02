# S0 · Repository skeleton

Everything needed before the first line of domain code. Small, mechanical, but
it fixes the license boundary — and that one is expensive to correct later.

---

### F-S0-01 · Go module and package layout

**Spec:** CLAUDE.md § Architecture & layering
**Depends on:** —

- `go.mod` with the current stable Go version
- Directory skeleton: `chunk/`, `hash/`, `manifest/`, `graph/`, `store/`,
  `client/`, `cmd/fibula/`, `internal/`
- Public packages at the root, **not** under `internal/` — the client library
  must be importable
- Empty `doc.go` per package stating its purpose in one sentence

---

### F-S0-02 · Licenses and the license boundary

**Spec:** CLAUDE.md § License
**Depends on:** F-S0-01

- `LICENSE` (MIT) at the repository root
- `internal/server/LICENSE` (AGPL-3.0) once that directory exists — until then
  record the intent in the README
- `README.md` explains the boundary: core, store, client, CLI are MIT; the
  reference server is AGPL
- **No `CONTRIBUTING.md`** — blocked until the CLA question is settled
  (CLAUDE.md § Open decisions)

**Done when:** every existing file is unambiguously assignable to one of the two
sides.

---

### F-S0-03 · Linting and formatting gate

**Spec:** CLAUDE.md § Ground rules
**Depends on:** F-S0-01

- `.golangci.yml` with a ruleset matching the coding standards (at minimum
  `errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, function length,
  file length)
- `gofmt` + `goimports` enforced in CI, not merely recommended

---

### F-S0-04 · CI pipeline

**Spec:** CLAUDE.md § 6 Testing
**Depends on:** F-S0-03

- Build, vet, lint, `go test -race`
- **Matrix on Windows and Linux** — required for the cross-platform determinism
  tests from S1 (E8), not optional
- `govulncheck` as a separate job

---

### F-S0-05 · README with instability notice

**Spec:** CLAUDE.md § Further rules, § Sequence
**Depends on:** F-S0-02

- What Fibula is, in the framing of the current core model: a self-contained VCS
  for binary assets, **no Git dependency**
- Visible notice: phase 1, explicitly unstable, no compatibility guarantees
- Link to `refinements/` as the source of truth

---

### F-S0-06 · Central constants catalogue

**Spec:** E3, E33, CLAUDE.md § 8 Code quality
**Depends on:** F-S0-01

Two strictly separated groups, each with a refinement reference in the comment:

- **Format parameters** — object versions, `derive_key` contexts, serialization
  rules. Never runtime configuration.
- **Tuning parameters** — chunk min/avg/max, window size, mask, manifest chunk
  target size (~64 KB per E6). Changeable; they cost dedup rate, not
  correctness.

**Done when:** no magic number for either group exists anywhere else in the
codebase.
