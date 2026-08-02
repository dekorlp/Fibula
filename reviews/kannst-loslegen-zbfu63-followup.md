# Follow-up review on the S1 branch

**Date:** 2026-08-02
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

No task ID: this is a second pass over the S1 slice, so the branch name is the
slug per CLAUDE.md § Task review documents.

## What was done

A re-read of the merged-state and of the S1 code. It turned up one thing about
the repository state and three things in the code, all fixed here. It also
closed an open worry from the S0 review with evidence rather than with an
assumption.

## Spec reference

No new decisions. The changes serve existing ones: E8.4 (case collisions) and
the CLAUDE.md rules on data races (§ 1 Concurrency) and on avoiding needless
allocation on the hot path (§ 7 Performance, weekly review check 7).

## Context

Follows `reviews/F-S1-core-format.md`, which is left untouched — this document
records what a second look found, it does not amend the first one.

## Findings

### 1. The S1 branch is not merged anywhere — repository state, not code

`origin/develop` is at `5ba8e61`, the S0 squash merge. `origin/main` is still at
the initial commit. GitHub lists exactly two pull requests, #1 (backlog) and #2
(S0); **there is no pull request for S1 at all**, so there was nothing to merge.
The branch `claude/kannst-loslegen-zbfu63` carries the eight S1 commits and is
pushed and intact — nothing is lost, and nothing has landed.

### 2. CI is green, including the two things the S0 review was unsure about

Run 2 on `develop` after the S0 merge, all four jobs successful:

| Job | |
|---|---|
| `build & test (ubuntu-latest)` | success |
| `build & test (windows-latest)` | success — **`go test -race` works on the Windows runner**, so the doubt about needing a C toolchain there was unfounded |
| `lint & format` | success — `golangci-lint-action@v8` does leave the binary on `PATH`, so `golangci-lint fmt --diff` runs as intended |
| `govulncheck` | success |

Two open questions from `reviews/F-S0-repo-skeleton.md` are therefore settled by
evidence. What is still unverified is the S1 code on Windows: the
cross-platform determinism claim only gets tested once S1 runs through this
matrix.

### 3. A shared `cases.Caser` in `path` — latent, not live

`path.go` kept `var folder = cases.Fold()` in package scope. x/text states
plainly: *"A Caser may be stateful and should therefore not be shared between
goroutines."*

I first assumed this was an outright data race and wrote a concurrent test to
prove it. **It did not fire**, and reading the implementation shows why:
`makeFold` returns `&caseFolder{}`, whose whole definition is
`struct{ transform.NopResetter }` — no fields, no state. Sharing it is safe
today.

So the honest description is not "there was a race" but "the code depended on
an implementation detail against the library's explicit contract". That matters
because this sits in `Manifest.Validate`, on the path every manifest parse
takes, and the sync engine will parse manifests in parallel. If a future x/text
implements the `Compact` option that the fold source has a TODO for, the
dependency turns into a silent race in exactly the wrong place.

Fixed by constructing the caser per call — one small allocation per
`CheckCollisions`, reused across all paths within that call, so the cost is
nothing. The concurrency test stays as a canary: it does not fail today, but it
would catch the day x/text adds state.

### 4. `Builder.Build` serialized a manifest only to throw it away

`Build` validated by calling `Marshal` and discarding the result. On a
50,000-entry manifest that is several megabytes allocated and a full
serialization pass, purely as a validity check — and then every caller marshals
again for real.

Fixed by exporting `object.Manifest.Validate`, which was already there as an
unexported method and is genuinely useful on its own: a caller that wants to
know whether a manifest is well formed should not have to serialize it.

### 5. Paths were validated twice on every parse

`UnmarshalManifest` validated each path in `parseEntry` and then again in
`Validate`. Path validation walks the string for control characters, checks NFC
and splits segments, so on a 50,000-entry manifest that is 50,000 redundant
passes. Removed from `parseEntry`; `Validate` covers every entry once, with the
same sentinel and the same message.

### Measured effect of 4 and 5

50,000 entries, `-benchmem`:

| | before | after | |
|---|---|---|---|
| `Build` time | 163 ms | 122 ms | −25 % |
| `Build` allocated | 58.5 MB | 35.1 MB | −40 % |
| `Build` allocations | 400,329 | 250,409 | −37 % |
| `UnmarshalManifest` time | 123 ms | 117 ms | −5 % |
| `UnmarshalManifest` allocated | 46.1 MB | 43.3 MB | −6 % |
| `UnmarshalManifest` allocations | 300,159 | 200,159 | −33 % |

## Changed files

| File | |
|---|---|
| `path/path.go` | caser constructed per call instead of shared in package scope |
| `path/concurrency_test.go` | new — concurrent folding test, a canary rather than a reproduction |
| `object/manifest.go` | `validate` exported as `Validate`; redundant path validation removed from `parseEntry` |
| `manifest/build.go` | `Build` validates instead of marshaling and discarding |
| `manifest/bench_test.go` | new — the benchmarks the numbers above come from, kept as a regression guard |

## What to look at in review

- **`object.Manifest.Validate` is new exported surface**, and every exported
  symbol is a promise (CLAUDE.md § Architecture). I judged it worth it: it
  removes a real waste, and "is this manifest well formed" is a question a
  caller legitimately has. The other four object types keep their validation
  unexported — say the word if you want the symmetry instead.
- **Dropping validation from `parseEntry` changes when an invalid path is
  reported**, not whether. It now surfaces after all entries are parsed rather
  than at the offending line. Same sentinel, same message, and the rejection
  tests still pass — but if you want the error to name the line number, that
  belongs in `Validate` rather than back in `parseEntry`.

## What could still go wrong

- **The concurrency test proves nothing today.** It passes against both the old
  and the new code, because the fold caser is currently stateless. It is
  insurance against a future x/text, not a regression test for a bug that
  existed. I would rather say that plainly than let it look like a caught race.
- **The benchmarks are not run in CI**, so a regression in these numbers would
  go unnoticed until someone runs them by hand. Wiring `go test -bench` into CI
  with a comparison against a baseline is a real piece of work and I did not do
  it.
- **The measurements come from this container**, on a machine with four cores
  and under an unknown load. The ratios are meaningful, the absolute
  milliseconds are not.
- **`Build` no longer exercises the encoder**, so a manifest that validates but
  fails to marshal would now be caught later than before. There is no such case
  today — `Marshal`'s only failure path is `Validate` — but that is an
  invariant of the current code rather than something the type system enforces.

## Open questions

1. **Should I open the pull request for S1?** There is none, so there is nothing
   to merge. I have not created one because that needs an explicit go-ahead.
2. The six questions in `reviews/F-S1-core-format.md` are still open and
   unaffected by this pass — above all the GraphID placement (E21 versus E11 and
   the E33 example), which is the one worth settling before S2 writes anything
   to a store.
