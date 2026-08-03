# Chunk parameter sweep · F-B-02

**Date:** 2026-08-03
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Two things, both small in code and one of them the answer to the question I
left open last time.

1. **Measured what the chunking parameters cost and buy**
   ([TP-003](../test-plans/TP-003-chunk-parameter-sweep.md)), and pinned the two
   laws that came out of it as tests in `chunk/`. **No parameter was changed.**
2. **Fixed F-B-02**: `space check` no longer prints one line per asset.

No behaviour changed apart from the CLI output. The value here is the two new
tests and the table.

## Spec reference

| Change | Implements |
|---|---|
| `TestAnEditCostsAboutOneChunk` | **E3, E37, E38** — pins the property that justifies content-defined chunking at all, in bytes rather than boundary counts |
| `TestSharedRegionDedupsOnceItExceedsTheChunkSize` | **E5** (global deduplication) — states the granularity limit that TP-002 discovered by accident |
| `space check --verbose` | F-B-02; CLI command names are deliberately open (CLAUDE.md) |

**No refinement was written and no addendum was added.** TP-003 is input for a
Refinement Session, not a substitute for one — the parameters are tuning
parameters and the decision needs real project data.

## Context

Follows `reviews/scale-run-manifest-chunking.md`, whose open question 3 asked
whether the chunk size parameters should be revisited. This answers the half
that can be answered without a Blender project: what the trade-off curve looks
like. It does not answer where on that curve a game project belongs.

## Changed files

| File | |
|---|---|
| `chunk/cost_test.go` | new — the two laws as tests, plus `newBytes`/`meanChunk` helpers |
| `cmd/fibula/main.go` | `space check` summarizes the safe list, `--verbose` restores it; usage text |
| `cmd/fibula/main_test.go` | an unknown `space check` flag is a usage error |
| `test-plans/TP-003-chunk-parameter-sweep.md` | the measurement |
| `test-plans/TEST_PLANS.md` | the index |
| `Backlog/B-found-in-testing.md` | F-B-02 marked done |

## What to look at in review

### The finding, in one sentence

**Deduplication depends on the ratio of shared-region length to chunk size, and
on nothing else.** A region shorter than one chunk deduplicates to zero however
often it repeats; at 8× the chunk size it is ~80 %, at 32× ~97 %. The same law
seen from the other side is that an edit costs 1.3–1.5× the mean chunk,
regardless of how small the edit is.

At the current 1 / 2 / 4 MiB that means **nothing under 2 MiB of contiguous
identical content deduplicates at all**. That is the explanation for TP-002's
zero-dedup import, which I recorded there as correct-but-surprising without
being able to say how surprising it should have been.

### The cost of smaller chunks is narrower than I expected

Throughput is flat across a 34× range of chunk sizes (254–328 MiB/s, noise) and
file-object metadata is negligible even at 64 KiB chunks (2.2 MiB for a 3.7 GiB
project). The only thing that changes is object count — 16× more objects, hence
16× the fsyncs and, on a network store, 16× the round trips.

Worth checking my reasoning here, because it is the load-bearing claim: if
object count really is the only cost, then the parameters are a straightforward
trade against store round trips, and that is a decision S5 has an opinion about.

### The tests are bounded loosely on purpose

`TestAnEditCostsAboutOneChunk` allows 4× the mean chunk where the measured mean
is 1.35× and the worst single offset ~3×. A tighter bound would fail on an
unlucky boundary alignment, which is a flaky test rather than a real signal. The
same reasoning applies to the 50 % / 80 % thresholds in the dedup test, where
measurements were 92 % and 98 %.

If you would rather have tighter bounds and accept occasional noise, that is a
reasonable different call — the numbers to move are in one place each.

### `space check` still lists unversioned files in full

The safe list is now a count; the unversioned list is not, however long it gets.
That asymmetry is the point: the safe list is the expected case, and the
unversioned list is what is about to be snapshotted and then deleted. Summarizing
the second one would hide exactly the thing a user needs to look at before
agreeing.

The flag is rejected rather than ignored when mistyped, because a user who types
`--verbos` and gets a summary would reasonably read it as the full list.

## What could still go wrong

- **The measurement is on pseudo-random data**, which has no internal structure
  to resynchronize on. Every figure is therefore a floor, and I do not know how
  far above the floor a real `.blend` sits. The whole table could be
  pessimistic by a wide margin for structured formats.
- **This still does not answer the actual question.** Whether 1–4 MiB is right
  depends on how much contiguous content survives between two saves of a real
  asset, and that is unmeasured for the third plan running. What changed is
  that the question is now sharp: it is "how long are the unchanged runs", not
  "which rolling hash".
- **FastCDC was not benchmarked.** TP-003 argues the size parameters dominate;
  it does not show the algorithm is irrelevant. E36's door stays open and this
  is not evidence for closing it.
- **Only proportional parameter sets were tested.** Every set scales min,
  expected and max together. Whether a wider max at the same expected size buys
  anything is untested, and it is a plausible cheap win for constant runs.
- **Restore cost at small chunk sizes is unmeasured.** 31,071 chunks instead of
  1,960 will cost something on the read path too, and I only measured the write
  path.
- **`TestSharedRegionDedupsOnceItExceedsTheChunkSize` asserts a lower bound for
  the "shorter than one chunk" case of zero**, which is trivially true. It is
  there to document the limitation, and it would not catch a regression that
  made short regions dedup *worse* — there is no worse than zero.
- **The new tests use `smallParams()`**, not the real ones, so they run fast.
  They pin the law, not the production parameters. A change to
  `tuning.ChunkMinSize` would not fail them.

## Open questions

1. **Should the chunk size parameters change?** My recommendation is no, not
   yet — but for a specific reason rather than caution: the table says the
   decision hinges on a number nobody has measured, and picking now would be
   guessing with more decimal places. The measurement to make is the length of
   unchanged runs between two saves of a real asset.
2. **Is object count actually the constraint I think it is?** I argued it from
   fsync cost and round trips. If the reference server ends up batching
   aggressively, 31,000 objects for a 3.7 GiB project may be entirely
   acceptable, and then smaller chunks are close to free.
3. **Should TP-003's sweep live in the repo as a runnable tool?** It ran from a
   scratch directory against the `chunk` package. Keeping it would make the
   table reproducible when the parameters are eventually revisited; adding it
   means maintaining a benchmark nobody runs. I left it out.
4. **The GraphID addendum from S3 is still unanswered**, carried forward from
   `F-S4-versions.md`.
