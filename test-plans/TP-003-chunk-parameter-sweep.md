# TP-003 · Chunk parameter sweep

**Date:** 2026-08-03
**Task:** follow-up to [TP-002](TP-002-scale-run.md), open question 3 of
`reviews/scale-run-manifest-chunking.md`
**Branch:** claude/kannst-loslegen-zbfu63
**Executed:** yes, against the `chunk` package
**Result:** characterization, no defects. **No parameter was changed.**

## Why this plan exists

TP-002 established that a change of any size costs about one chunk, which makes
the chunk size the deduplication granularity. That reframes the question E36
left open: it looked like a question about the algorithm (Buzhash vs FastCDC)
and it is mostly a question about min/avg/max.

This plan measures the trade-off so that the decision has numbers under it. It
does **not** make the decision: the parameters are tuning parameters
([object model E3](../refinements/2026-08-02-object-model.md)), changing them
costs dedup rate rather than correctness, and the input that matters is a real
project rather than a synthetic corpus.

## Method

Five parameter sets, each min / expected / max, where expected is
`min + 2^maskBits` (E38). Content is deterministic pseudo-random, which is the
hard case for a chunker — it has no structure to resynchronize on, so every
figure here is a lower bound on what structured data would achieve.

Measurements A and B are averaged (24 insertion offsets, 6 repetitions with
different seeds and offsets respectively) because single samples turned out to
vary by a factor of three depending on where an edit fell relative to a
boundary.

## A · Cost of a 4 KiB insertion into a 64 MiB file

Mean of 24 insertion offsets spread across the file.

| parameters | chunks | mean chunk | mean new bytes | worst | file object |
|---|---|---|---|---|---|
| 64 KiB / 128 KiB / 256 KiB | 518 | 127 KiB | 194 KiB | 512 KiB | 37 KiB |
| 256 KiB / 512 KiB / 1 MiB | 132 | 496 KiB | 677 KiB | 1.3 MiB | 9 KiB |
| 512 KiB / 1 MiB / 2 MiB | 64 | 1.0 MiB | 1.3 MiB | 2.5 MiB | 5 KiB |
| **1 MiB / 2 MiB / 4 MiB (current)** | 34 | 1.9 MiB | **2.5 MiB** | 7.1 MiB | 2 KiB |
| 2 MiB / 4 MiB / 8 MiB | 15 | 4.3 MiB | 5.9 MiB | 12.0 MiB | 1 KiB |

**A small edit costs 1.3–1.5× the mean chunk size**, across a 34× range of
parameters. The worst single offset is about 3×. Nothing about the cost depends
on the size of the edit — a 4 KiB change and a 4 byte change cost the same.

## B · Dedup of a shared region, by region length

Two 64 MiB files sharing one contiguous region at a different offset in each.
The percentage is of the shared region that ended up in chunks common to both.

| parameters | 64 KiB | 256 KiB | 1 MiB | 4 MiB | 16 MiB |
|---|---|---|---|---|---|
| 64 KiB / 128 KiB / 256 KiB | 0 % | 37 % | 83 % | 96 % | 99 % |
| 256 KiB / 512 KiB / 1 MiB | 0 % | 0 % | 34 % | 83 % | 95 % |
| 512 KiB / 1 MiB / 2 MiB | 0 % | 0 % | 0 % | 48 % | 89 % |
| **1 MiB / 2 MiB / 4 MiB (current)** | 0 % | 0 % | **0 %** | **30 %** | **79 %** |
| 2 MiB / 4 MiB / 8 MiB | 0 % | 0 % | 0 % | 0 % | 50 % |

**Read this table along the diagonal.** It is not five different behaviours, it
is one law: a shared region is cut together with the unique data around it at
each end, so it loses roughly one chunk per boundary. What survives depends on
the **ratio** of region length to chunk size, not on either alone:

| region ÷ mean chunk | deduplicated |
|---|---|
| < 1 | 0 % |
| ~2 | ~30–37 % |
| ~8 | ~79–83 % |
| ~32 | ~95–99 % |

