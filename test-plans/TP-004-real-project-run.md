# TP-004 · Real project run — Blender editing cycles

**Date:** 2026-08-03
**Task:** the outstanding half of the S0–S4 milestone (see `Backlog/index.md`)
**Branch:** feature/tp004-real-project-run
**Executed:** yes, against the built binary and a live Blender 5.2 instance
**Result:** pass. One defect found and fixed (EC-301), one behaviour recorded
that changes what we should recommend to users (EC-303)

## Why this plan exists

TP-001 and TP-002 both closed with the same sentence: *the real-project run is
still outstanding*. TP-002 could state what the dedup floor **is** — about one
chunk per changed region — but not whether that floor sits in the right place
for a `.blend`, because its corpus was synthetic.

This plan answers that. It uses a live Blender instance, real save operations,
and edits of the kind an artist actually makes.

## What is real here and what is not

**Real:** Blender 5.2 writing genuine `.blend` files through its own save path,
including the automatic `.blend1` backup; real geometry; real editing
operations; the built `fibula` binary against an `fs.Store`.

**Not real:** the scene was built by script rather than by an artist over weeks.
There are no external texture files, no linked libraries, no packed images, and
no `.psd`/`.exr` sidecars. A studio project is a *tree* of assets that reference
each other; this is one file that contains everything.

That limitation matters most for EC-303 below, where file size drives the
conclusion. It does not affect EC-301 or the mechanism results.

| | |
|---|---|
| Scene A | 173,224 vertices, 9 objects, 2 materials — **16.3 MiB** uncompressed, 4.4 MiB compressed |
| Scene B | 722,500 vertices, 1 object — **66.6 MiB** uncompressed, 21.8 MiB compressed |
| Stores | four plain directories (`fs.Store`), no server, no S3, no Postgres |
| Platform | Windows 11, Go 1.26.1, Blender 5.2 |

## Test cases

| ID | Case | Result |
|---|---|---|
| TC-301 | Blender saves the same scene byte-identically twice | **pass** — critical precondition, see below |
| TC-302 | `init` + `commit` imports a real `.blend` | pass |
| TC-303 | Smallest realistic edit (move one object) costs about one chunk | pass — 1.69 MiB of a 16.3 MiB file |
| TC-304 | Six editing cycles, cost tracks edit size | pass — 1.4 % to 31.7 % |
| TC-305 | Compressed vs uncompressed `.blend` over the same edits | pass — see EC-303 |
| TC-306 | The pattern holds at 4× file size | pass — and inverts the recommendation |
| TC-307 | `.fibulaignore` keeps `.blend1` out of the store | **fail, then pass** — see EC-301 |
| TC-308 | `space check` / `clear` with ignored files present | pass |
| TC-309 | Round trip after `clear` is byte-identical | pass |
| TC-310 | `log` shows the editing history in order | pass — 7 versions |

## TC-301 — the precondition nobody had checked

Everything else depends on this: **does Blender write the same bytes twice for
an unchanged scene?** If it embedded a timestamp, a session ID or serialized
memory addresses in file order, dedup between saves would be structurally
capped, and no chunker parameter could fix it.

Saved twice with no edit in between, uncompressed:

```
save 1: 68f263a366649757   16.34 MiB
save 2: 68f263a366649757   16.34 MiB   delta 0 bytes
```

Byte-identical. Fibula's premise holds for this format.

## TC-304 — cost tracks the size of the edit

Scene A, uncompressed, six consecutive cycles. "Growth" is the measured increase
of the store directory, not what the CLI reported:

| Cycle | Edit | Store growth | Of file size |
|---|---|---|---|
| 1 | move one prop | 1.69 MiB | 10.4 % |
| 2 | change a material colour | **0.22 MiB** | **1.4 %** |
| 3 | displace a terrain patch | 3.86 MiB | 23.6 % |
| 4 | add an object | 2.35 MiB | 14.0 % |
| 5 | delete an object | 5.25 MiB | 31.7 % |
| 6 | displace the whole terrain | 5.05 MiB | 30.5 % |

This is content-defined chunking behaving exactly as specified. A material
tweak costs 0.22 MiB against a 16.3 MiB file — the change is confined to a
small region of the file and the chunker re-synchronizes around it. A
whole-mesh displace costs 30 %, because that genuinely is how much of the file
changed.

**Deleting an object costs more than adding one** (31.7 % vs 14.0 %). Blender
appears to compact the file on delete, shifting everything after the removed
datablock; an addition mostly appends. Worth knowing, not worth acting on.

## EC-303 · Compression inverts the picture — **behaviour, no defect**

The same six cycles against a compressed `.blend`:

| Cycle | Uncompressed | Compressed |
|---|---|---|
| 1 move prop | 1.69 MiB (10.4 %) | 2.16 MiB (49.1 %) |
| 2 material | 0.22 MiB (1.4 %) | 0.89 MiB (20.3 %) |
| 3 local mesh | 3.86 MiB (23.6 %) | 2.16 MiB (49.1 %) |
| 4 add object | 2.35 MiB (14.0 %) | 2.29 MiB (50.6 %) |
| 5 delete object | 5.25 MiB (31.7 %) | 2.29 MiB (50.5 %) |
| 6 whole displace | 5.05 MiB (30.5 %) | 2.25 MiB (50.1 %) |

