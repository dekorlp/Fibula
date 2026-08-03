# F-S5a-05 (part 1) · Lock storage

**Date:** 2026-08-03
**Branch:** feature/s5a-locks
**Status:** Ready for review

## What was done

The store side of file locks: `store.Lock`, `store.LockStore` and the
filesystem implementation. Acquiring is atomic, transferring records who lost
the lock, and paths are validated before anything touches the disk.

**The CLI commands are not in this change.** `lock`, `unlock`, `unlock --force`
and `locks` follow in part 2, the same way `manifest.Merge` shipped before
`fibula sync` used it. Nothing calls this yet.

## Spec reference

- **E50** — locks live beside the refs, per file, created atomically
- **E51** — `Transfer` keeps `broken_from` so the original holder is told by the
  tool rather than by a person who might forget
- **E13.2** — `O_EXCL` create as the atomic primitive, the same one the ref lock
  uses and the one TP-005 exercised under real contention over SMB
- **CLAUDE.md § 4** — path safety; a lock path is untrusted input

## Context

Second task of S5a's locking half, after F-S5a-01 put the settings in the store.

## Changed files

- `store/lock.go` — `Lock`, `LockStore`, `Lock.Expired`
- `store/fs/locks.go` — the filesystem implementation
- `store/fs/locks_test.go` — nine tests
- `store/fs/settings.go` — `replaceFile` extracted for shared use
- `errs/errs.go` — `ErrLockHeld`, `ErrLockNotFound`

## What to look at in review

**`Lock.Expired` lives on the struct, not in the store.** The deadline comes
from the project settings, which the store does not read — and keeping the
comparison out of the backend means there is exactly one place where lock
expiry depends on a clock. That matters because clock skew over a network
share is the open risk TP-005 named.

**`Transfer` replaces rather than deletes and re-creates.** Deleting first
would leave the path briefly free for a third party to take, which is the one
way a forced break could lose a lock to somebody who was not involved.

**Path validation in `pathFor`.** A lock on `../../etc/passwd` would otherwise
write outside the store. It is the same rule restore follows, in a place where
it is easier to forget because nothing is being restored — worth confirming the
test covers what you would expect.

**Re-acquiring your own lock is an error, not a no-op.** `Acquire` refuses
regardless of who asks. Taking a lock you already hold means you lost track of
something, and saying so beats hiding it.

## What could still go wrong

- **Nothing calls it.** The commands, the read-only attribute, the commit check
  and the expiry handling are all still to come (F-S5a-05 part 2, 06, 07, 08).
  Until then a lock is a file nobody writes.
- **`Owner` is a name, not an identity.** There is no authentication in phase 1,
  so anyone can claim to be anyone. Stated in the type's doc comment. It makes
  locks a coordination aid, which is what E51 says they are.
- **A crash between `O_EXCL` create and the write leaves an empty lock file.**
  `Acquire` removes it on a failed write, but a killed process cannot. The file
  would then fail to parse and `List` skips it — but `Acquire` on that path
  keeps failing, because the file exists. It needs the expiry path (F-S5a-08) to
  clear, and that is only sound once expiry is implemented.
- **`List` walks the whole directory.** Fine at the scale a studio locks files;
  it is not an index.

## Open questions

1. **Should an unparseable lock file block the path or be ignored?** Today
   `Acquire` fails on it (the file exists) while `List` skips it, which is
   inconsistent. I would rather make it visible than silently reclaimable, but
   the crash case above means it can happen without anybody misbehaving.
2. **Assumption made:** that `Transfer` should keep the previous holder's
   `Reason`. It describes work that is no longer being done, but discarding it
   loses the only context about what the broken lock was for.
