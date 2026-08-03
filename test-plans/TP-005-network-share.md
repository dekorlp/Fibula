# TP-005 · Network share — SMB store, and what it uncovered

**Date:** 2026-08-03
**Task:** the last outstanding item of the S0–S4 milestone (see `Backlog/index.md`)
**Branch:** feature/tp005-network-share
**Executed:** yes, against the built binary over an SMB path
**Result:** the SMB mechanics pass. **A critical defect was found that has
nothing to do with SMB** — a commit silently discards another client's work
(EC-401). Filed, not fixed: the fix is a design decision, see below.

## Why this plan exists

TP-001, TP-002 and TP-004 each closed naming a network share as untested.
`store/fs` is written for one — the `O_EXCL` lock file, the rename-based ref
update and the flush in `Put` all exist because two Fibula binaries against one
NAS share is a stated scenario (E13). None of it had ever run on a network
filesystem.

## What is real here and what is not

The store lived on `\\localhost\C$\...`, reached through the SMB redirector and
the local SMB server. **Every store operation went through the real SMB
protocol stack**: create-with-`FILE_CREATE` disposition for the lock file,
rename, flush, directory enumeration.

**Not real, and it matters:**

- **No network.** Loopback has no latency, no packet loss, no dropped
  connections and no session reconnects. A real NAS has all four, and a
  reconnect in the middle of a rename is exactly the case `Put`'s atomicity
  claim would have to survive.
- **One clock.** `breakStaleLock` compares a lock file's `ModTime` against
  `time.Since`. Client and server being the same machine makes that comparison
  trivially consistent. On a real NAS the server stamps the file and the client
  reads its own clock — a skew of more than 30 s (`lockStale`) either expires
  live locks or never expires dead ones. **This is the single most likely
  real-NAS defect and this run cannot see it.**
- **An administrative share**, not an ordinary one. Same protocol, different
  ACL path.
- **One machine.** SMB oplock breaks between two hosts contending for the same
  file were never exercised.

| | |
|---|---|
| Asset | the 16.6 MiB `.blend` from TP-004 |
| Store | `\\localhost\C$\...` |
| Platform | Windows 11, Go 1.26.1 |

## Test cases

| ID | Case | Result |
|---|---|---|
| TC-401 | `init` + `commit` against a UNC store | pass — 0.53 s for 16.6 MiB |
| TC-402 | The store is written on the share, not locally | pass — 18 objects, 16.59 MiB |
| TC-403 | Four concurrent clients, one store | pass mechanically — **see EC-401** |
| TC-404 | Ref CAS rejects a stale update | pass — one client rejected with an explicit conflict |
| TC-405 | `space check` / `space clear` over SMB | pass |
| TC-406 | `restore` over SMB is byte-identical | pass — 0.17 s, hashes match |
| TC-407 | `gc` over SMB | pass — 32 reachable of 37, 5 spared by the grace window |
| TC-408 | No lock files or temporaries left behind | pass — 0 and 0 |
| TC-409 | Does `snapshot` have the same staleness gap as `commit`? | **no** — see below |

The SMB mechanics hold. `O_EXCL` create works as a cross-process mutex over the
protocol, rename commits a ref atomically, and nothing is left behind.

## EC-401 · A commit silently discards another client's work — **critical, filed**

Found while checking TC-403. Four clients started from the same committed state
and committed at once: three succeeded, one was rejected with a ref conflict.
Three winners from one starting point is one too many, so the history was
checked rather than assumed:

```
$ fibula diff <client-1-commit> <client-3-commit>
+ client3.txt
- client1.txt
```

Client 3's commit names client 1's as its parent **and deletes its content**.
The diff from baseline to head contains only `client4.txt`. Three clients
committed; the work of two is gone from head.

### It is not a race, and not SMB

Reproduced with no concurrency at all, on a plain local store:

```
alice commits alice.txt          -> version 5645be9912c3
(3 seconds pass, nothing running)
bob   commits bob.txt            -> version 867bd76cce5a

head contains: asset.txt, bob.txt        alice.txt is gone
history:       bob -> alice -> baseline  (looks perfectly linear)
```

### Cause

`commitTarget.parentOf` (`client/snapshot.go:220`) resolves the parent as
`s.refValue(ctx, s.currentRef())` — **the ref's current value in the store**.
The manifest, meanwhile, is built from the local working directory. Nothing
compares the two.

So a commit says "my parent is whatever head is now" while describing a tree
that never contained head's changes. The ref CAS then succeeds, because the
value being replaced is genuinely the one that was read. The lock works, the
CAS works, the atomic rename works. The layer above them is what is missing:
**nobody asks whether this working directory descends from head.**

The space already stores the checked-out VersionID (E16), so the information
needed for the check is present and unused.

### Why this is critical

E13.3 states that a non-fast-forward is a conflict and "never an automatic
overwrite". This is an automatic overwrite. It is the silent lost update E13
exists to prevent — one level above where the defence was built.

The old versions stay reachable through the parent chain, so nothing is
destroyed in the store and GC will not collect them. But head is wrong, `log`
looks correct and complete, and the user gets no signal. For the two-artist
studio this project targets, that is the first scenario they will hit.

### Correction to TP-002

TP-002's TC-114 ran four concurrent clients locally, observed "two won, two
failed with an explicit ref conflict" and recorded *"the CAS did its job under
real contention"*. That reading was too generous: the CAS did do its job, but
the two winners almost certainly overwrote each other exactly as above. The
plan checked for corruption and lost ref updates, not for lost content.

Per the test-plan rules TP-002 is not modified. This paragraph is the
correction.

### What is *not* affected — TC-409

`snapshot` was checked for the same gap and does not have it. Two clients from
one baseline, snapshotting two seconds apart:

```
8678eaeacbf6  alice
48bf411df946  bob
2 snapshots        - and both clients see both
```

`snapshotTarget.publish` compare-and-swaps against a **zero** old value, i.e.
"this ref must not exist yet", so every snapshot lands on its own ref and no two
can collide. The safety net works in the multi-client case even though the
deliberate-version path does not.

That materially limits the damage: nothing is destroyed in the store either way
(old versions stay reachable through the parent chain), and where snapshots are
running, the displaced work is also reachable through the timeline. What is
wrong is **head**, and the fact that nothing says so.

### Why it is filed rather than fixed

The minimal fix — reject a commit when the checked-out VersionID differs from
the ref — is a few lines. The problem is what the user does next: there is no
`pull`, no `fetch`, no merge. The only way forward would be `checkout`, which
discards their working directory. Trading silent data loss for a dead end is an
improvement, but it is a **design decision about sync semantics**, and sync
semantics are S5.

Filed as **F-B-04** with both options written out.

## What this run still did not cover

- **A real NAS.** The clock-skew case around `lockStale` is the concrete risk
  and is untestable on loopback. Everything in "what is not real" above stands.
- **Interruption.** No connection was dropped mid-write. `Put`'s atomicity over
  SMB is argued, not demonstrated.
- **Contention from two machines**, hence no oplock behaviour.
- **Linux and macOS clients** against an SMB share, where the redirector is a
  different implementation entirely.
- **Throughput on a real link.** 0.53 s for 16.6 MiB over loopback says nothing
  about a gigabit share, let alone WiFi.

## Follow-up

The milestone's network-share item is discharged to the extent loopback SMB can
discharge it: the store mechanics work over the protocol. A real-NAS run stays
on the list, with the clock-skew case named as its first target.

**F-B-04** is filed and is the most severe open item in the backlog.

Details in `reviews/tp005-network-share.md`.
