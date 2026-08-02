# F-S1-01 … F-S1-07 · Core — chunking, hashing, object format

**Date:** 2026-08-02
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Slice S1 complete: the deterministic foundation. BLAKE3 hashing with one
`derive_key` domain per object type and typed IDs, path normalization, the
canonical text serialization of all five object types with checked-in golden
vectors, a buzhash content-defined chunker, file object construction in a
single streaming pass, and manifest building plus diff. Ahead of the chunker a
new refinement closes the "concrete chunking parameters" item that was open —
buzhash, 64-byte window, the size bounds and the derived table (E36–E40).

Everything CLAUDE.md pins early is now decided in code. `go test -race` and
`golangci-lint` are green; coverage on the core packages is chunk 94.7 %,
object 85.0 %, manifest 98.3 %, hash 96.1 %, path 100 %.

## Spec reference

| Task | Implements |
|---|---|
| **F-S1-01** | E4 (derive_key domain per type), E34 (hex, lowercase, no prefix), typed IDs per object kind |
| **F-S1-02** | E2, E3, CLAUDE.md pinned point 2, and the new E36–E40 |
| **F-S1-03** | E8, all six rules |
| **F-S1-04** | E32, E33, E34, E35 |
| **F-S1-05** | E2, E3 — FileID over content, chunk list as an attribute |
| **F-S1-06** | E5, E7, E9, E10 |
| **F-S1-07** | E6 |

New refinement `refinements/2026-08-02-chunking-parameters.md`: **E36** buzhash
over Rabin, **E37** 64-bit hash with a 64-byte window, **E38** expected size is
`min + 2^maskBits`, **E39** the table is derived from BLAKE3 rather than checked
in, **E40** boundary vectors are a dedup fence and not a format fence. The entry
continues the global decision numbering, which `refinements/index.md` now states
explicitly.

## Context

