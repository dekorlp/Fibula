# B · Defects found in testing

Items that came out of a test run rather than out of a slice, per
[index.md](index.md): *"Bugs found during testing go straight in here with a
reference to the test plan."*

They carry `F-B-nn` IDs rather than `F-S<slice>-nn`, because they do not belong
to a planned slice. The convention deviation is deliberate and the IDs are
permanent like any other.

Items fixed in the branch that found them are recorded in the test plan and the
review document instead, not here.

---

### F-B-01 · `Put` should report whether the write was new

**Spec:** E24, E29
**Found by:** [TP-002](../test-plans/TP-002-scale-run.md), EC-104
**Blocked on:** the first network backend

`SnapshotResult.Chunked` measures the file content that was read and cut, not
the bytes that actually reached the store. It cannot measure the latter, because
`ObjectStore.Put` is idempotent and returns only an error — a chunk the store
already holds is written again and looks identical to a new one.

At scale the gap is large: an edit that re-chunked 28.5 MiB grew the store by
17.2 MiB, so the reported figure overstated by 40 %. Locally that is cosmetic.
Once the store is remote it is the difference between "how much did I upload"
and "how much did I hash", which is the number a user on a slow connection
actually wants.

The fix is a store interface change — `Put` reporting whether the object was
new, or a batched existence query ahead of the write. **Both are decisions for
the slice that introduces network transfers**, not for now: E24 deliberately
gives `ObjectStore` only Get/Put/Exists, and widening it for a progress counter
before there is a transfer to measure would be the wrong order.

Until then the field is named for what it measures.

---

### F-B-04 · A commit silently discards another client's work — **option (a) done 2026-08-03**

**Fixed in:** `reviews/F-B-04-stale-commit-check.md`

Option (a) shipped: a commit is refused when the space does not descend from the
ref. Implementing it required splitting `Head` into `Version` (what is in the
directory) and `Base` (what the next commit builds on), because auto snapshots
were moving the only field there was.

The CLI prints a way out with the error — `snapshot` to keep the current work,
then `checkout`. That route is only safe because snapshots land on their own ref
and cannot collide (TC-409), and it is what turns the refusal from a dead end
into a detour.

**Option (b) is still open** and belongs with S5: there is no merge, so bringing
two diverged states together remains manual. The original entry follows.

---

### F-B-04 (original entry) · A commit silently discards another client's work

**Spec:** E13.3 (a non-fast-forward is a user decision, never an automatic
overwrite), E16 (the space stores its checked-out VersionID)
**Found by:** [TP-005](../test-plans/TP-005-network-share.md), EC-401
**Blocked on:** nothing technically — blocked on a decision, see below

Two clients sharing a store, no concurrency needed:

```
alice commits alice.txt
bob   commits bob.txt three seconds later

head: asset.txt, bob.txt          alice.txt is gone
log:  bob -> alice -> baseline    looks perfectly linear
```

`commitTarget.parentOf` (`client/snapshot.go:220`) takes the parent from the
ref's **current** value in the store, while the manifest is built from the local
working directory. Nothing checks that the working directory descends from head,
so a commit claims a parent whose content it does not contain. The lock, the CAS
and the atomic rename all work correctly — the missing check sits above them.

The space already records its checked-out VersionID (E16), so the comparison
needs no new state.

Nothing is destroyed in the store (old versions stay reachable through the
parent chain), but head is wrong and the user gets no signal. For the
two-artist studio this project targets, it is the first scenario they hit.

**`snapshot` is not affected** (TP-005 TC-409): snapshots CAS against a zero old
value, so each lands on its own ref and two clients interleave correctly. The
safety net holds where the deliberate path does not.

**The decision, which is why this is not just fixed:**

- **(a) Reject and stop.** Refuse the commit when the checked-out VersionID
  differs from the ref. Few lines, honours E13.3 — but the user's only way
  forward is `checkout`, which discards their working directory. Silent data
  loss becomes a dead end with a clear message.
- **(b) Reject and offer a way out.** The same check plus the sync semantics
  that make it survivable — fetch, and some answer to "my file and theirs both
  changed". That answer is the conflict/locking strategy still open in
  `refinements/index.md`, and it belongs with S5.

(a) is strictly better than today and can ship now. (b) is the real fix.
Recommendation is to do (a) immediately and treat it as the forcing function
for the S5 conflict discussion.

---

### F-B-03 · Document that `.blend` files should be saved uncompressed

**Spec:** E3 (chunking parameters are an efficiency question)
**Found by:** [TP-004](../test-plans/TP-004-real-project-run.md), EC-303
**Blocked on:** nothing — it is a README section, waiting only for the README to
have a usage part

Measured over six editing cycles on a real `.blend`: an uncompressed file costs
roughly what the edit was worth (1.4 % of the file for a material tweak, 30 % for
a whole-mesh displace), while a compressed one costs **~50 % every time**,
regardless of the edit. A stateful compressor turns any local change into a
global one from that offset onwards.

At small file sizes compression still wins overall, because the 4× smaller
baseline outweighs the worse incremental behaviour. It inverts with size: for
the same local mesh edit, going from a 16 MiB to a 66 MiB scene multiplied the
uncompressed cost by 1.4 and the compressed cost by 4.4. Uncompressed is
approximately O(edit), compressed is O(file).

**No code change.** Fibula behaves correctly either way; users just need to know.
The natural home is a "working with DCC tools" section, which is also where the
`.blend1` note from EC-302 belongs.

---

### F-B-02 · `space check` prints every path — **done 2026-08-03**

**Found by:** [TP-002](../test-plans/TP-002-scale-run.md), EC-105
**Fixed in:** `reviews/chunk-parameter-sweep.md`

`fibula space check` lists all safe-to-delete paths — 3,050 lines on the test
corpus, and one line per asset on a real project. The information a user wants
from it is the verdict plus anything that blocks it; the full list is noise that
buries the problems.

Print counts and the problem cases, with the full list behind a flag. The
`Unversioned` and `Problems` lists must stay fully visible however long they
are: those are the ones that decide whether data is at risk.

Done as specified: the safe list is a count, `--verbose` restores it, and the
unversioned list is never summarized. An unrecognized flag is an error rather
than being ignored.
