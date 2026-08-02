# S2 · Store — interfaces and the filesystem backend

The persistence layer. `fs.Store` is not a placeholder for "the real thing
later": it is a first-class deployment (CLAUDE.md, single binary path) and after
S4 the backend Fibula is actually used with.

---

### F-S2-01 · Store interfaces

**Spec:** E24, E25, E29
**Depends on:** F-S1-01

```
ObjectStore   Get · Put · Exists
RefStore      Get · CompareAndSwap · List · Delete
GCStore       Delete                              // separate, admin path only
```

- `Get(ctx, id) ([]byte, error)` — verified bytes, not a stream (E26)
- `Exists(ctx, ids []ID) ([]bool, error)` — **batch-only**, the signature offers
  no single lookup
- `Delete` deliberately absent from `ObjectStore` — a client holding one must be
  unable to delete

**Done when:** it is impossible to write a compiling client that deletes objects
through `ObjectStore`.

---

### F-S2-02 · Verification decorator

**Spec:** E27, trust boundary in § 8 of the object model
**Depends on:** F-S2-01

- Wraps any `ObjectStore` and checks every `Get` result against its hash before
  returning it
- Wiring makes the wrapper the only way to construct a usable store, so a new
  backend cannot forget it

**Tests:** a tampered object in the underlying store surfaces as an error, never
as data.

---

### F-S2-03 · `fs.Store`

**Spec:** E29, CLAUDE.md § Store
**Depends on:** F-S2-01

- Content-addressed layout with fan-out directories (avoid one directory holding
  a million entries)
- **Atomic writes:** temp file → verify → rename. No half object ever visible.
- **Idempotent:** writing the same hash twice is not an error
- `Exists` as a batched `stat` loop — no index needed at this size

**Tests:**

- **Crash mid-upload** leaves no half-visible object (kill between write and
  rename)
- Concurrent writes of the same hash do not corrupt each other (`-race`)
- Windows and Linux, given the different rename semantics

---

### F-S2-04 · `fs.RefStore` with compare-and-swap

**Spec:** E13
**Depends on:** F-S2-01

Refs are the only mutable structure, and a lost ref update is a lost working
state (E13, criticality).

- CAS via atomic rename or file locking — **not** read-modify-write
- Separate namespaces for local and remote refs

**Tests:** concurrent CAS from N goroutines — exactly one wins, no update is
silently lost.

---

### F-S2-05 · Object round trip across the store

**Spec:** E9, E24
**Depends on:** F-S2-02, F-S2-03, F-S1-04

Writing and reading back all five object types plus chunks, end to end,
including manifests (which live in the store per E9, not just locally).

**Done when:** a complete file tree can be written to a store and restored
byte-identically into an empty directory.