Compressed sits at **~50 % regardless of how large the edit was**. That is the
signature of a stateful compressor: a change at some offset alters every byte
after it, so the cost is set by *where* the edit landed, not by how big it was.
An edit in the middle costs half the file, every time.

Uncompressed costs track the edit. Compressed costs track the file.

**At scene A's size compressed still wins overall** (16.5 MiB of store after six
cycles versus 34.8 MiB) — the 4× smaller baseline more than pays for the worse
incremental behaviour. That inverts with size. Scene B, 4× larger:

| Edit | Uncompressed | Compressed |
|---|---|---|
| add one small object | 3.78 MiB (5.7 %) | 2.84 MiB (13.0 %) |
| local mesh edit | 5.37 MiB (8.1 %) | **9.40 MiB (43.2 %)** |

Comparing the same local mesh edit across both scenes: uncompressed grew by
factor **1.4** for a 4× larger file, compressed by factor **4.4**. Uncompressed
is approximately O(size of edit); compressed is O(size of file).

Extrapolating scene B's rates, the cumulative totals cross at roughly **29
editing cycles** — beyond that the uncompressed store is smaller in absolute
terms despite starting three times larger. A real project reaches 29 saves in
about a day.

**Recommendation for the README, once there is one to write:** save `.blend`
files uncompressed when versioning them with Fibula. This is a documentation
matter, not a code change — Fibula behaves correctly in both cases.

**Honest limit:** two data points at scene B, and one of them (adding an object)
is unrepresentatively cheap for the compressed case because the new datablock
lands near the end of the file. The direction is solid; the crossover figure of
29 is an estimate, not a measurement.

## EC-301 · A BOM disables the first ignore rule — **defect, fixed**

`.fibulaignore` was written containing `*.blend1`, and `scene.blend1` was
versioned anyway — 16.6 MiB of backup file in the store on every cycle.

The file, written by PowerShell's `Out-File -Encoding utf8`:

```
ef bb bf                    <- UTF-8 BOM
2a 2e 62 6c 65 6e 64 31     <- "*.blend1"
0a
```

`bufio.Scanner` handles CRLF correctly, so line endings were never the problem.
The BOM is: `parseIgnoreLine` trims only trailing spaces and tabs, so the first
rule became `﻿*.blend1` and matched nothing. Every later rule worked, which
is what makes it nasty — the file looks like it is being read, because it is.

**Why this is a real defect and not a bad test setup:** on Windows, a BOM is the
default rather than the exception. PowerShell 5.1's `Out-File -Encoding utf8`
writes one, as do older Notepad versions and several editors. The target
audience is largely on Windows. The failure is silent, and its symptom —
"my backups are in the store" — points nowhere near the cause.

Severity is **warning, not critical**: it versions too much rather than too
little, so nothing is lost. A negation rule (`!keep.blend`) on the first line
would be dropped too, causing a file to be ignored that should not be — still
not data loss, since ignored files are left in place, but the same silence.

Fixed by stripping a leading BOM in `ParseIgnore`, with a regression test.

## EC-302 · `.blend1` doubles local disk use — **observed, correct behaviour**

After `space clear`:

```
deleted 2 files, 16.6 MiB freed
16.6 MiB of ignored files not deleted, --include-ignored to include them
```

Blender's automatic backup is exactly as large as the file itself, so a cleared
space still holds half of what it did before. The behaviour is correct and
specified (E18): the `.blend1` was never in the store, and deleting it would be
destroying data Fibula cannot recover.

The message already names the flag that would include them, which is the right
resolution. Recorded because "clear freed half of what I expected" is a
predictable support question for this specific audience.

## TC-309 — round trip

```
before clear: A6584EEB...2688FC
after restore: A6584EEB...2688FC   identical
```

`space check` correctly reported the space as clearable, `clear` freed the two
versioned files, `restore` reproduced the working directory, and the `.blend`
hashed identically. `log` listed all seven versions in order with their
messages.

## What this run still did not cover

- **A project tree, not a single file.** No external textures, no linked
  libraries, no packed images. The dependency graph (S6) exists precisely for
  that structure, and this run says nothing about it.
- **Other DCC formats.** `.psd`, `.exr`, `.fbx`, `.wav` and Unreal `.uasset`
  are untested. TC-301's determinism result is about Blender specifically and
  must not be assumed for any of them — it is exactly the kind of property that
  differs per application.
- **A NAS share.** Still outstanding from TP-001 and TP-002. Unchanged.
- **Long time spans.** Every version here is minutes old; the retention buckets
  still have only unit-test coverage.
- **An artist's real editing rhythm.** Six scripted edits are not a work
  session. Real sessions save far more often, with many near-identical states —
  which should favour Fibula more than this run shows, not less.

## Follow-up

One defect fixed in this branch (EC-301). EC-303 is a documentation item for
the README rather than a code change, filed as **F-B-03**. The milestone's real
project half is now discharged; the NAS share half remains.

Details in `reviews/tp004-real-project-run.md`.
