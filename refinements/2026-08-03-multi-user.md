# Multi-user on a shared store (S5a)

**Date:** 2026-08-03
**Status:** Decided
**Phase:** 1 (build it and use it — no compatibility guarantee)

What happens when two people share one store. Triggered by
[TP-005](../test-plans/TP-005-network-share.md) EC-401, which found that a
commit silently discarded another client's work, and by the fix for it
(F-B-04): that fix produces a refusal with no answer to "so what do I do now".
This entry is the answer.

Decision numbers continue the global sequence; the previous entry ended at E40.

---

## E41 — S5 splits into S5a and S5b, and only S5a is urgent

- **S5a — multi-user on a shared store:** transfer of work, conflicts, locking
- **S5b — remote:** reference server, `s3.Store`, Postgres index, auth,
  presigned URLs

*Rationale:* TP-005 established that two clients on a shared `fs.Store` already
work — locking, CAS, GC and restore all survive real SMB. For a studio of two to
five people a NAS share is a complete deployment, and the single-binary path
stays intact (CLAUDE.md). The server solves a scaling problem the target
audience does not have yet.

What it *does* have is a commit that refuses without a way forward.

**S5b is deferred**, not cancelled. It gets its own entry when the size of a
project makes an index worth its operational cost.

---

## E42 — The merge computation lives in the core

```
manifest.Merge(base, ours, theirs) (Manifest, []Conflict)
```

*Rationale:* it is arithmetic over three manifests — no IO, no store, fully
deterministic. That is the definition of the core layer (CLAUDE.md,
architecture), and it makes the merge rules golden-vector testable like any
other format behaviour.

## E43 — The base is `Head.Base`, not a lowest common ancestor

The F-B-04 fix records, per space, the deliberate version its working directory
descends from. That value is the merge base.

*Rationale:* with linear history it is exactly the common ancestor. Once merge
commits exist it is *a* common ancestor but not necessarily the nearest, and a
true LCA needs a graph walk whose cost grows with history.

*Why the imprecision is acceptable:* too old an ancestor produces **more**
reported conflicts, never a wrong result. The error direction points at a user
decision rather than at a silent loss, which is the side this project stands on.
An LCA can be added later without changing anything else.

## E44 — Merge rules, compared over FileIDs

For every path in `base ∪ ours ∪ theirs`:

| base | ours | theirs | Result |
|---|---|---|---|
| X | X | X | X — unchanged |
| X | **Y** | X | Y — ours only |
| X | X | **Z** | Z — theirs only |
| X | **Y** | **Y** | Y — both did the same, no conflict |
| X | **Y** | **Z** | conflict |
| X | — | X | deleted |
| X | X | — | deleted |
| X | **Y** | — | conflict: changed vs deleted |
| X | — | **Z** | conflict: deleted vs changed |
| — | Y | — | Y — added |
| — | — | Z | Z — added |
| — | Y | Z | Y if Y = Z, otherwise conflict |

Row 4 and the last row are what content addressing buys: **"both did the same"
is a single hash comparison, not a conflict.** Two people copying the same
texture into the project create no work.

All four conflict rows share one condition: both sides touched the same file.
In an asset project that is the exception.

## E45 — In phase 1 there is no divergence, so this is transfer, not merge

The F-B-04 check only admits a commit when `Base == Ref`. Every new version is
therefore a direct successor of the ref, and history **cannot** fork.

What actually happens when someone is behind is:

- `base` = `Head.Base`
- `ours` = the current working directory
- `theirs` = the ref version
- result = a new working directory

and the commit afterwards is an ordinary commit **with one parent**.

*Consequences:*

- No merge state in `.fibula`, no `--continue`, no `--abort`
- No two-parent merge commit in phase 1
- E11's parent *list* stays correct and stays unused — it is what makes real
  divergence expressible later without a format change

E44's rules are unaffected; only what happens with the result changes.

## E46 — Conflict copies live in `.fibula/conflicts/`

On a conflict the working directory keeps `ours`; `theirs` is written to
`.fibula/conflicts/<original path>`, under its original name and extension.

*Rationale:* an artist cannot resolve a binary conflict from metadata — the
files have to be opened. Any answer that only reports the conflict leaves them
to dig the other version out themselves.

*Why not a suffix in the working tree:* `hero.blend.theirs` is the obvious
shape and it is wrong, because Blender will not open it — the extension decides
which program accepts the file, and a suffix at the end makes the copy useless
to the one tool that could show it. `hero.theirs.blend` fixes that and buys
three other problems: the file sits in the scanned tree and must be excluded
from versioning, asset importers may pick it up, and it survives if the
resolution is forgotten. `.fibula/conflicts/` has none of these — the directory
is never scanned and clears in one move.

## E47 — Changed against deleted is resolved in favour of the file

There are no two contents to compare in these rows. The file stays in the
working directory and the deletion is reported as a conflict.

*Rationale:* deliberately asymmetric. A file wrongly kept is tidying; a file
wrongly deleted is data loss, and on an overlooked conflict the deletion would
win silently. Same direction as invariant 6.

