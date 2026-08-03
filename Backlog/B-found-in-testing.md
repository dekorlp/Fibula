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
