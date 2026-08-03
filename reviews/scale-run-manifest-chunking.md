# Scale run · manifest chunking, GC reachability, store contracts

**Date:** 2026-08-03
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Ran the scale half of the outstanding S0–S4 milestone acceptance test: 3,050
files and 3.7 GiB through the full loop, documented as
[TP-002](../test-plans/TP-002-scale-run.md). The run found that **E6 was never
implemented** — the manifest was stored whole, not chunked — and fixing that
uncovered a **data-loss path in garbage collection** and a **missing aliasing
contract on `ObjectStore.Put`**. All three are fixed here, plus a reporting
defect where the CLI claimed to have written bytes it had only read.

No new feature was added. Everything in this branch either implements a
refinement that was already decided or fixes something the measurement exposed.

## Spec reference

| Change | Implements |
|---|---|
| `store.PutManifest` / `store.GetManifest` | **E6** — the manifest is itself chunked, "as an ordinary file object, but with a smaller target size" |
| `Verify` no longer hashes manifest bytes | E3, E27 — consequence of E6: a ManifestKey now holds a chunk list, so the key cannot be checked against the object bytes |
| `store.ManifestChunkKeys` + `markManifest` | **CLAUDE.md invariant 8** — never GC without a complete reference check |
| `ObjectStore.Put` aliasing contract | E24, E29 — the interface must state what an implementation may do with the slice |
| `SnapshotResult.Chunked` | none; a truthfulness fix |

**No refinement was changed and no addendum was written.** E6 already specifies
the form completely, down to "without a new object type". This branch is the
code catching up to it, which is a bug fix under CLAUDE.md's own framing: *"A
deviation in code is a bug; a deviation in concept needs a refinement
addendum."*

## Context

Follows `F-S4-versions.md`, which closed S4 and recorded the milestone as
"reached, with one part of it outstanding". TP-001 named four gaps: real asset
formats, scale, a network share, long time spans. **This branch closes the
scale gap only.** The real-project run remains the first task of phase 1 and is
still outstanding — `Backlog/index.md` and the README both still say so.

## Changed files

| File | |
|---|---|
| `store/manifest.go` | new — `PutManifest`, `GetManifest`, `ManifestChunkKeys`: chunk a manifest on write, reassemble and verify it on read, and enumerate the keys it occupies |
| `store/manifest_test.go` | new — round trip across four sizes, the E6 payoff measured in the store, tampered-chunk detection, key enumeration |
| `store/verify.go` | manifests join file objects as structurally-checked-only, because a ManifestKey no longer holds hashable content |
| `store/store.go` | `Put` now states that an implementation must not retain `data` |
| `store/store_test.go` | `memStore.Put` copies, as the contract now requires; helpers for size and tamper tests; the manifest case drops out of `TestVerifyAcceptsHonestObjects` |
| `store/fs/roundtrip_test.go` | uses `PutManifest`/`GetManifest` instead of writing manifest bytes directly |
| `client/snapshot.go` | `putManifest` delegates to `store.PutManifest`; `Uploaded` becomes `Chunked` with the reason on the field |
| `client/checkout.go`, `client/dirty.go` | read manifests through `store.GetManifest` |
| `client/gc.go` | `markManifest` marks the manifest's chunks as well — the data-loss fix |
| `client/gc_manifest_test.go` | new — the data-loss test, in its own file to keep `gc_test.go` under the 400-line limit |
| `client/gc_test.go` | the new test moved out |
| `client/space_test.go` | follows the `Chunked` rename |
| `cmd/fibula/main.go`, `cmd/fibula/versions.go` | the snapshot line says "chunked" instead of "written" |
| `test-plans/TP-002-scale-run.md`, `test-plans/TEST_PLANS.md` | the run and the index |
| `Backlog/B-found-in-testing.md`, `Backlog/index.md` | F-B-01, F-B-02 |
| `README.md` | the status section reflects what is now measured |

## What to look at in review

### The GC fix is the one that matters

Everything else here is efficiency or honesty. This one is data loss, and it
was **introduced by the E6 fix in the same branch** — chunking the manifest
turns its chunks into ordinary chunk objects that a reference walk marking only
the manifest object will happily delete.

It is asserted rather than argued. With the fix reverted,
`TestCollectionKeepsTheChunksOfAChunkedManifest` fails on a store where nothing
is unreachable:

    garbage collection deleted 2 objects from a store with nothing unreachable
    Clear after GC: ... read manifest 24af5a948df9, chunk 0: object not found

I checked that deliberately rather than trusting a passing test — a
reachability test that passes for the wrong reason is worse than none.

The second line is worth noticing on its own: it is the dirty check refusing to
clear a working directory against a store it could no longer prove was
complete. Invariant 6 caught the damage that invariant 8 had already done.

The enumeration lives in `store.ManifestChunkKeys`, next to the writer, rather
than being restated in the collector. That is the point: the two cannot drift.

### `Verify` gets weaker for manifests, and where the guarantee went

