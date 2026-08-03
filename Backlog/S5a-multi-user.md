# S5a · Multi-user on a shared store

Two people, one store, no server. Decided in
[multi-user](../refinements/2026-08-03-multi-user.md) (E41–E53).

This slice exists because F-B-04 produced a refusal without an answer: a commit
from a stale space is now rejected, and `sync` is the thing that does not exist
yet. Until it does, the only way forward is `checkout`, which discards work.

**Order matters here more than in earlier slices.** The store configuration
comes first because "locking is enabled for this project" has to live somewhere
a freshly initialized client will find it — build the locks first and the first
new space will not honour them.

---

### F-S5a-01 · Store configuration

**Spec:** E51
**Depends on:** —

A configuration that belongs to the store rather than the client, because two
clients must not disagree about it.

- Locking enabled for this project, yes or no
- Lock expiry, default 14 days
- Written atomically like a ref; read on every operation that needs it
- A store without the file behaves as "locking disabled", so existing stores
  keep working

**Done when:** a freshly initialized space against an existing store picks up
the project's locking settings without being told.

---

### F-S5a-02 · `manifest.Merge` in the core

**Spec:** E42, E43, E44
**Depends on:** F-S1-06

```
manifest.Merge(base, ours, theirs) (Manifest, []Conflict)
```

Pure computation over three manifests — no IO, no store, no clock.

**Tests:** table-driven over **all twelve rows** of E44, including the two that
are not conflicts (`Y|Y` and `— |Y|Z` with `Y = Z`). Golden vectors for the
result manifest, because it is a format artefact like any other.

**Done when:** every row of the table has a named test case and the conflict
list names the path and which rule produced it.

---

### F-S5a-03 · `fibula sync`

**Spec:** E45, E52
**Depends on:** F-S5a-02

Three situations, one command:

| | |
|---|---|
| up to date | nothing |
| behind, no local changes | fast-forward: write files, move `Base` |
| behind, with local changes | apply E44, move `Base` |

Afterwards `Base == Ref`, so an ordinary `commit` succeeds — **one parent**, no
merge state, no `--continue` (E45).

**Tests:** the F-B-04 repro run to completion — alice commits, bob syncs, bob
commits, both files present. That is the case the fix could only refuse.

**Done when:** the round trip works without any command that exists only for
merging.

---

### F-S5a-04 · Conflict copies

**Spec:** E46, E47, E53
**Depends on:** F-S5a-03

- `theirs` written to `.fibula/conflicts/<original path>`, original name and
  extension preserved
- The working directory keeps `ours`
- **Changed against deleted keeps the file** and reports the conflict (E47)
- `status` reports leftover copies for as long as they exist

**Tests:** a conflict copy is openable by name (extension intact); `space clear`
and `gc` ignore `.fibula/conflicts/`; a deleted-on-one-side file survives.

**Done when:** an artist can open both versions in Blender without renaming
anything.

---

### F-S5a-05 · Lock storage and commands

**Spec:** E50, E51
**Depends on:** F-S5a-01

- Own namespace in the store, beside the refs; created with `O_EXCL` like a ref
  lock (E13.2)
- Contents: path, owner, timestamp, optional reason, and `broken_from` once
  transferred
- `fibula lock <path>` — patterns expand to one lock per matching path
- `fibula unlock <path>` — own locks
- `fibula unlock --force <path>` — transfers, showing who held it and how long
- `fibula locks` — who holds what, since when

**Tests:** concurrent lock attempts from N processes — exactly one wins;
`--force` preserves `broken_from`.

**Done when:** a forgotten lock is visible without anyone stumbling over it.

---

### F-S5a-06 · Read-only attribute

**Spec:** E49
**Depends on:** F-S5a-05

A foreign lock marks the local file read-only, so the DCC tool refuses to save.

- Set on `checkout`, `restore` and `sync`
- **Advisory only** — Fibula never relies on it; the commit check is the
  boundary
- Cross-platform: Windows attribute vs. Unix mode bits
- **`space clear` must delete read-only files** — on Windows the attribute has
  to be cleared first, and that path runs through the dirty check (E17)

**Tests:** clear a space containing read-only files on Windows and Linux; a
tool that saves by delete-and-recreate drops the attribute and the commit check
still catches it.

---

### F-S5a-07 · Lock enforcement on commit

**Spec:** E49
**Depends on:** F-S5a-05

Commit is refused when a foreign lock covers a changed path. Editing is never
refused.

The message must distinguish "locked before you last synced" from "locked
since", because offline clients cannot have known (E49).

**Done when:** a solo developer with locking disabled never encounters this
path at all.

---

### F-S5a-08 · Broken and expired locks

**Spec:** E51
**Depends on:** F-S5a-05, F-S5a-01

- Expiry from the store configuration, not the client
- An expired lock may be taken over by anyone
- The space records which locks it believes it holds and **reports any
  difference against the store on contact** — same pattern as `Head.Base`
  against the ref, so the notice does not depend on anyone remembering to send
  it

**Tests:** the full scenario from the refinement — A locks, A disappears, B
breaks, B commits, A returns and is told, A commits and gets an ordinary
conflict rather than a loss.

**Note:** expiry depends on a clock, and TP-005 named clock skew over a network
share as the likely real-NAS defect. Same cause as `breakStaleLock`; solve it
once, not twice.

---

### F-S5a-09 · Point the staleness message at `sync`

**Spec:** E52, E53
**Depends on:** F-S5a-03

`cmd/fibula`'s `behindAdvice` currently suggests `snapshot` then `checkout`,
because that was the only safe route when the check shipped. Once `sync` exists
it is the answer, with `snapshot` as the optional safety step before it.

Small, but leaving it stale means the tool recommends the workaround over the
fix.