Follows `chore: repository skeleton, tooling and license boundary (S0)` (#2).
F-S1-01 and F-S1-02 both named F-S0-06 as their dependency; the constants
catalogue from that task gained `ChunkMaskBits`, `BuzhashWindow` and
`BuzhashTableContext`, which S0 deliberately left out until the algorithm was
chosen. S2 (store) builds directly on this — see the note on file object
verification below, which affects E27.

## Changed files

**Refinement**

| File | |
|---|---|
| `refinements/2026-08-02-chunking-parameters.md` | new entry E36–E40, including the observed behaviour on constant runs |
| `refinements/index.md` | entry listed, the chunking item removed from "Open", global numbering stated |

**Core**

| File | |
|---|---|
| `errs/errs.go` | the five sentinel errors of the core, centrally per CLAUDE.md § 1 |
| `hash/hash.go` | typed IDs over an unexported digest, one hash function per object type, streaming `FileHasher` |
| `hash/parse.go` | strict parsing of the canonical rendering, one function per type |
| `hash/derive.go` | `DeriveBytes`, the BLAKE3 XOF the buzhash table is generated from; keeps BLAKE3 confined to one package |
| `path/path.go`, `path/doc.go` | normalization, validation, fold keys, collision detection, bytewise sorting |
| `object/codec.go` | the canonical encoder and the strict decoder: framing, numbers, timestamps, free-text fields |
| `object/file.go` | file object with per-chunk lengths (E35) |
| `object/manifest.go` | manifest object, sorted and collision-checked, with `ID()` |
| `object/version.go` | version object with parent list, optional expiry/graph and the message block |
| `object/graph.go` | graph object with extractor provenance (E23) — provisional, see below |
| `object/signature.go` | signature object — provisional, phase 1 does not sign |
| `chunk/buzhash.go` | derived table and the rolling hash |
| `chunk/chunk.go` | `Params`, `Splitter`, the bounded buffer |
| `chunk/build.go` | `BuildFile`: chunking, hashing and the optional sink in one pass |
| `manifest/build.go` | `Builder`: normalize, sort, reject collisions |
| `manifest/diff.go` | added / removed / changed / renamed, with deterministic rename pairing |
| `tuning/tuning.go` | mask bits, window and the table context added |

**Tests and fixtures**

| File | |
|---|---|
| `object/testdata/*.golden` | eight serialization vectors — binding, no update flag exists |
| `chunk/testdata/*.golden` | boundary vectors and the buzhash table hash — resettable via `-update-boundaries` (E40) |
| `object/golden_test.go`, `roundtrip_test.go`, `reject_test.go` | vectors, round trip, and ~40 rejection cases |
| `chunk/chunk_test.go`, `build_test.go`, `stream_test.go`, `golden_test.go` | round trip, both edit-locality properties, size distribution, memory ceiling |
| `manifest/manifest_test.go`, `chunking_test.go` | builder, diff, and the E6 measurement |
| `hash/hash_test.go`, `path/path_test.go` | domain separation, NFD, case collisions, bytewise sorting |

**Other**

`README.md` layout table extended, `Backlog/S1-core.md` moved to
`Backlog/archive/`, `Backlog/index.md` marks S1 Done. `go.mod` gained
`github.com/zeebo/blake3` and `golang.org/x/text`.

## What to look at in review

### 1. Where the GraphID lives — a genuine contradiction in the refinement

The object model says both things:

- **E21** (heading and body): "Own object, **optionally referenced by the
  manifest**" — "the manifest optionally carries a GraphID".
- **E11** (the version field table): GraphID listed as a field of the
  **version**, and the serialization example in **E33** shows a `graph` line
  inside `fibula-version v1`.

I implemented it on the **version**, on the grounds that E11 and E33 are the
sections that define the actual format while E21 discusses the concept, and
that a manifest can be shared by several versions (E12 promotion), so a graph
in the manifest could not be updated without a new manifest. **This needs a
decision and a dated addendum either way** — it is cheap now and a format break
later. If it belongs on the manifest, the change is one line in `object.Manifest`
and one in `object.Version`.

### 2. The file object cannot be verified against its own ID — matters for S2

The FileID is the hash of the file **content** (E3), not of the serialized file
object. So a file object fetched from the store under its FileID **cannot** be
checked by hashing the bytes that came back; verifying it means reassembling
its chunks and hashing those. E27 puts verification in a decorator that "every
store passes through" — that decorator therefore cannot treat file objects like
the other four types. Manifest, version, graph and signature are all hashes of
their own serialization and verify normally.

This is a consequence of E3 rather than a problem with it, but it is exactly
the kind of thing that gets discovered late in the store slice.

### 3. Sentinel errors in a central package

CLAUDE.md § 1 says "sentinel errors as typed errors in a central package", so
they live in `errs/`. The idiomatic Go alternative is per-package sentinels
(`io.EOF`, `fs.ErrNotExist`), which would put `ErrInvalidPath` in `path`. I
followed the standard as written; if the idiomatic form is preferred, moving
them back is mechanical and touches only the five declarations.

### 4. Path rules: two deliberate readings of E8

- **Backslash is rejected, not converted.** A backslash is a legal filename
  character on Linux and macOS, so converting it would corrupt those names,
  while admitting it would produce an asset that cannot be checked out on
  Windows at all. Converting the host separator belongs to the caller, which is
  where the host is known (`filepath.ToSlash`). E8.1 does not say which.
- **All C0 controls and DEL are rejected**, not only tab and LF. E8.6 forbids
  the two that break the framing; NUL cannot be passed to a filesystem call,
  CR would make a path indistinguishable from a CRLF ending, and none of them
  can occur in a Windows filename. This is a widening of E8.6 — deliberate, but
  a widening.

What is **not** rejected: `:`, `*`, `?`, `"`, `<`, `>`, `|` and the Windows
reserved names (`CON`, `NUL`, `LPT1`, …). Those are legal on Linux and macOS
and must not stop a Linux user from versioning their files. Per CLAUDE.md § 4
they belong to the **restore** path, which S3 has to implement — it is not
covered anywhere yet.

### 5. Canonical rules I had to invent because E33 does not spell them out

- The **message block** must not begin or end with a blank line, and no line in
  it may have trailing whitespace. Without a rule here, "text" and "text\n\n"
  would be two spellings of one message and therefore two VersionIDs.
- **Timestamps are truncated**, not rejected, when they carry sub-second
  precision. The alternative — failing to marshal — means one forgotten
  `Truncate` at a call site produces a version that cannot be written at all.
  This is the only lossy step in the codec.
- **Field order in the version** is manifest, parents, author, time, expiry,
  graph, message. E33 shows all of those except expiry; I placed it after time.

### 6. `chunk.boundary()` skips the first `MinSize - 64` bytes

The rolling hash is only started `BuzhashWindow` bytes before the minimum,
because bytes further back cannot influence any boundary that is allowed to be
accepted. That halves the rolling work and is the one non-obvious step in the
chunker. `TestSkippedPrefixDoesNotChangeBoundaries` proves it against a naive
reference implementation that rolls every byte — if that test ever fails, the
skip is wrong and the reference is right.

### 7. The manifest builder does not walk a filesystem

F-S1-06 says "build from a file tree", but the core "knows no filesystem
layout" (CLAUDE.md § Architecture) — walking means symlinks, ignore rules,
permissions and the host separator, all of which belong to the client. So
`manifest.Builder` takes `(path, FileID, size)` and owns everything that
decides what a manifest *is*: normalization, sorting, collision rejection. The
walker belongs to S3. I treated the layering rule as outranking the backlog
phrasing.

## What could still go wrong

- **Cross-platform determinism has not actually been observed.** F-S1-06 asks
  for the same tree to yield a byte-identical manifest on Windows and Linux.
  The mechanism is in place — byte-exact golden vectors plus NFC normalization,
  and the CI matrix runs the same vectors on both — but no run on Windows has
  happened yet, and the CI itself has still never executed. The first Windows
  run is where an NFC or path-separator assumption would surface.
- **The chunker has never seen a real asset.** Every property is tested against
  pseudo-random bytes and constant runs. Real `.blend`, `.exr` and `.wav` files
  have structure that random data does not, and the dedup rate on them is
  unmeasured. That measurement is phase 1's job and it is the input for
  revisiting E36 (FastCDC).
- **The `0x00` fixed point.** A run of zero bytes keeps the buzhash at zero, so
  such regions are cut at the minimum rather than the maximum. Documented in
  E38 and covered by a test. The chunks are identical and dedup to one, so the
  only cost is a four times longer chunk list for padded regions — but if a
  real asset format turns out to be mostly zero padding, this deserves a second
  look rather than a shrug.
- **The graph and signature objects are provisional.** `Extractor.Scope` is my
  invention: E23 requires recording "which extractors ran over which files" and
  does not say how. The extractor interface is explicitly open
  (`refinements/index.md`), and S6 will almost certainly change this shape.
  Likewise `Signature.Key` — E31 says the format should leave room, not what a
  key identifier looks like. Both are serialized and tested, neither is used.
- **Message blocks accept anything but CR and trailing whitespace**, including
  a message that is one very long line, or one containing what looks like a
  header line. Cutting on `"\nmessage\n"` is safe because every header line has
  a key and a tab in front of its value, but that argument depends on no future
  field ever being written as a bare key on its own line.
- **`go.mod` moved to Go 1.25.0.** That was forced by `golang.org/x/text@v0.40.0`,
  not chosen. CI follows via `go-version-file`, but it is a bump nobody decided
  on.
- **The memory ceiling test streams 512 MiB, not more than RAM.** CLAUDE.md
  asks for a file larger than RAM. Streaming that under the race detector costs
  minutes of CI time for no additional information — the ceiling is structural,
  the buffer is allocated once at capacity and never grown. A genuine multi-GB
  run belongs in the end-to-end suite, which does not exist yet. Even at
  512 MiB the chunk package takes about 57 s under `-race`.
- **`object` coverage is 85 %**, the lowest of the core packages. The gap is in
  error paths of the less-used object types (graph, signature), not in the
  manifest or version path.
- **Case-collision detection uses full Unicode case folding** (`x/text/cases`),
  which is neither Windows' uppercase folding nor the HFS+ table. It is a very
  close approximation and errs toward rejecting; a pair that Windows would
  collide on but Unicode folding would not is theoretically possible.

## Open questions

1. **GraphID on the version or on the manifest?** See point 1 above. This is
   the one item that should be settled before S2 stores anything, and it needs
   an addendum either way.
2. **Sentinel errors central or per package?** Point 3. I followed CLAUDE.md
   literally; say the word and they move.
3. **Is the widened path character rule wanted** (all C0 controls and DEL, not
   just tab and LF)? If yes it should become an addendum to E8.6 rather than
   living only in a code comment.
4. **Does `path` shadowing the standard library package bother you?** The
   alternatives were `fpath` or `assetpath`. I judged the clearer name worth
   the occasional import alias; it is trivial to rename now and annoying later.
5. **Is the `0x00` behaviour acceptable as it stands**, or should the rolling
   hash be seeded with a non-zero constant? Seeding would remove the fixed
   point at the price of one more magic value; I left it out deliberately.
6. **`Extractor.Scope`** — placeholder until the extractor interface is
   refined. Fine to leave provisional, or should the graph object wait for S6
   entirely rather than shipping a shape that will change?