At the current parameters that means a repeated region needs to be **8 MiB or
more before deduplication does much with it**, and anything under 2 MiB is
invisible. That is the explanation for TP-002's most surprising number — a
corpus with 15–60 % shared content deduplicating to exactly zero, because its
shared runs were at most 544 KiB.

## C · What smaller chunks actually cost

For a 3.7 GiB project, extrapolated from a 128 MiB sample.

| parameters | chunks | file-object bytes | fsyncs | fsync time¹ | throughput |
|---|---|---|---|---|---|
| 64 KiB / 128 KiB / 256 KiB | 31,071 | 2.2 MiB | 31,071 | 79 s | 307 MiB/s |
| 256 KiB / 512 KiB / 1 MiB | 7,782 | 555 KiB | 7,782 | 20 s | 312 MiB/s |
| 512 KiB / 1 MiB / 2 MiB | 3,715 | 265 KiB | 3,715 | 9 s | 254 MiB/s |
| **1 MiB / 2 MiB / 4 MiB (current)** | 1,960 | 140 KiB | 1,960 | **5 s** | 306 MiB/s |
| 2 MiB / 4 MiB / 8 MiB | 1,111 | 79 KiB | 1,111 | 3 s | 328 MiB/s |

¹ at the 2.54 ms per fsync measured on this machine in TP-002.

**Two costs that people assume are real turn out not to be, and one that is.**

- **Throughput does not depend on chunk size.** 254–328 MiB/s across the whole
  range, and the spread is run-to-run noise rather than a trend. The rolling
  hash examines every byte regardless of where it cuts.
- **Metadata does not matter either.** Even at 64 KiB chunks the file objects
  for a 3.7 GiB project come to 2.2 MiB. That is not a constraint.
- **Object count is the whole cost.** 31,071 objects instead of 1,960 is 16×
  the fsyncs, 16× the directory entries, and — the part that will hurt — 16×
  the round trips against a network store. TP-002 already showed the import is
  dominated by disk, not CPU, so this is the axis that decides.

## What follows, and what deliberately does not

The trade-off is one-dimensional and clean: **chunk size buys deduplication
granularity and is paid for in object count.** Nothing else changes.

What this run cannot say is where on that line a game project belongs, because
that depends on how much contiguous content survives unchanged between two
saves of a real `.blend`, `.exr` or `.psd` — which is exactly the measurement
TP-001 and TP-002 could not make either. If real saves preserve multi-megabyte
runs, the current parameters are already right. If they preserve a few hundred
kilobytes at a time, the current parameters deduplicate nothing at all and no
choice of rolling hash will change that.

**No parameter was changed and no refinement was written.** Deciding this needs
a Refinement Session and real data, per CLAUDE.md.

Two properties were pinned as tests, so that the law cannot be rediscovered by
accident a third time:

- `TestAnEditCostsAboutOneChunk` — an edit costs at most 4× the mean chunk size
- `TestSharedRegionDedupsOnceItExceedsTheChunkSize` — a region of 8 mean chunks
  deduplicates at least 50 %, one of 32 at least 80 %, one shorter than a chunk
  nothing at all

At the unit-test scale (987 byte mean chunk) the same law reproduces: an edit
costs 1,329 bytes, 1.35× the mean. The relationship holds across three orders
of magnitude of chunk size, which is the main reason to trust it.

## What this run did not cover

- **Real asset formats**, again and unchanged. This plan characterizes the
  mechanism precisely and says nothing about the data.
- **Structured content.** Deterministic pseudo-random data has no internal
  repetition, so figures here are a floor. A format with long constant runs —
  uncompressed audio, padded textures — will do better, and
  `TestLongRunOfIdenticalBytesDedupesPerfectly` already covers the extreme.
- **The min/max bounds separately from the expected size.** Every set here
  scales all three together. Whether a wider max with the same expected size
  helps is untested.
- **FastCDC itself.** The comparison E36 names was not run. This plan argues
  that the size parameters dominate, not that the algorithm is irrelevant.
- **Rebuild cost.** Restoring from 31,071 chunks instead of 1,960 was not
  measured; TP-002's restore figure is for the current parameters only.

## Follow-up

No defects, nothing to file. The decision this enables belongs in a Refinement
Session with real project data. Recorded in
`reviews/chunk-parameter-sweep.md`.