---

## E48 — Locking is optional, opt-in per project

*Rationale:* Perforce makes locking the normal case, and the target audience
knows it that way. Fibula cannot: invariant 9 says local operations work without
a server, so requiring a lock before editing would make offline work impossible.

After E44 and E45, locking is no longer a substitute for merging. It prevents
the one case the transfer cannot resolve: both sides touched the same file.

## E49 — Editing is always allowed, committing is checked

| | |
|---|---|
| **Editing** | always permitted, online or off |
| **Committing** | refused when a foreign lock covers a changed path |

A lock is a **reservation, not an obligation**: committing without one is fine
as long as nobody else holds one. A solo developer never notices the feature
exists.

**A foreign lock additionally marks the local file read-only.** That is the
Perforce mechanism the audience knows, and it moves the discovery from commit
time to edit time — the DCC tool refuses to save.

The attribute is **advisory**. Anyone can remove it and Fibula must not rely on
it; the enforcement is the commit check. Three consequences follow:

- The attribute needs maintaining — set on checkout, restore and lock change,
  which is when the client talks to the store anyway
- Going offline means new locks are not seen, so the refusal message must say
  "locked since your last sync"
- Some tools save by deleting and recreating, which drops the attribute; this is
  exactly why it cannot be the boundary
- `space clear` must delete read-only files, which on Windows requires clearing
  the attribute first (E17's path)

## E50 — Locks live beside the refs, per file

Mutable state, not content-addressed — the same category as refs, with the same
consequences (E13.2): their own namespace in the store, created atomically with
`O_EXCL`, and in a later server they belong in the index rather than the blob
store. Contents: path, owner, timestamp, optional reason.

**Granularity is the file**; `fibula lock "levels/**"` creates individual locks
for every matching path. Directory locks would turn every check into a prefix
search and raise questions nobody wants to answer — whether a new file inside a
locked directory is covered, and whether the lock applies retroactively.

## E51 — Anyone may break a lock, visibly; expiry is configured in the store

No admin right. There is no authentication in phase 1 to carry a role, and in a
three-person studio "who is admin" is bureaucracy that gets worked around.

`--force` **transfers** the lock rather than deleting it, recording
`broken_from` and the time. The original holder is told on their next sync:

```
your lock on levels/hangar.blend is gone
  now held by ben, broken 2026-08-01
```

The mechanism is social, not technical. In a team this size, breaking a lock
visibly costs more than asking.

**Breaking a lock loses no work.** If the holder returns and commits, the F-B-04
check catches them, the transfer of E45 runs, and if both touched the same file
they get the conflict of E46. Their work was never at risk — it was in their
working directory and could be snapshotted at any time. **A lock saves effort;
it does not protect data.**

*Detection is client-side:* the space records which locks it believes it holds
and reports any difference against the store on contact — the same pattern as
`Head.Base` against the ref. The notice does not depend on anyone remembering
to send it.

**Expiry defaults to 14 days and is configured in the store, not the client.** A
client-local value would let A consider a lock expired that B still honours.
The store needs configuration regardless, because "locking is enabled for this
project" has to live somewhere a freshly initialized client will find it.

*The generous default is deliberate.* Expiry depends on a clock, and over a
network share the server stamps the file while the client reads its own time —
the risk TP-005 named for `breakStaleLock`. At 30 seconds a minute of skew is
fatal; at 14 days it is irrelevant. **Where a deadline may be long, it should
be.**

---

## E52 — One command: `fibula sync`

| Situation | Behaviour |
|---|---|
| up to date | nothing |
| behind, no local changes | fast-forward: update files, move `Base` |
| behind, with local changes | apply E44, write conflict copies, move `Base` |

Afterwards `Base == Ref` and `commit` works again — no special command, no
intermediate state.

**No `fetch`/`pull` pair.** On a shared store there is nothing to fetch; the
store is right there. The split would be copied from Git, where it models an
actual network step. If S5b later adds a server, `sync` can do that internally
and the user still sees one command.

## E53 — `commit` does not sync by itself

The staleness error names `sync` as the way out but does not run it. A commit
that quietly pulls someone else's changes into the working directory is the
same class of surprise that produced F-B-04 — visible this time, but still
unasked for. Two commands, two decisions.

**Leftover conflict copies are reported by `status`** for as long as they exist.
They are the only trace of an unfinished decision.

---

## Deliberately open

- **S5b** — server, `s3.Store`, index, auth (E41)
- **A true LCA** for the merge base (E43), if the approximation ever costs real
  conflicts
- **Real divergence and two-parent merges** — expressible in the format (E11),
  not reachable in phase 1 (E45)
- **Rename tracking through a merge.** A rename is "deleted here, added there";
  if the other side changed the content that yields a conflict plus a new file.
  Inelegant, safe, and cheaper than the well-known swamp of rename detection
  across both sides.
- **The clock-skew question** behind both `breakStaleLock` and lock expiry
  (E51). It has one cause and should be solved once, together with the real-NAS
  run TP-005 still owes.
