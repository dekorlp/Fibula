# F-S2-01 … F-S2-05 · Store — interfaces and the filesystem backend

**Date:** 2026-08-02
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Slice S2 complete: the persistence layer. Three interfaces with the deletion
capability deliberately split off, a verification decorator that every backend
passes through, `fs.Store` with atomic idempotent writes, `fs.RefStore` with
compare-and-swap, and an end-to-end round trip that writes a whole file tree
into a store and restores it byte-identically.

With this, the pieces from S1 and S2 have to agree with each other for the
first time rather than only with their own tests.

## Spec reference

| Task | Implements |
|---|---|
| **F-S2-01** | E24 (two interfaces, not one), E25 (`Delete` outside `ObjectStore`), E26 (`Get` returns bytes), E29 (idempotent, atomic, batch-only `Exists`, mandatory CAS) |
| **F-S2-02** | E27 (verification as a decorator), object model § 8 trust boundary |
| **F-S2-03** | E29, CLAUDE.md § 2 Store |
| **F-S2-04** | E13 (refs mutable, CAS mandatory, local and remote separate) |
| **F-S2-05** | E9 (the manifest lives in the store), E24 |

**Invariants touched:** no data loss (6) — the `Put` flush is part of it, see
below. Content addressing is verified, not assumed (7) — the decorator. Never
GC without a reference check (8) — anchored in the type system by E25 rather
than implemented here.

## Context

Follows `reviews/F-S1-core-format.md` and
`reviews/kannst-loslegen-zbfu63-followup.md`. The S1 review flagged that the
file object cannot be verified against its own ID; S2 is where that had to be
resolved, and point 1 below is the resolution.

## Changed files

| File | |
|---|---|
| `store/store.go` | `Key` with its typed constructors, `ObjectStore`, `GCStore`, `RefStore`, `RefName` with scope and validation |
| `store/verify.go` | the decorator, `Verify` per object type, and `VerifyFileContent` for the complete check |
| `store/doc.go` | package purpose |
| `store/fs/fs.go` | the object store: fan-out layout, atomic `Put`, batched `Exists`, `Delete` behind `OpenGC` |
| `store/fs/refs.go` | the ref store: CAS guarded by an in-process mutex and an on-disk lock |
| `errs/errs.go` | six new sentinels: object not found, corrupt object, invalid store, invalid ref name, ref not found, ref conflict |
| `store/store_test.go` | keys, the decorator in both directions, `VerifyFileContent`, ref names |
| `store/fs/fs_test.go` | round trip, idempotence, concurrent writes, tampering, the crash case, batching |
| `store/fs/refs_test.go` | CAS conflicts, the concurrent-CAS race, scope separation, listing, name validation |
| `store/fs/lock_test.go` | a held lock, a stale lock, lock release, corrupt ref content, foreign files |
| `store/fs/roundtrip_test.go` | the F-S2-05 end-to-end tree round trip |
| `store/fs/dedup_test.go` | file-level dedup and the swapped-chunk case |
| `README.md`, `Backlog/` | layout table, S2 archived |

## What to look at in review

### 1. The file object gap, and what was done about it

The S1 review flagged this and S2 had to answer it: the FileID is the hash of
the file *content*, not of the serialized file object (E3), so `Verify` cannot
check a file object against its key the way it checks the other five types.

The answer is deliberately two-part, and the split is the thing to review:

- **`Verify`** parses the file object and checks it is self-consistent — chunk
  lengths add up to the recorded size, no empty chunk IDs. That catches
  truncation and garbage, which is most of what actually goes wrong. It does
  **not** prove the object belongs to that FileID, and the doc comment says so
  in those words, because CLAUDE.md § 4 requires the trust boundary to be
  documented wherever verification does not happen.
- **`VerifyFileContent`** is the complete check: it reads every chunk,
  reassembles and compares against the FileID. It is a separate, explicitly
  called function rather than part of `Get`, because doing it on every read
  would turn one lookup into one per chunk — a thousand round trips for a 4 GB
  asset.

`TestVerifyOfAFileObjectIsStructuralOnly` asserts the limitation rather than
merely commenting it: it checks that a well-formed file object is accepted
under a completely unrelated FileID. `TestVerifyFileContentDetectsASwappedChunk`
then shows the complete check catching exactly that case.

**What this means for S3:** the dirty check before `space clear` must call
`VerifyFileContent`, not rely on `Get`. E17 point 3 already demands that every
referenced chunk exists in the store; this is the function that answers it
honestly.

### 2. `Put` flushes to disk, and that is not a performance oversight

`fs.Store.Put` calls `Sync` before the rename. Without it, a crash shortly
after upload can leave a zero-length file that `Exists` still reports as
present — and the dirty check before `space clear` deletes local data based on
exactly that answer. Durability here is part of the no-data-loss invariant.

The cost is real: one fsync per chunk, so roughly 100,000 fsyncs for a 200 GB
initial upload. If that turns out to be the bottleneck in phase 1, the honest
fix is batching at a higher level (fsync once per group of chunks, before the
manifest that references them is published) — not dropping the flush.

### 3. `Open` returns the verified store, and there is no way around it

The concrete store type is unexported. `fs.Open` wraps it in `store.Verified`
before handing it out, so this package cannot produce an unverified store at
all. That is what F-S2-02 means by "wiring makes the wrapper the only way to
construct a usable store"; the verification invariant is not a convention a new
backend can forget.

The decorator checks on `Put` as well as on `Get`. Checking on write is nearly
free — the bytes are in hand — and turns a caller that computes the wrong key
into an immediate error rather than an object that reads back as corrupt weeks
later.

### 4. Ref CAS is guarded on two levels, and only one of them is really tested

