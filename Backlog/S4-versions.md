# S4 · Versions, refs, expiry, GC

The slice that turns Fibula from a backup tool into version control. With this
complete, S0–S4 form a usable system for real work (see
[index.md](index.md), milestone).

---

### F-S4-01 · Version object and deliberate versions

**Spec:** E11, E12
**Depends on:** F-S1-04, F-S3-03

- Version object with `parents []VersionID` — a **list** from day one even
  though phase 1 only produces linear history (E11)
- `fibula commit -m "…"` — message mandatory, no expiry
- Snapshot and deliberate version share one type, distinguished by which fields
  are set

---

### F-S4-02 · Refs with compare-and-swap

**Spec:** E13
**Depends on:** F-S4-01, F-S2-04

- `main` as the default ref
- Every ref update goes through CAS — never read-modify-write
- Local and remote ref namespaces kept separate, even though S4 has no remote
  yet: retrofitting the separation later would touch every call site

---

### F-S4-03 · Promotion

**Spec:** E12
**Depends on:** F-S4-01

Turning an auto snapshot into a deliberate version ("yesterday's 14:20 state was
good").

- Creates a **new object** referencing the same manifest — no mutation of
  content-addressed objects
- The snapshot expires normally afterwards
- Costs nothing: the chunks are already there

---

### F-S4-04 · Checkout, log, diff

**Spec:** E11, E12, F-S1-06
**Depends on:** F-S4-02

- `fibula log` — history along the parent chain, snapshots and versions
  distinguishable
- `fibula checkout <ref|version>` — restores the working directory to that state
- `fibula diff <a> <b>` — manifest diff (added / removed / changed / renamed)

**Note:** checkout must run the dirty check from F-S3-04 first — switching away
from unsaved work is the same data-loss path as clearing.

---

### F-S4-05 · Expiry by thinning schedule

**Spec:** E14
**Depends on:** F-S4-01

```
hourly  → last 24 hours
daily   → last 30 days
weekly  → last 6 months
```

- Applies to auto snapshots only; deliberate versions never expire
- Expiry removes the snapshot object, **not** chunks — that is GC's job

---

### F-S4-06 · Garbage collection with full reference check

**Spec:** CLAUDE.md invariant 8, E14, E25
**Depends on:** F-S4-05, F-S2-01

The second data-loss-critical task after the dirty check.

- Reachability from all refs and all unexpired snapshots: version → manifest →
  file → chunk
- A chunk is deleted **only** when no reachable object references it
- Runs through `GCStore` — the only place in the system permitted to delete
- Grace period for objects written concurrently but not yet referenced,
  otherwise GC races an in-flight upload

**Tests:**

- expiring a snapshot never deletes a chunk referenced by a reachable deliberate
  version (E14, hard coupling)
- a chunk uploaded during a GC run survives
- a manifest reachable only through a snapshot keeps its chunks alive

---

### F-S4-07 · Self-hosting dry run

**Spec:** CLAUDE.md § Sequence, phase 1
**Depends on:** F-S4-06

Not a feature — the acceptance test for the milestone. Version a real Blender
project against an `fs.Store` on a second disk or NAS share:

- initial import, several editing sessions, snapshots, deliberate versions
- clear space, restore, verify byte equality
- checkout of an older version and back
- run GC, confirm nothing needed is missing

**Done when:** the working directory can be reproduced from the store at any
point, and the findings are written up as a test plan under `test-plans/`.

Whatever this uncovers becomes the input for S5 and S6 — which is the whole
point of phase 1.
