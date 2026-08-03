# F-S5a-01 · Store settings

**Date:** 2026-08-03
**Branch:** feature/s5a-store-settings
**Status:** Ready for review

## What was done

Project-wide configuration that belongs to the store rather than to a client:
whether locking is in force, and how long a lock survives. This is the
precondition for the rest of S5a — build the locks first and a freshly
initialized space would not know the rule exists.

It also brings Fibula its first store-side configuration at all; everything so
far lived client-locally in `.fibula/config`.

## Spec reference

- **E51** — expiry is configured in the store, not the client, so two clients
  cannot disagree about what has expired
- **E48** — locking is opt-in per project, hence a flag that defaults to off
- **E24** — extended by a dated addendum: a third interface, with the reasoning

## Context

First task of S5a's locking half, after the transfer half shipped
(F-S5a-02/03/04/09) and was exercised on real assets in
[TP-006](../test-plans/TP-006-two-artists.md).

## Changed files

- `store/settings.go` — `Settings`, `SettingsStore`, `DefaultLockExpiry`
- `store/fs/settings.go` — the filesystem implementation
- `store/fs/settings_test.go` — six tests
- `client/space.go` — the space carries a settings store and exposes it
- `client/settings_test.go` — three tests, including F-S5a-01's acceptance case
- `refinements/2026-08-02-object-model.md` — addendum to E24

## What to look at in review

**The third interface, and whether you agree with it.** E24 says "two
interfaces, not one". Settings are mutable and not content-addressed, which is
`RefStore`'s category — but a ref is compare-and-swapped by name and read
constantly, while settings are read whole and written by hand. Folding them
together would repeat the conflation E24 exists to avoid. The addendum argues
this; if you would rather have two more methods on `RefStore`, that is the
place to say so.

**`Settings()` reads from disk on every call.** Caching at open time would let a
long-running client act on "locking is off" after someone turned it on, and a
stale answer in that direction is exactly the one that must not be given. The
cost is a small file read per call, which is nothing next to what any operation
that needs it will do afterwards.

**Unknown keys are ignored rather than rejected** (`parseSettings`). A newer
client writing a field this one does not know must not make the store
unreadable — the opposite choice makes a mixed-version team unable to work.

**The default is fourteen days and the comment says why.** It is not a guess:
expiry depends on a clock, and TP-005 named clock skew over a network share as
the likely real-NAS defect. At thirty seconds a minute of skew is fatal; at
fourteen days it is irrelevant.

## What could still go wrong

- **`SetSettings` has no compare-and-swap.** Two concurrent writers overwrite
  each other. Acceptable for a value changed by hand perhaps twice in a
  project's life, and stated in the interface doc — but it is a difference from
  how refs are treated, and if settings ever grow a field that changes often it
  becomes wrong.
- **Nothing sets it yet.** There is no CLI command; the API is the only way in.
  That arrives with the lock commands (F-S5a-05), and until then the flag is
  reachable only from code.
- **No concurrent test.** `refs_test.go` hammers CAS from N goroutines; this has
  nothing equivalent, because there is no CAS to test.
- **The settings file is not covered by GC or verification.** It is not a
  content-addressed object, so nothing checks it for corruption. A truncated
  file would fail to parse rather than silently read as "locking off" — the
  parser errors on a malformed line — but a file truncated exactly at a line
  boundary would read as fewer settings.

## Open questions

1. **Should a malformed settings file be fatal or fall back to defaults?**
   Today a malformed *line* is an error, which fails every operation that reads
   settings. That is the safe direction for "is locking on", and arguably too
   strict for a store someone hand-edited badly.
2. **Assumption made:** that the truncation case above is acceptable. A checksum
   line would close it, and would be the first place in the system where
   non-object state is verified. It seemed disproportionate for two fields.
