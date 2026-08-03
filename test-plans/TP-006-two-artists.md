# TP-006 · Two artists on one store, with real `.blend` files

**Date:** 2026-08-03
**Task:** exercise S5a's transfer half (F-S5a-02/03/04) before building the
locking half
**Branch:** feature/tp006-two-artists
**Executed:** yes, against the built binary
**Result:** pass, no defects found

## Why this plan exists

`fibula sync` shipped with eight unit tests and one manual end-to-end run. That
run found a defect the unit tests could not — a `status` that reported a clean
directory over unrecorded work — which is the argument for doing the same thing
at a realistic scale before adding locking on top.

Phase 1 is "build it and use it". Locking on an untested transfer would only
increase how much a later finding overturns.

## What is real here and what is not

**Real:** genuine Blender `.blend` files of 16–67 MiB, three of them different
editing states of the same scene taken out of the TP-004 store, so the
differences between them are what Blender actually writes rather than something
constructed. Two separate working directories against one shared store, driven
through the CLI.

**Not real, and it matters:**

- **Blender was not connected during this run.** The states were produced
  earlier (TP-004) and replayed here, so no file was written by a live save
  while a sync was in progress.
- **The conflict copy was never opened in Blender.** It is byte-identical to a
  file Blender wrote and carries the `BLENDER` magic and the `.blend`
  extension, which is strong evidence but not the same as watching it load.
- **Sequential, not concurrent.** Anna and Ben take turns; nothing races.
- **Five assets.** Nothing here says how a merge behaves at TP-002's 3,050.
- **Local store.** No network share.

| | |
|---|---|
| Assets | `hero.blend` (16.3 MiB), `level.blend` (66.6 MiB), `prop.blend` (4.5 MiB), `notes.txt`, `.fibulaignore` |
| Store | a plain directory, no server |
| Platform | Windows 11, Go 1.26.1 |

## Test cases

| ID | Case | Result |
|---|---|---|
| TC-501 | Two artists edit different assets | pass — no conflict, both edits survived |
| TC-502 | Both edit the same `.blend` | pass — conflict reported, copy is a valid `.blend` |
| TC-503 | One deletes what the other edited | pass — the file stayed (E47) |
| TC-504 | A `.blend1` backup does not travel through a merge | pass |
| TC-505 | `space clear` + `restore` after a merge | pass — byte-identical |
| TC-506 | `gc` after several merges | pass — 82 reachable of 84, nothing deleted |

## TC-501 — the everyday case costs nothing

Anna reworks the level, Ben reworks the prop, neither knows about the other.

```
ben: fibula commit   -> refused, space is behind
ben: fibula sync     -> 1 written, 0 removed, 16.6 MiB   (0.14 s)
ben: fibula commit   -> 5 files, 1 read, 16.8 MiB chunked
```

Both edits survived, verified by hash: `level.blend` holds Anna's version,
`prop.blend` holds Ben's. **No conflict**, because the two artists touched
different files — which E44 predicts and which is the shape most of a real
project's work takes.

The store grew 10.97 MiB for two edited 16 MiB assets, because the two versions
share most of their chunks.

## TC-502 — a conflict on a real asset

Both replace `hero.blend` with a different editing state.

```
1 conflict(s) - your version is in place, theirs is in .fibula\conflicts:
  hero.blend (both changed)
```

| | |
|---|---|
| Conflict copy | `32B8475E5C43`, 16.78 MiB |
| Anna's file | `32B8475E5C43` — **identical** |
| Ben's working file | `A6584EEB6EA6` — his own version untouched |
| First bytes of the copy | `BLENDER` |

This is E46 doing exactly what it was written for. The copy carries the original
name and extension and is bit-for-bit what Blender wrote, so the artist can open
both and decide. Had the copy been `hero.blend.theirs`, this is the point where
it would have been useless.

`status` afterwards reported both halves of the situation:

```
modified (1):
  hero.blend
4 unchanged

1 unresolved conflict(s), their version kept in .fibula\conflicts:
  hero.blend
```

## TC-503 — a deletion does not beat an edit

Anna deletes `prop.blend`, Ben edits it. After Ben syncs:

```
1 conflict(s): prop.blend (changed here, deleted there)
prop.blend still present: True
```

E47 holds where it matters: the surviving file wins and the deletion is reported
rather than applied. A file wrongly kept is tidying; a file wrongly deleted
would have been the kind of loss this project exists to prevent.

## TC-505 — the round trip still closes after merging

A merged working directory is not a version, so it was worth checking that the
usual guarantees still hold across one:

```
safe to delete: 5 files
16.6 MiB of ignored files would stay in place
hero.blend A6584EEB6EA6 -> A6584EEB6EA6   identical
```

The ignored 16.6 MiB is the `.blend1` from TC-504, correctly left in place.

## What this run did not cover

- **A live Blender writing during a sync.** Everything here replays states
  captured earlier. Whether a save that lands mid-sync is handled cleanly is
  untested, and it is a realistic thing for an artist to do.
- **Opening the conflict copy in Blender.** Evidence is strong (byte-identical,
  correct magic, correct extension) but indirect.
- **Concurrency.** Anna and Ben never act at the same moment; TP-005's
  concurrent-client case was not repeated here.
- **Scale.** Five assets. The merge walks a map per side and sorts the union,
  which is fine at this size and unmeasured at 3,050.
- **A network share.** Unchanged from TP-005, and still the open item.
- **Renames through a merge.** Deliberately unsupported (E45's open list) and
  therefore not exercised.

## Follow-up

No defects. S5a's transfer half behaves as specified on real assets, which was
the precondition for starting the locking half (F-S5a-01, F-S5a-05 to 08).

Details in `reviews/TP-006-two-artists.md`.
