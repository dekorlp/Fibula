# F-S5a-08 · Lost-lock notification, and the author in the config

**Date:** 2026-08-03
**Branch:** feature/s5a-lock-notifications
**Status:** Ready for review

## What was done

A space now remembers which locks it believes it holds and reports any that are
gone. That closes E51's promise: the holder is told by the tool rather than by
whoever took the lock remembering to mention it.

Also moves the identity out of the environment: `Config.Author` in
`.fibula/config`, with `FIBULA_AUTHOR` still winning as an override. That was
the open question from F-S5a-07's review.

## Spec reference

- **E51** — detection is client-side, the same pattern as `Head.Base` against
  the ref: keep what we believe locally, compare against the store on contact,
  report the difference
- **E48/E49** — the identity a lock is attributed to

## Context

Completes S5a's locking half except for F-S5a-06, the read-only attribute.
Follows F-S5a-07, whose end-to-end run exposed the shared-identity problem this
change resolves properly.

## Changed files

- `client/lockstate.go` — `LostLocks`, `ForgetLostLocks`, the `held` state file
- `client/locks.go` — `Lock` and `Unlock` keep that state current
- `client/space.go` — `Config.Author`, read and written
- `client/lockstate_test.go` — six tests
- `cmd/fibula/locks.go` — `printLostLocks`, reported by `locks`
- `cmd/fibula/main.go` — `authorFor`, and `status` reporting losses too

## What to look at in review

**Losses are reported once, then forgotten.** An unread notice is useful; a
permanent one is noise, and noise is how people learn to skip output. The
trade-off is that a loss seen on a machine the user was not looking at is gone
from the other one — but the store still shows who holds the lock now, so the
information is not lost, only the prompt.

**`rememberLocks` runs after the acquire loop**, so a run that fails partway
records exactly what was taken rather than what was asked for.

**A deliberate `unlock` is not a loss.** `forgetLocks` drops the path, which is
why `TestReleasingALockStopsTrackingIt` exists — without it every release would
produce a notice on the next `status`.

**`authorFor` resolves environment, then space config, then OS user.** The
environment winning is deliberate: it lets a single run be overridden without
editing anything, which is exactly what the F-S5a-07 test needed. Whether that
precedence is right for a studio is worth a second opinion.

## What could still go wrong

- **The `held` file can drift.** It is local state with no verification: if a
  space is copied, both copies believe they hold the same locks and both will
  report a loss when one of them releases. Copying `.fibula` between machines is
  already a grey area (noted in F-B-04's review) and this adds a second thing
  that assumes it does not happen.
- **Nothing prunes it.** A path locked, lost, and forgotten leaves no trace,
  but a space that locks many files over a long time keeps the list of currently
  held ones only — which is correct, though it means `ForgetLostLocks` is the
  only way entries ever leave.
- **`status` now does store reads.** It was cache-only and deliberately fast
  (E17's reasoning); it now also lists locks and reads the ones this space
  holds. On a slow network store that is new latency in the command people run
  most.
- **No `cmd/fibula` test** for the notification path; verified by hand.

## Open questions

1. **Should `status` report lock losses at all, or only `locks`?** I put it in
   both because `status` is where people look first, at the cost above. If the
   latency turns out to matter, `locks` alone would still be correct.
2. **Assumption made:** that a released-by-someone-else lock (`Holder == ""`)
   deserves the same notice as a takeover. It means somebody force-released it,
   which is rarer than a takeover and just as worth knowing about.
