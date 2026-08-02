# F-S0-01 … F-S0-06 · Repository skeleton, tooling, licenses

**Date:** 2026-08-02
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Slice S0 complete: the repository now carries a Go module, the package layout
with the license boundary fixed, the lint and format gate, a CI pipeline with
the Windows/Linux matrix, a README with the phase-1 instability notice, and the
central constants catalogue split into format parameters and tuning parameters.
No domain code — every package except `format`, `tuning` and `cmd/fibula` is a
`doc.go` stating its purpose. `go build`, `go test -race` and `golangci-lint`
are green locally.

## Spec reference

- **F-S0-01** — CLAUDE.md § Architecture & layering. Public packages
  (`chunk`, `hash`, `manifest`, `graph`, `store`, `client`) at the root, not
  under `internal/`, so the client library stays importable.
- **F-S0-02** — CLAUDE.md § License. `LICENSE` (MIT) at the root; the AGPL
  `LICENSE` under `internal/server/` is deferred until that directory exists,
  with the intent recorded in `README.md` and `internal/README.md` as the task
  requires. No `CONTRIBUTING.md` — blocked on the CLA question.
- **F-S0-03** — CLAUDE.md § 1 Ground rules, § 8. `.golangci.yml` in the v2
  schema with `errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, `funlen`
  (40 lines) and `revive: file-length-limit` (400 lines).
- **F-S0-04** — CLAUDE.md § 6. Build, vet, `go test -race` on the
  ubuntu/windows matrix, lint plus an explicit format gate, `govulncheck` as
  its own job.
- **F-S0-05** — CLAUDE.md § Sequence, § Further rules. README in the framing of
  the self-contained core model (E1), visible instability notice,
  `refinements/` named as the source of truth.
- **F-S0-06** — E3, E33, CLAUDE.md § 8. Two packages rather than two sections
  of one package: `format` (never runtime configuration) and `tuning`
  (changeable, costs dedup rate rather than correctness).

## Context

Standalone task — the first code in the repository. Preceded only by
`docs: backlog with slices S0-S4 and sketches for S5/S6` (#1). S1 depends on
this slice: F-S1-01 and F-S1-02 both name F-S0-06 as their dependency, and the
derive_key contexts and chunk bounds they need are now in place.

## Changed files

| File | |
|---|---|
| `go.mod` | module `github.com/dekorlp/fibula`, Go 1.24 |
| `chunk/doc.go`, `hash/doc.go`, `manifest/doc.go`, `graph/doc.go`, `store/doc.go`, `client/doc.go` | package purpose in one sentence plus the refinement entries the package implements |
| `format/format.go` | format parameters: object version, the six object types, derive_key contexts, framing lines, canonical serialization rules, hash rendering |
| `format/format_test.go` | contexts pairwise distinct, context and header shape, serialization rules independent of the platform |
| `tuning/tuning.go` | tuning parameters: asset chunk bounds 1/2/4 MiB, manifest chunk bounds 32/64/128 KiB |
| `tuning/tuning_test.go` | bound ordering, the pinned 1–4 MB range, manifest bounds strictly below the asset bounds |
| `cmd/fibula/main.go` | CLI skeleton: `version` and `help` only, dispatch split out of `main` so it is testable |
| `cmd/fibula/main_test.go` | table-driven test over the dispatch |
| `internal/README.md` | what belongs here and why it is the AGPL side; keeps the directory tracked without inventing packages |
| `LICENSE` | MIT, copyright Dennis Köhler |
| `README.md` | what Fibula is, instability notice, repository layout, license boundary, why there is no `CONTRIBUTING.md` |
| `.golangci.yml` | the lint ruleset, including the format gate |
| `.github/workflows/ci.yml` | build/test matrix, lint, govulncheck |
| `.gitignore` | build output, coverage, `.env`, `.fibula/`, editor noise |
| `Backlog/archive/S0-repo-setup.md` | moved from `Backlog/`, slice finished |
| `Backlog/index.md` | S0 marked Done, link points at the archive |

## What to look at in review

- **`format/format.go`, the absent chunk header.** E33 says "every object
  starts with `fibula-<type> v1`", but there is deliberately no `HeaderChunk`:
  a chunk is the raw content the chunker cut (E2), and prefixing it would make
  the stored bytes differ from the file content, which would break the FileID
  falling out of the same BLAKE3 pass (E3, F-S1-05). `ContextChunk` does exist,
  because the derive_key domain separation applies to chunks as well (E4). If
  that reading of E33 is wrong, it is cheapest to correct now.
- **`tuning/tuning.go`, the concrete numbers.** `1/2/4 MiB` is my reading of
  "target size 1–4 MB" as min/avg/max in binary units. The manifest bounds
  `32/64/128 KiB` keep the same avg/2 … avg·2 ratio around the ~64 KB from E6.
  Both are decided here, not by refinement — per E3 they cost dedup rate, not
  correctness.
- **`tuning/tuning.go`, what is missing.** Window size, boundary mask and any
  Rabin polynomial are deliberately not declared. They are algorithm specific
  and the algorithm choice belongs to F-S1-02, which is required to record it
  as a refinement addendum. Adding them here would have settled that decision
  silently.
- **Two packages instead of one for F-S0-06.** "Strictly separated" is
  expressed in the import path: a value cannot drift from one group to the
  other by being moved a few lines within a file. Cost is one extra package.
- **`.golangci.yml` exclusions for `_test.go`** (`funlen`, `gosec`, `unparam`).
  Table-driven tests exceed 40 lines from the table alone; the file path and
  randomness rules do not apply to fixtures. `errcheck` deliberately stays on
  in tests.

## What could still go wrong

- **CI has never run.** The workflow is written but unverified — no push has
  triggered it. Concretely at risk: `golangci-lint fmt --diff` relies on
  `golangci-lint-action@v8` leaving the binary on `PATH` for the next step
  (documented behaviour, not tested here), and `go run
  golang.org/x/vuln/cmd/govulncheck@latest` needs proxy access from the runner.
- **`go test -race` on `windows-latest`** needs cgo and a C toolchain. The
  GitHub Windows image ships MinGW, so this normally works, but if the job
  fails on the first run the fix is either installing gcc explicitly or
  dropping `-race` on Windows while keeping it on Linux. I did not weaken it
  pre-emptively, since CLAUDE.md § 1 makes `-race` part of every test suite.
- **`golangci-lint config verify` could not be run** — the JSON schema is
  fetched from `golangci-lint.run` and the proxy in this environment returns
  403. The config was validated indirectly instead: `golangci-lint run` parses
  it without error, and a deliberately broken probe file produced exactly the
  expected findings (errcheck, gofmt, revive package-comments, revive
  exported), so the ruleset demonstrably bites. Schema-level typos in keys that
  golangci-lint silently ignores would not have been caught.
- **The Go version is `go 1.24` in `go.mod`** while the local toolchain is
  1.24.7. CI resolves the latest 1.24.x through `go-version-file` with
  `check-latest: true`. If the intent is to track the newest minor release
  instead, `go.mod` has to be bumped by hand.
- **No `internal/server/LICENSE` yet.** F-S0-02's done-criterion — every file
  unambiguously assignable — holds only because there is no AGPL file in the
  repository at all. The moment the first server file lands, that `LICENSE`
  has to land in the same commit.
- **The manifest chunk bounds are untested against reality.** F-S1-07 requires
  a benchmark showing that one changed line in a 50,000-entry manifest touches
  exactly one chunk. If 64 KiB turns out to be badly chosen, the numbers move
  there.

## Open questions

- **MIT copyright holder.** `LICENSE` says "Copyright (c) 2026 Dennis Köhler",
  taken from the git author of the existing commits. If the copyright is meant
  to sit with a company or a legal entity rather than a private individual, it
  should be corrected before external contributions arrive — it interacts with
  the still-open CLA question.
- **Module path.** `github.com/dekorlp/fibula`, derived from the repository. If
  a vanity import path (e.g. `fibula.dev/fibula`) is planned, changing it later
  is a breaking change for every embedder.
- **CLI framework.** `cmd/fibula` uses the standard library only, no Cobra or
  similar. Command names are explicitly open (CLAUDE.md), so `version` and
  `help` are placeholders, not a proposal.
- **`golangci-lint` version pin.** CI pins `v2.5.0` to match the version
  available locally. Whether the pin should be bumped deliberately or tracked
  as `latest` is a decision I did not make.
