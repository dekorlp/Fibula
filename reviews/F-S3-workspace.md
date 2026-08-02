# F-S3-01 … F-S3-06 · Workspace — local state, space, snapshots

**Date:** 2026-08-02
**Branch:** claude/kannst-loslegen-zbfu63
**Status:** Ready for review

## What was done

Slice S3 complete, plus a dated addendum resolving a contradiction in the
object model. This is the first slice a human touches: `fibula init`, `status`,
`snapshot`, `space check`, `space clear` and `restore` work end to end against
a real directory.

It also contains F-S3-04, the dirty check, which the backlog calls the trust
question of the entire space feature. Writing its tests found two real holes,
both described below.

## Spec reference

| Task | Implements |
|---|---|
| **F-S3-01** | E15 (a space is the working copy), E16 (`.fibula/` contents, status cache, chunk lists) |
| **F-S3-02** | E18 (gitignore syntax) |
| **F-S3-03** | E12 (snapshot and deliberate version are one type), E16 |
| **F-S3-04** | E17 (two levels of guarantee), CLAUDE.md invariant 6 |
| **F-S3-05** | E15, E18 |
| **F-S3-06** | CLAUDE.md § 4 Security |

**Addendum:** `refinements/2026-08-02-object-model.md` gains a dated addendum to
E21 — the GraphID sits on the version, not on the manifest. See "Decisions I
made rather than deferred" below.

## Context

Follows `reviews/F-S2-store.md`. That review recorded that the dirty check must
call `VerifyFileContent` rather than rely on `Get`; what it actually needed
turned out to be a batched `Exists` over chunk keys, for the reason given in
point 3.

## Changed files

| File | |
|---|---|
| `client/space.go` | `.fibula/` layout, config, head, atomic local-state writes |
| `client/cache.go` | the status cache and the locally remembered chunk lists |
| `client/ignore.go` | `.fibulaignore`, gitignore syntax, hand-written matcher |
| `client/scan.go` | the working-directory walk and `status` |
| `client/snapshot.go` | snapshot, the snapshot chain, the checked-out manifest |
| `client/dirty.go` | `CheckClear` — the three checks of E17 |
| `client/ignored.go` | measuring what clearing will leave behind |
| `client/clear.go` | `Clear`, empty-directory pruning, the budget |
| `client/restore.go` | `SafeJoin` and restore |
| `cmd/fibula/main.go` | the CLI |
| `store/fs/fs.go`, `refs.go` | `Create` split from `Open` — see point 1 |
| `errs/errs.go` | four new sentinels |
| tests | `dirty_test.go`, `clear_test.go`, `restore_test.go`, `ignore_test.go`, `space_test.go` |

## Two holes the tests found

### 1. A vanished store looked exactly like an empty one

`TestClearAbortsWhenTheStoreIsUnreachable` failed on the first run, and the
reason was worse than the test: `fs.Open` created the store layout on demand.
So a store on an unmounted network share came back as a **valid, empty store**.
Every local file then hashed to a FileID no version referenced, everything was
classified as unversioned, no problem was reported — and `Clear` would have
re-snapshotted the whole tree into the mount point and deleted the originals.

Two fixes, because one was not enough:

- **`fs.Create` is now separate from `fs.Open`.** Opening refuses a directory
  that is not already a store. "No data in the store" and "no store" are
  different answers and only one of them is safe to act on.
- **`CheckClear` positively confirms the store holds the head version.** The
  space asks a question it knows the answer to: it has a head, so the store
  must be able to produce that version object. This catches the case where the
  store handle was opened before the share went away, which the first fix does
  not.

### 2. The Windows-only path rule was only ever tested on Windows

`checkReservedNames` started as a `runtime.GOOS` check and an early return,
which means the entire rule — device names, trailing dots and spaces — ran on
exactly one CI runner and on no developer machine. The platform is now a
parameter, and `TestWindowsReservedNameRules` exercises both branches
everywhere.

While fixing the test I also found it was asserting nothing in the negative
direction: `if tc.wantErr && ...` silently skipped every case expected to pass.
Both directions are asserted now, and two cases that I had marked as passing
turned out to be rejections the code makes deliberately.

## What to look at in review

### The line between Status and CheckClear

This is the design decision of the slice, and it is deliberately stark:

| | `status`, auto snapshot | `space clear` |
|---|---|---|
| Data source | the status cache | a full re-hash, cache ignored |
| Store existence | assumed | positively confirmed, batched |
| When in doubt | continue | abort |

`TestClearDetectsAChangeTheCacheLiesAbout` is the one to read: it edits a file
to the same length and puts the old mtime back, asserts that `status` is
**fooled** — that assertion is the premise of the test — and then asserts that
`CheckClear` is not.

### Unversioned files are snapshotted, not refused

