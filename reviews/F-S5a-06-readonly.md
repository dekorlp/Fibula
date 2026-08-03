# F-S5a-06 · Read-only attribute

**Date:** 2026-08-03
**Branch:** feature/s5a-readonly
**Status:** Ready for review

## What was done

A file somebody else holds is marked read-only in the working directory, so the
DCC tool refuses to save and the conflict is discovered while working rather
than at commit time. Verified on Windows: writing to a held file fails with
`UnauthorizedAccessException`.

This completes S5a.

## Spec reference

- **E49** — the two levels: the attribute is the reminder, the commit check is
  the boundary. Advisory by design.
- **E17** — `space clear` must be able to delete read-only files, and that path
  runs through the dirty check

## Context

Last task of S5a, built after F-S5a-07 rather than before it, because the commit
check is the actual enforcement and this is the early warning on top of it.

## Changed files

- `client/readonly.go` — `ApplyLockAttributes`, `setReadOnly`, `makeWritable`
- `client/clear.go` — `deleteFile` clears the attribute first
- `client/readonly_test.go` — six tests
- `cmd/fibula/locks.go` — `refreshLockAttributes`, called after lock and unlock
- `cmd/fibula/sync.go` — called after a sync

## What to look at in review

**`os.Chmod` is the portable spelling.** On Unix it sets permission bits; on
Windows the Go runtime maps write permission to the read-only attribute, which
is exactly the flag a DCC tool checks. One call, both platforms, no build tags —
worth confirming you find that as clean as I do rather than too clever.

**`deleteFile` clears the attribute before removing.** On Windows a read-only
file cannot be deleted at all, and this is the dirty-check path: failing here
would leave the user believing a space was cleared when it was not. That is the
one place in this change where getting it wrong is a data-integrity problem
rather than an inconvenience.

**A project with locking off is left entirely alone.** `ApplyLockAttributes`
returns immediately. Reaching into a working directory to change permissions
Fibula never set would be gratuitous, and `TestLockingOffTouchesNothing` pins
it: a file the user made read-only themselves stays that way.

**Applying twice does nothing.** The check compares before writing, so the scan
that runs on every sync does no filesystem writes on an unchanged tree.

**`refreshLockAttributes` swallows its errors.** The attribute is a reminder;
failing to set it must not fail the operation that just succeeded. That is
deliberate and is the kind of decision worth disagreeing with — the counter-
argument is that a silent failure here removes the only warning the user gets
before commit time.

## What could still go wrong

- **Some tools drop the attribute.** Saving by delete-and-recreate loses it
  without meaning to, which is why this can never be the boundary. Stated in
  E49 and in the code.
- **It is not refreshed by `status`.** Only lock, unlock and sync apply it, so a
  lock somebody else takes while you are working is not reflected until your
  next sync. Adding it to `status` would make the most-used command do
  filesystem writes, which felt worse.
- **Unix permission bits are coarse.** `0644`/`0444` ignores whatever the user
  or their umask had set — group and other bits are overwritten. On a shared
  Unix box with non-default permissions this is destructive in a small way.
- **`ApplyLockAttributes` scans the whole tree.** Fine at studio scale, another
  full scan on a large project.
- **No test for the delete-and-recreate case**, because it is a property of the
  tool rather than of Fibula.

## Open questions

1. **Should the Unix side preserve the existing mode rather than forcing 0644?**
   Reading the current mode and clearing or setting only the write bits would be
   correct and is a few lines. I went with fixed modes for symmetry with the
   Windows path, which has no other bits to preserve — probably the wrong call
   if anyone runs this on a shared Unix machine.
2. **Assumption made:** that swallowing errors in `refreshLockAttributes` is
   right. See above.
