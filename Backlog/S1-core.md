# S1 · Core — chunking, hashing, object format

The deterministic foundation. Pure functions over streams: no network, no
database, no filesystem layout. **This slice is the format** — everything pinned
early in CLAUDE.md is decided here in code.

Golden vectors written in this slice become binding fixtures. When one fails
later, that is a format break, not a test update.

---

### F-S1-01 · Hash package with domain separation

**Spec:** E4, E34
**Depends on:** F-S0-06

- BLAKE3, 256 bit, via `derive_key` with one context string per object type
  (`fibula chunk v1`, `fibula file v1`, `fibula manifest v1`, `fibula version v1`,
  `fibula graph v1`, `fibula signature v1`)
- Rendering: hex, lowercase, **no prefix**
- Typed ID per object kind so a ChunkID cannot be passed where a FileID is
  expected

**Done when:** a chunk and a file object with identical bytes provably produce
different IDs.

---

### F-S1-02 · Content-defined chunker

**Spec:** E2, E3, CLAUDE.md pinned point 2
**Depends on:** F-S0-06

- Rolling hash (Buzhash or Rabin — pick one, record the choice as an addendum
  with a rationale)
- Target size 1–4 MB, min/max bounds from the constants catalogue
- Streaming: operates on `io.Reader`, never holds the whole file in memory

**Tests:**

- **Property test:** an insertion at the start of a file shifts boundaries only
  locally, not throughout
- **Round trip:** `chunk → reassemble` is byte-identical
- **Large file** (> RAM) passes through with an asserted memory ceiling
- **Golden vectors** for boundaries — a failure here is a dedup regression, not
  a format break (E3): find the cause, then reset deliberately

---

### F-S1-03 · Path normalization and validation

**Spec:** E8
**Depends on:** F-S0-01

All six rules, as a separate unit because everything downstream depends on it:

- `/` as separator on every platform
- relative to the manifest root, no `..`, no leading `/`
- Unicode NFC
- case-sensitive storage, **collision rejected at creation time**
- bytewise sorting over the UTF-8 bytes
- tab and LF forbidden in paths

**Tests:** the macOS NFD case (decomposed umlaut equals composed umlaut), the
case-collision rejection, sorting independent of locale.

---

### F-S1-04 · Object serialization

**Spec:** E32, E33, E34, E35
**Depends on:** F-S1-01, F-S1-03

Canonical text encoding for all five object types (manifest, file, version,
graph, signature):

- `fibula-<type> v1` framing line
- UTF-8 without BOM, LF endings, tab separator, no trailing whitespace
- RFC 3339 UTC timestamps with `Z` and second resolution
- optional fields omitted rather than written empty
- fixed field order per type
- chunk length per line in the file object (E35)

**Tests:**

- **Golden vectors** per object type (checked-in fixtures) — binding
- Round trip parse → serialize is byte-identical
- **Rejection** of non-canonical input: CRLF, BOM, unsorted manifest entries,
  leading zeros, an empty optional field

---

### F-S1-05 · File object construction

**Spec:** E2, E3
**Depends on:** F-S1-02, F-S1-04

- FileID = BLAKE3 **of the file content**, computed in the same pass as chunking
- Chunk list plus per-chunk lengths as an attribute, not as identity

**Done when:** two chunkers with different parameters over the same file produce
the same FileID with different chunk lists.

---

### F-S1-06 · Manifest construction and diff

**Spec:** E5, E7, E9, E10
**Depends on:** F-S1-03, F-S1-04, F-S1-05

- Build from a file tree: path → FileID + size, sorted
- ManifestID over the canonical serialization
- Diff between two manifests: added / removed / changed / renamed (rename
  detected via identical FileID at a different path)
- Multiple manifest roots must not be precluded (E10)

**Tests:** **cross-platform determinism** — the same tree yields a byte-identical
manifest on Windows and Linux (CI matrix from F-S0-04).

---

### F-S1-07 · Manifest chunking parameters

**Spec:** E6
**Depends on:** F-S1-02, F-S1-06

The manifest goes through the ordinary chunker but with a ~64 KB target size, so
a single changed line transfers ~64 KB instead of the whole manifest.

**Done when:** a benchmark demonstrates it — change one line in a manifest with
50,000 entries, measure how many chunks differ. Expected: one.
