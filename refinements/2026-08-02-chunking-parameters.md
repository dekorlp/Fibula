# Chunking parameters

**Date:** 2026-08-02
**Status:** Decided
**Phase:** 1 (build it and use it — no compatibility guarantee)

Closes the item "concrete chunking parameters" left open in
[index.md](index.md). Required by F-S1-02, which asks for the algorithm to be
picked and the choice recorded with a rationale before the chunker is written.

**Numbering** continues the global sequence started by the
[object model](2026-08-02-object-model.md) (E1–E35), so that a reference like
"E37" is unambiguous without naming the entry it came from.

**The whole entry is revisable.** Per object model E3 the FileID is the hash of
the file content, not of the chunk list, so everything decided here costs dedup
rate rather than correctness. A later change needs a dated addendum — but it
needs no format break, and no store written under these parameters becomes
unreadable.

---

## E36 — Buzhash as the rolling hash

CLAUDE.md pinned point 2 names Rabin and Buzhash as the candidates. **Buzhash.**

*Rationale:* per byte it is a rotate and two XORs against a 256-entry table.
Rabin needs carry-less polynomial arithmetic over GF(2), which is more code, has
more parameters to pin down (the irreducible polynomial, the degree) and carries
a patent history in exactly this application. Neither is a correctness argument
— both produce boundaries of comparable quality — and that is the point: with
two equivalent options the simpler one wins, because it is the one that is
obviously deterministic across platforms.

*Considered and rejected for now: Gear / FastCDC.* It is measurably faster than
both, mainly through normalized chunking, which narrows the size distribution.
It is not chosen because it is outside what pinned point 2 names, and this
refinement is meant to settle an open parameter rather than quietly widen a
pinned decision. The door stays open: E3 makes the switch a dedup question, and
the honest way to make it is a measurement against real asset data in phase 1,
not a preference expressed before a single `.blend` file has been versioned.

*Consequence:* the throughput budget is not where the argument is anyway. BLAKE3
runs over the same bytes and dominates; the rolling hash has to stay out of its
way, not win a benchmark of its own.

## E37 — Window 64 bytes, boundary on the low mask bits

| | |
|---|---|
| Hash width | 64 bit |
| Window | **64 bytes** |
| Boundary condition | `hash & ((1 << maskBits) - 1) == 0` |

*Window:* 64 bytes is enough context for boundaries to resynchronize quickly
after an edit and short enough that a chunk boundary depends on a genuinely
local neighbourhood. With a 64-bit hash rotated by one bit per byte, the 64
bytes in the window sit at 64 distinct rotations — the window is used fully and
no byte cancels another out. It also makes the roll step cheap: the byte leaving
the window has been rotated exactly 64 times, which is the identity, so removal
is a plain XOR.

*Boundary on the low bits:* the table is BLAKE3 output (E38) and therefore
uniformly distributed, so the low bits are as good as any other selection and
the test is a single mask.

## E38 — Expected chunk size is `min + 2^maskBits`, not `2^maskBits`

The minimum size is a hard bound: below it, no boundary is tested at all. The
mask therefore does not describe the average chunk size — it describes the
average *distance past the minimum* at which the first boundary appears. The
expected size is `min + 2^maskBits`, and choosing `maskBits` such that
`2^maskBits == avg` would overshoot the target by exactly `min`.

Parameters follow from that:

| | min | maskBits | expected | max |
|---|---|---|---|---|
| **Assets** | 1 MiB | 20 (1 MiB) | **2 MiB** | 4 MiB |
| **Manifests** (E6) | 32 KiB | 15 (32 KiB) | **64 KiB** | 128 KiB |

The asset figures realize CLAUDE.md pinned point 2 (target size 1–4 MB) in
binary units. The manifest figures realize E6: the manifest goes through the
same chunker at a much smaller target, so that one changed line in a sorted,
line-based file transfers roughly 64 KB instead of the whole manifest.

The maximum truncates the tail of the distribution. At `max = min + 3·2^maskBits`
roughly 5 % of chunks are cut by the bound rather than by the content — enough
to keep a pathological input (a long run of identical bytes, common in
uncompressed textures and audio) from producing one gigantic chunk, and rare
enough not to distort dedup.

*Edge cases:* the last chunk of a stream may be shorter than the minimum. An
empty stream produces **no chunks at all** — its file object has size 0 and an
empty chunk list, and its FileID is still well defined because it is the hash of
the content (E3), not of the list.

## E39 — The Buzhash table is derived from BLAKE3, not checked in as a literal

`table[i] = LittleEndian.Uint64(out[8i : 8i+8])`, where `out` is 2048 bytes of
BLAKE3 XOF output in `derive_key` mode with the context
`fibula buzhash table v1` over empty key material.

*Rationale:* a 256-entry table of random-looking 64-bit literals is unreviewable
— nobody can tell a correct one from a corrupted one by reading it, and a single
mistyped digit would silently cost dedup rate without failing any test. Derived
from BLAKE3 the table is reproducible from one line of specification, it is
demonstrably uniform, and its correctness is checkable: the golden vector for
the table is the hash of the table, not the table.

*Determinism:* the byte order is pinned to **little-endian** explicitly, so that
the table is identical on every architecture. It is the one place in the
chunker where host byte order could have leaked in.

*Note on the context string:* it deliberately does **not** live in package
`format`. The table is a tuning parameter — changing it changes boundaries and
therefore dedup rate, not object identity. It belongs with the other tuning
values, and the strict separation demanded by CLAUDE.md § 8 only means anything
if it is also applied to the awkward cases.

## E40 — Chunk boundary golden vectors are a regression fence, not a format fence

Restating the consequence of E3 for the test suite, because this is where the
distinction becomes operational:

- **Object serialization golden vectors** (E32, E33): a failure is a **format
  break**. Find the cause and fix the code, never the fixture.
- **Chunk boundary golden vectors**: a failure is an **unintended dedup
  regression**. Find the cause; if the change was deliberate, reset the fixture
  deliberately and note it in the review document.

Both are checked in. They mean different things, and treating the second like
the first would make every parameter experiment look like a catastrophe.

---

## Deliberately open

- **Switching to FastCDC / Gear** (E36) — decidable on measurement, not on
  preference. Belongs to phase 1 with real asset data.
- **Per-format parameters** — whether uncompressed textures deserve different
  bounds from compressed video is exactly the kind of question that should be
  answered by observation rather than in advance.
- **A shared cross-project chunk cache** — already listed as open in the object
  model, unaffected by this entry.