- **In-process:** a mutex per ref name. This is what a sync engine running
  several pushes at once hits, and it is what
  `TestConcurrentCompareAndSwapLosesNothing` exercises — twelve goroutines,
  exactly one winner, eleven conflicts, no silent loss.
- **Cross-process:** an `O_EXCL` lock file, with a stale-lock timeout of 30 s
  for the case where a process dies holding it. `TestLockHeldByAnotherProcess…`
  and `TestStaleLockIsBroken` stage those states by writing the lock file
  directly. **They simulate a second process, they do not run one.** A genuine
  two-binary test against one directory belongs in the end-to-end suite.

The backoff is capped at 100 ms per attempt. Without the cap the linear growth
put the worst case at about 13 seconds of blocking, which I noticed only while
writing the test for it.

### 5. `Delete` cannot be reached through `ObjectStore`

F-S2-01's done-criterion is "it is impossible to write a compiling client that
deletes objects through `ObjectStore`", which is a compiler property rather
than a test. `TestObjectStoreCannotDelete` states it in a form a reader can
see: it asserts that what `Open` returns does not satisfy `store.GCStore`, so
even a type assertion cannot get at deletion.

### 6. A test assumption that was wrong, and what it revealed

`TestIdenticalContentIsStoredOnce` first asserted that the number of chunks on
disk equals the number of chunk references in the file object. It failed: 3 on
disk for 9 references. The content in the fixture is repetitive enough that
several of its own chunks come out byte-identical and deduplicate *against each
other*. The test now asserts the correct invariant — the store holds exactly
the distinct chunks — and says so, because intra-file dedup is worth showing.

## What could still go wrong

- **`store/fs` coverage is exactly 80.0 %**, right on the floor. The uncovered
  remainder is mostly I/O error paths that need fault injection to reach
  (a failing `Sync`, a failing `Chmod`, a `WalkDir` that errors mid-listing).
  Any small addition of uncovered code drops the package below the gate.
- **The crash test does not crash anything.** `TestNoHalfObjectIsVisible`
  stages a leftover temporary file the way a killed process would leave one; it
  does not kill a process. The property it checks — that a temp file is never
  reachable as an object and never makes `Exists` lie — is the one that
  matters, but a real kill test would be stronger.
- **Windows rename semantics are handled but only reasoned about, not
  observed.** `Put` falls back to "the object already exists, so we are done"
  when the rename fails, because Windows refuses to rename onto an existing
  file where POSIX replaces silently. CI runs the tests on `windows-latest`, so
  this will be exercised — but the specific branch only fires under a
  concurrent write of the same hash, which the test suite does trigger, so it
  should be genuinely covered. I have not confirmed it by instrumentation.
- **The stale-lock threshold is a guess.** 30 seconds is long enough that no
  live update should ever be interrupted and short enough that a crash does not
  block a user for long, but nothing measured says 30 rather than 10 or 120.
- **No fsync of the containing directory.** After the rename, the directory
  entry itself is not flushed, so a crash immediately after `Put` can lose the
  entry on some filesystems even though the file content was flushed. Adding it
  costs another syscall per write and is not portable to Windows in the same
  form. This is a known, deliberate gap rather than an oversight.
- **`VerifyFileContent` reads every chunk through the store.** On a remote
  backend that is one request per chunk. It has no batching and no parallelism;
  when S5 puts a network behind the interface, this function will need both.
- **Ref listing tolerates foreign files silently.** A file in the refs
  directory whose name is not a valid ref is skipped rather than reported.
  That keeps a listing from failing over an editor backup, but it also means a
  ref that becomes invalid through a future tightening of the name rules would
  quietly disappear from listings.

## Found by CI after the review was written

Appended rather than split into a second document, because it belongs to this
slice and the slice is not merged yet. Nothing above was rewritten.

**govulncheck failed on the first CI run: GO-2026-4602**, "FileInfo can escape
from a Root in os", present in `os@go1.25` and fixed in `os@go1.25.8`. The call
path it reported is real — `fs.refStore.List` uses `filepath.WalkDir`, which
reaches `os.ReadDir`. S1 never triggered it because no code walked a directory.

The reason it was reachable at all is a CI detail worth knowing: **`setup-go`
treats the `go` directive in `go.mod` as an exact version, not as a floor.** So
`go 1.25.0` meant CI genuinely built with 1.25.0 and would have kept doing so
as patch releases appeared. Raising the directive to `1.25.8` is therefore the
actual fix rather than a workaround — and it is the honest one, since `go.mod`
is what a studio embedding the client library reads as the minimum.

That coupling is now written into the workflow, so the next reader does not
mistake the pin for bookkeeping: a standard-library vulnerability surfaces as a
red `govulncheck` job and is fixed by raising the directive. All four jobs are
green after the bump.

I could not verify this locally — the vulnerability database at `vuln.go.dev`
is unreachable from this environment — so CI was the only check available.

## Open questions

1. **The GraphID placement is still unanswered** (E21 versus E11 and the E33
   example). It did not block S2 — the store is content-agnostic — but S4
   writes version objects for real, so it is now one slice away from being
   expensive.
2. **Is `Key` the right shape?** It carries the object type alongside the hash,
   which the verifier needs and which makes the on-disk layout self-describing.
   The alternative is a bare digest plus a type argument on every call. I chose
   the former; it is a public type and therefore a promise.
3. **Should `RefStore.Delete` require a compare-and-swap too?** Right now
   deleting a ref does not check its current value, so a delete races against
   a concurrent update in a way a swap would not. No content is lost either
   way — every version stays in the object store — but the working state
   pointer is.
4. **Fsync batching**, per point 2 above: worth designing now, or worth waiting
   until a real 200 GB upload shows whether it matters?