Before, a manifest was fully verified on read: hash the bytes, compare to the
key. Now a ManifestKey holds a chunk list, whose hash has nothing to do with the
ManifestID — the same situation E3 creates for file objects.

The guarantee is not lost, it moved: `GetManifest` always reassembles and hashes
against the ManifestID. That is *stronger* than the old byte check, since it
also covers the chunks. It is affordable because a manifest is megabytes at
worst, which is exactly why `VerifyFileContent` stays a separate opt-in call for
assets and this does not.

Worth checking that I have described that trade honestly in the comments rather
than talked past it.

### The aliasing contract is a fix for a bug that has not happened yet

`chunk.Splitter.Next` and `chunk.Sink` both document that the slice dies at the
next call. `ObjectStore.Put` said nothing, so an implementation that queues
writes — a batching S3 backend, which is what a network store wants to be —
would have stored the following chunk's bytes under this chunk's key. Correct
hash, wrong content, no error anywhere.

`fs.Store` writes synchronously, so nothing was ever broken. I found it because
the in-memory test store retained the slice and a 50,000-entry manifest came
back corrupt. Fixed on the interface rather than by copying at the call site,
because copying every 2 MiB asset chunk on the hot path to protect a backend
that does not exist yet is the wrong trade.

### The reporting fix is a rename, not a measurement

`Chunked` is what the number always was. Reporting genuinely-new bytes needs
`Put` to say whether the write was new, and that is a store interface decision
belonging with the first network backend — filed as F-B-01, not decided here.

## What could still go wrong

- **This is a format change and it breaks existing stores.** A store written by
  the S4 build has raw manifest bytes under `ManifestKey`; this build reads them
  as a file object and fails. Phase 1 gives no compatibility guarantee and the
  README says so plainly, but a store on the maintainer's disk from yesterday
  will not open. There is no migration and I did not write one.
- **E6's payoff is much smaller than its wording suggests, below ~10,000
  assets.** Measured: 60 % of a full rewrite at 1,000 assets, 26 % at 3,050,
  3.7 % at 10,000, 2.5 % at 50,000. The refinement's "64 KB instead of 7 MB" is
  true at 50,000 and misleading at 1,000. I did not add an addendum, because
  nothing normative is wrong — but the number deserves to be known before it
  gets quoted.
- **The milestone is still not discharged.** Real asset formats are untested,
  and that gap is now sharper rather than smaller: this run can state what the
  dedup floor *is* (about one chunk, ~1.7 MiB, per changed region) but not
  whether that floor is in the right place for a `.blend`.
- **Dedup on the corpus was exactly zero on first import.** That is correct
  behaviour given a 1 MiB minimum chunk and shared runs of at most 544 KiB, but
  it means the "globally deduplicated" claim in the README has still never been
  demonstrated on anything, by any run.
- **`GetManifest` holds the whole manifest in memory.** At 50,000 assets that is
  ~5 MB, which is fine; at a million it is ~100 MB, which is not obviously fine
  and contradicts the streaming rule. Nothing enforces a ceiling, and no test
  covers a manifest that large.
- **The manifest chunk list is rewritten in full on every snapshot.** It is
  small (~70 bytes per chunk) but it is O(project size) per snapshot, so it is
  the same shape of problem E6 solved one level down. At 50,000 assets it is
  ~8 KB per snapshot, which is why I left it alone.
- **The concurrency test is four processes on one machine.** It exercised the
  ref CAS genuinely and it held, but a network filesystem — where `O_EXCL` and
  flush semantics are the actual question — was still not involved.
- **Manifest scaling above 3,050 files is measured through `store.PutManifest`,
  not through the CLI.** Generating a 50,000-file tree exceeded the disk here.
  The unit-level numbers are real; the end-to-end behaviour at that size is
  inferred.
- **GC still has no locking** and **expiry still never runs by itself** —
  unchanged from S4, both still recorded there.

## Open questions

1. **Should E6 gain a dated addendum recording the scale dependence?** Nothing
   normative is wrong, so my judgement was no — the measurement belongs in the
   test plan. But E6's rationale reads as a flat claim, and the honest version
   is "decisively true from about 10,000 assets". That is the maintainer's call.
2. **Is breaking existing stores acceptable here?** I assumed yes, on the
   README's phase-1 notice. If there is a store with real work in it already,
   this branch should not be merged before it is re-imported.
3. **Should the chunk size parameters be revisited now?** The measurement says
   the dedup floor is one chunk per changed region, so at 1–4 MiB a 4 KiB save
   costs ~1.7 MiB. E36 left the door open to FastCDC, but this run suggests the
   more consequential lever is min/avg/max, not the algorithm. I did not touch
   them: they are tuning parameters, changing them costs dedup rate rather than
   correctness, and the right input is a real project — not this corpus.
4. **Is `.fibula` at 1 MiB per 3,050 files still "a few KB"?** README and
   CLAUDE.md both use that phrase. It is linear in file count, so a
   100,000-asset project keeps ~33 MB locally. Still small, still trivially
   restorable — but the phrase is no longer accurate and I left it in place
   rather than editing a claim in CLAUDE.md unilaterally.
