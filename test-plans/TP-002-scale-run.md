# TP-002 · Scale run — 3,050 files, 3.7 GiB

**Date:** 2026-08-03
**Task:** the outstanding half of the S0–S4 milestone (see `Backlog/index.md`)
**Branch:** claude/kannst-loslegen-zbfu63
**Executed:** yes, against the built binary
**Result:** pass, with one unimplemented refinement found and fixed, one latent
data-loss path found and fixed, and one interface contract found missing and
documented

## Why this plan exists

TP-001 established that the mechanism works end to end but named four things it
could not cover: real asset formats, **scale**, a network share, and long time
spans. This plan closes the scale half. It does **not** close the real-project
half — see "What this run still did not cover".

## Scope and honesty about the corpus

The corpus is synthetic, as in TP-001, but it is not noise. A tree of random
bytes deduplicates at exactly zero and would have made every dedup number in
this plan meaningless. The generator therefore draws 15–60 % of each file from
a shared 16 MiB block pool, in runs of 32–544 KiB, so that repetition exists at
a realistic granularity.

**What that still does not model:** the internal structure of a real `.blend`,
`.exr` or `.psd`, and the way a real editing session rewrites them. Every dedup
figure below describes Fibula's behaviour on *this* corpus. It is evidence
about the mechanism, not a prediction about a studio's project.

| | |
|---|---|
| Files | 3,050 in 5 groups (textures, meshes, audio, levels, scripts) |
| Size | 3,744.5 MiB |
| Sizes | 1 KiB – 8 MiB |
| Store | a plain directory (`fs.Store`), no server, no S3, no Postgres |
| Machine | 4 cores, Intel Xeon @ 2.80 GHz; disk 82.5 MB/s sequential write, 2.54 ms per fsync |

## Test cases

| ID | Case | Result |
|---|---|---|
| TC-101 | `init` + `commit` imports 3,050 files / 3.7 GiB | pass — 91.2 s |
| TC-102 | Memory stays bounded while importing 3.7 GiB | pass — **21.4 MiB peak RSS** |
| TC-103 | `status` on a clean tree is interactive | pass — 0.07 s |
| TC-104 | Mid-file insertion re-chunks locally, not wholesale | pass — see below |
| TC-105 | Append costs about one chunk per file | pass — see below |
| TC-106 | `diff` between two versions at scale | pass — 0.02 s |
| TC-107 | `space check` re-hashes every file | pass — 2.5 s (1.45 GiB/s) |
| TC-108 | `space clear` frees the tree | pass — 2.7 s, 3.7 GiB freed |
| TC-109 | `restore` reproduces the tree | pass — 63.7 s |
| TC-110 | The restored tree is byte-identical | pass — all 3,050 files re-hashed and matched |
| TC-111 | `gc --dry-run` / `gc` at 7,116 objects | pass — 0.2 s, 0 deleted, all reachable |
| TC-112 | Manifest chunking isolates a changed line | pass, after EC-101 |
| TC-113 | GC keeps the chunks of a chunked manifest | pass, after EC-102 |
| TC-114 | Four concurrent clients on one store | pass — CAS held, no corruption |

## Measurements

### Memory — the guarantee holds with room to spare

CLAUDE.md requires a 4 GB asset to pass through an 8 GB machine. Importing
3.7 GiB peaked at **21.4 MiB RSS**, and the largest single operation (restore)
at 18.8 MiB. Nothing in the working set scales with either file size or file
count.

### Throughput — the import is disk-bound, not CPU-bound

| Stage | Rate |
|---|---|
| `commit` (read, chunk, hash, write, fsync) | 41 MiB/s |
| `restore` (read store, write tree) | 59 MiB/s |
| `space check` (re-hash only) | 1.45 GiB/s |
| Chunker in isolation (`BenchmarkSplitterThroughput`) | 386 MB/s |
| BLAKE3 in isolation (`BenchmarkHashThroughput`) | 2,314 MB/s |
| Disk, sequential write | 82.5 MB/s |

The import moves 3.7 GiB in and 3.7 GiB out on a disk that writes at 82.5 MB/s,
plus ~7,100 fsynced object writes at 2.54 ms each. That accounts for essentially
all of the 91 s. **The chunker is not the bottleneck here** — but note that it
is single-threaded on a 4-core machine, so on an NVMe disk it would become one
at ~386 MB/s. That is a finding for later, not a defect now.