E17 is explicit about why: aborting with "there is something unversioned here"
is technically correct and teaches users to reach for `--force`, which is how a
safety feature stops being one. So `Clear` snapshots them, **re-runs the whole
check**, and only then deletes. Re-checking rather than trusting the snapshot
is the point — the snapshot wrote to the store, and the only acceptable
evidence that it arrived is asking the store again.

### `SafeJoin` refuses rather than resolves

A path whose parent chain passes through a symlink is rejected outright, not
resolved and compared. That is stricter than necessary, and deliberate: the
strict form has no window between the check and the write that a
resolve-then-compare would have. A working directory where a parent of an asset
is a link is not a state Fibula can restore into meaningfully anyway.

Restore also validates **every** path before writing **any** file, so a
manifest with one hostile entry cannot write the harmless entries preceding it
and only then fail.

### The ignore matcher is hand-written

Roughly 90 lines instead of a dependency. Supported: comments, negation with
last-match-wins, directory-only patterns, root anchoring, `*`, `?`, `**` and
character classes. Deliberately unsupported: backslash escaping, since a
backslash cannot occur in a Fibula path at all (E8) and a pattern needing one
could never match.

One test of mine was wrong here too: I expected leading whitespace to be
trimmed from a pattern. Git does not trim it — only trailing whitespace — and
the implementation was right.

## Decisions I made rather than deferred

**The GraphID addendum.** I raised the E21-versus-E11/E33 contradiction in the
S1 review, the S2 review and both pull requests without an answer. Since S1 had
to pick one to compile, the contradiction has been load-bearing for three
slices. Leaving the spec self-contradictory while building on it is worse than
resolving it on the record, so it is now a dated addendum with the reasoning,
and the original text is untouched. **Reversing it costs one field on each of
two structs plus their golden vectors** and stays cheap until a store holds
versions carrying graphs — which phase 1 does not yet produce. If the manifest
was the intent, say so and I will write the counter-addendum.

**Auto snapshot expiry is a flat six months.** E14's thinning schedule (hourly
for a day, daily for a month, weekly for six months) is a property of
expiry-driven pruning, which is S4. Rather than invent half a scheduler, an
auto snapshot gets the outer bound of that schedule, so nothing created here
can expire before the pruner that understands the schedule exists.

## What could still go wrong

- **The done-criterion of F-S3-01 is tested by proxy.** It says "`status` on an
  unchanged 200 GB tree completes without rehashing anything". What
  `TestSnapshotReusesTheCache` asserts is that a second snapshot reads zero
  files and uploads zero bytes, and `Status` never opens a file at all by
  construction. Neither is a 200 GB tree. The scaling claim is untested.
- **No time or budget trigger actually fires anything.** `Budget` reports and
  the CLI suggests, but nothing runs snapshots on a timer — E15's
  time-triggered snapshot needs a daemon or a file watcher, and CLAUDE.md is
  emphatic that a goroutine without a lifecycle is not acceptable. That belongs
  in its own task.
- **The snapshot chain is the only reachability root that matters today.**
  `reachableFiles` walks every local ref, which currently means the snapshot
  chain alone. When S4 adds deliberate refs and checkout this needs revisiting
  — in particular, a version reachable only from a *remote* ref is not counted,
  which is correct for now and will not be after sync exists.
- **`Clear` deletes file by file with no rollback.** If deletion fails halfway,
  what was deleted is gone (recoverably, from the store) and what was not stays.
  The check has already passed at that point, so nothing unrecoverable is lost,
  but the working directory is left in a mixed state.
- **`pruneEmptyDirs` removes directories the user created.** An empty directory
  that was never versioned — a manifest has no directories, only paths — is
  indistinguishable from one left empty by clearing, and it goes. This is
  visible behaviour I chose; it may be the wrong call for people who lay out
  empty folder structures ahead of time.
- **The ignore matcher is recursive over `**`.** A pathological pattern with
  many `**` segments against a deep path could be slow. Patterns come from a
  file the user wrote, so this is not an attack surface, but it is not bounded
  either.
- **Local state has no format version.** `.fibula/config`, `head` and `cache`
  are tab-separated text with no framing line, because they are never shared,
  never hashed and free to change. That also means an older client meeting a
  newer space fails with a parse error rather than a clear message.
- **The CLI has no `--help` per command, no flags parsing worth the name, and
  no progress output.** `space clear` on 200 GB prints nothing for two minutes
  while it re-hashes.

## Open questions

1. **Is the GraphID addendum the right resolution?** See above. This is the one
   I would most like an answer to, even though it is now recorded.
2. **Should `pruneEmptyDirs` exist at all?** It makes a cleared space look
   cleared, at the cost of removing empty directories the user may have made on
   purpose.
3. **`space clear` currently requires no confirmation.** It refuses unless
   everything is provably recoverable, so it is safe by construction — but it
   is still an irreversible-looking command that takes no `--yes`. Deliberate,
   or too brave?
4. **Author identity comes from the operating system user.** E30 says the
   server validates the author and rejects a mismatch, so this is a placeholder
   until there is an account to check against.
