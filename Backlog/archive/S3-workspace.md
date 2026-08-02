# S3 · Workspace — local state, space, snapshots

The first slice a human touches. From here on Fibula has a CLI and a working
directory rather than a library surface.

Contains the single most safety-critical task of the project: **F-S3-04, the
dirty check.** That one is the trust question of the entire space feature.

---

### F-S3-01 · `.fibula/` layout and status cache

**Spec:** E15, E16
**Depends on:** F-S1-06, F-S2-03

Local state in `.fibula/`:

- checked-out ref + VersionID
- manifest of the checked-out version
- **status cache** per file: path, mtime, size, FileID, **chunk list**
- local snapshot chain (not yet synchronized)
- configuration: store address, storage budget

The stored chunk list replaces a local chunk cache (E16) — 32 bytes per chunk,
roughly 3 MB for 200 GB of assets.

**Done when:** `status` on an unchanged 200 GB tree completes without rehashing
anything.

---

### F-S3-02 · `.fibulaignore`

**Spec:** E18
**Depends on:** F-S3-01

- gitignore syntax, no invention of our own
- applies to scanning, snapshotting and clearing alike

---

### F-S3-03 · Scan, status and snapshot

**Spec:** E12, E16
**Depends on:** F-S3-01, F-S3-02

- `fibula init` — create a space, write configuration
- `fibula status` — cache-backed, heuristic, fast
- `fibula snapshot` — auto snapshot: no message, expiry set, parent is the
  previous snapshot
- Time and storage-budget triggers (E15): budget reached → snapshot → **suggest**
  clearing, never clear automatically

---

### F-S3-04 · Dirty check before clearing

**Spec:** E17, CLAUDE.md invariant 6
**Depends on:** F-S3-03, F-S2-02

The safety-critical task of the project. Two levels of guarantee, deliberately
different from `status`:

| | `status`, auto snapshot | `space clear` |
|---|---|---|
| Data source | status cache | **full re-hash**, cache ignored |
| Store existence | assumed | **positively confirmed** via batch query |
| When in doubt | continue | **abort** |

Three checks, all mandatory:

1. Re-hash every local file → FileID
2. Every FileID appears in a reachable version or snapshot
3. **Every referenced chunk exists in the store** — not just locally

Point 3 is the one that gets forgotten: a locally created, never synchronized
snapshot sits on the very disk about to be cleared.

**Tests** (each one named, per CLAUDE.md § 6):

- store unreachable → abort, nothing deleted
- chunk exists locally but not in the store → abort
- file changed since the snapshot → abort
- mtime unchanged but content changed (the cache lies) → **still detected**,
  because the cache is ignored here
- unversioned file → snapshot and push automatically, then delete (no abort)

---

### F-S3-05 · `fibula space clear` and restore

**Spec:** E15, E18
**Depends on:** F-S3-04

- Delete asset files, keep `.fibula/` — the space retains identity and history
- Ignored files **stay in place**, do not block, and are reported
  ("12 GB of ignored files not deleted, `--include-ignored` to include them")
- `fibula restore` downloads the manifest state back

**Done when:** clear → restore reproduces the working directory
byte-identically, with ignored files untouched.

---

### F-S3-06 · Path safety on restore

**Spec:** CLAUDE.md § 4 Security
**Depends on:** F-S3-05

Manifest paths are untrusted input, even in single-user operation — they may
come from a store someone else wrote to.

- no `..`, no absolute paths, no symlink escapes
- no writing outside the target directory
- Windows reserved names (`CON`, `PRN`, `AUX`, `NUL`, `COM1`…) and drive
  prefixes

**Tests:** a hand-crafted malicious manifest writes nothing outside the target
directory.