The re-hash rate is worth recording separately because `CheckClear`'s own
comment predicts it: *"For 200 GB with BLAKE3 that is one to two minutes."* At
the measured 1.45 GiB/s, 200 GB takes 138 s. The estimate in the spec is
accurate.

### Dedup — the granularity floor is the chunk size

**Initial import: 4,038 chunks, 3,745.5 MiB for 3,744.5 MiB of input — no dedup
at all**, despite 15–60 % of the corpus being drawn from a shared pool.

This is not a bug, and it is the single most important number in this plan. The
asset chunker's minimum chunk size is 1 MiB, and the shared runs in the corpus
are at most 544 KiB. **A repeated region shorter than the minimum chunk size can
never be isolated into its own chunk**, so it can never be deduplicated. Every
chunk straddles unique data and is therefore unique.

The incremental cases show the same floor from the other side:

| Edit | Files touched | Content re-chunked | New chunks | New bytes |
|---|---|---|---|---|
| 4 KiB inserted at the midpoint | 10 | 28.5 MiB | 10 | 17.2 MiB (60 %) |
| 64 KiB appended | 10 | ~29 MiB | ~10 | ~12.3 MiB |

Both behave exactly as content-defined chunking promises: a 4 KiB insertion
shifts every following byte, and the chunker re-synchronizes so that only the
straddling chunk is rewritten. But "only one chunk" is still **~1.7 MiB written
for a 4 KiB change**.

**The consequence for the FastCDC question E36 left open is therefore not about
the algorithm, it is about the size parameters.** Whatever rolling hash is used,
1–4 MiB chunks put a floor of about one chunk under every change. Whether that
floor is right is exactly what a real `.blend` editing session has to answer,
and it is the measurement this run still cannot supply.

### Manifest chunking — E6's payoff is real but strongly scale-dependent

Measured by writing a manifest, changing one line and writing it again
(`TestOneChangedLineCostsOneChunkInTheStore` covers the same property as an
assertion):

| Assets | Manifest | One changed line costs | Of a full rewrite |
|---|---|---|---|
| 1,000 | 100,912 B | 60,859 B | 60.3 % |
| 3,050 | 310,012 B | 79,537 B | 25.7 % |
| 10,000 | 1,018,913 B | 37,707 B | **3.7 %** |
| 50,000 | 5,138,913 B | 126,018 B | **2.5 %** |

E6 argues its case at 50,000 assets and is right there. Below about 3,000 assets
the manifest spans so few chunks that a scattered change touches most of them
and the saving is small. In the live run at 3,050 files, ten scattered edits
cost 4 of the manifest's 7 chunks — 213,004 of 321,642 bytes, 66 %.

Recorded plainly because it is the kind of number that gets quoted as "64 KB
instead of 7 MB" and is only true at the top of the range.

### Local state

`.fibula` holds 1.0 MiB for 3,050 files: 0.4 MiB of file objects, 0.3 MiB
manifest, 0.4 MiB cache. It scales linearly with file count, so a 100,000-file
project would keep roughly 33 MiB locally. That is still small, but README and
CLAUDE.md both say "a few KB", which is true only for small projects.

### Concurrency

Four clients committed against one `fs.Store` simultaneously. Two won, two
failed with an explicit ref conflict:

    fibula: advance main: ref conflict: local/main is at 496e117a636d, expected f286d754823f

No corruption, no leftover temp files, no silent lost update — the CAS in
`store/fs/refs.go` did its job under real contention. The losers' chunks stayed
in the store as unreachable objects and were correctly spared by the grace
window on the next `gc`.

## Defects found

### EC-101 · The manifest was never chunked — **defect, fixed**

E6 requires the manifest to be stored "as an ordinary file object, but with a
smaller target size (~64 KB)". `chunk.ManifestParams()` existed since S1 and
`manifest/chunking_test.go` proved the property at the chunker — but **no
non-test code ever called it**. `client.putManifest` wrote the whole manifest as
a single object, so every auto snapshot rewrote it in full.

Found by inspecting the store after the import: one 0.3 MiB manifest object
where several 64 KiB chunks were expected.

Fixed by adding `store.PutManifest` / `store.GetManifest`, which chunk and
reassemble. The measurements above are the payoff. Consequences:

- `Verify` can no longer hash manifest bytes against the key, for the same
  reason it cannot for file objects (E3) — what sits under a `ManifestKey` is
  now a chunk list. `GetManifest` closes the gap by always reassembling and
  hashing against the ManifestID, which is affordable for a manifest and is not
  for a 4 GB asset.
- **This is a format change.** A store written before it is not readable by this
  build. Phase 1 gives no compatibility guarantee, and the README says so.

### EC-102 · Chunked manifests were a data-loss path in GC — **critical, fixed**

Directly caused by EC-101's fix, and caught before it could ship. Once the
manifest is a file object, its chunks are ordinary chunk objects in the store.
`markManifest` marked only the manifest object, so the sweep found the chunks
unreferenced and deleted them — destroying every version they described.

Confirmed rather than assumed: with the fix reverted,
`TestCollectionKeepsTheChunksOfAChunkedManifest` fails with

    garbage collection deleted 2 objects from a store with nothing unreachable
    Clear after GC: ... read manifest 24af5a948df9, chunk 0: object not found

The second line is invariant 6 working correctly on a store that had already
been damaged: the dirty check refused to clear a working directory it could no
longer prove was recoverable.

Fixed by enumerating the keys through `store.ManifestChunkKeys`, which lives
next to the writer so that the collector cannot drift from it.

### EC-103 · `ObjectStore.Put` had no aliasing contract — **latent defect, documented**

Found because the in-memory test store retained the slice it was handed and the
50,000-entry manifest round trip came back corrupt.

`chunk.Splitter.Next` and `chunk.Sink` both document that the slice is only
valid until the next call. `ObjectStore.Put` documented nothing, so an
implementation that queues writes — which is exactly what a batching or
asynchronous network backend wants to be — would store whatever the splitter's
reused buffer held by the time the write ran. **Silent corruption under a
correct hash**, and nothing in the interface warned against it.

`fs.Store` writes synchronously and was never affected, so this was latent, not
live. Fixed by stating the obligation on the interface: an implementation must
not retain `data`; queue a copy. Recorded here because the first backend that
would have hit it does not exist yet, and by then the cause would have been very
hard to find.

### EC-104 · Snapshot reported bytes it had not written — **defect, fixed**

`SnapshotResult.Uploaded` counted every chunk offered to `Put`, including ones
the store already had. The CLI printed it as "written":

    3050 files, 10 read and chunked, 28.5 MiB written

while the store actually grew by 17.2 MiB — a 40 % overstatement. Harmless
locally, misleading the moment "written" means "uploaded".

Reporting the genuinely new bytes needs `Put` to say whether the write was new,
which is a store interface decision belonging with the first network backend.
The field is therefore renamed to `Chunked` and the message now says what the
number measures. Filed as **F-B-01** for the real fix.

### EC-105 · `space check` prints every path — **not fixed, filed**

3,050 lines of output on a tree this size, and 100,000 on a real project. Filed
as **F-B-02**.

### EC-106 · A failed commit orphans its chunks — **observed, correct behaviour**

The two clients that lost the CAS race had already written their chunks. Those
stayed in the store as unreachable objects, correctly spared by the grace window
and collectable afterwards. Recorded because it is the expected shape of a
contended store, not a defect.

## What this run still did not cover

- **Real asset formats.** Unchanged from TP-001, and now the most conspicuous
  gap: this plan can say what the dedup floor *is*, but not whether it is in the
  right place for a `.blend`. **The milestone is still not fully discharged.**
- **A NAS share.** The store was a local directory. `store/fs/refs.go`'s lock
  file and `Put`'s flush are written for network filesystems and have still
  never run on one — and TC-114 shows contention is exactly where they matter.
- **Long time spans.** Every snapshot in this run was minutes old. The daily and
  weekly retention buckets still have only unit-test coverage.
- **Project scale beyond 3,050 files.** The manifest scaling table above is
  measured through `store.PutManifest` rather than through the CLI at 50,000
  files, because generating that tree exceeds the disk available here.
- **A slow store under GC.** The grace window was tested with an advanced
  clock, never against an upload slow enough to actually race it.

## Follow-up

Four defects fixed in this branch (EC-101 to EC-104); two items filed
(**F-B-01**, **F-B-02**). Details and open questions in
`reviews/scale-run-manifest-chunking.md`.
