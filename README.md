# Fibula

Version control for binary assets — 3D models, textures, audio: the half Git
never handled. Named after the Roman brooch: what belongs together is held
together.

> ## ⚠️ Phase 1 — explicitly unstable
>
> Fibula is in phase 1: it is being built and used, and the requirements are
> coming out of that use rather than out of a drawing board. **There are no
> compatibility guarantees of any kind** — object format, manifest layout, CLI
> commands and storage layout may change without a migration path, and a store
> written today may be unreadable by tomorrow's build.
>
> Do not point this at data you cannot afford to lose a second copy of.
> Compatibility guarantees begin with phase 2 and `PROTOCOL.md`.

## What it is

A version control system for binary data **in its own right**, not a layer on
top of another one. There is **no dependency on Git, in any layer** — neither
technical nor conceptual, neither mandatory nor optional. No Git mode, no
manifest export to Git, no special paths. A project is versioned completely
with Fibula alone.

- **Assets** go into the Fibula store: content-addressed, chunk-based,
  globally deduplicated — across projects, not merely between versions of one
  file.
- **State** is a manifest pointing at chunk and blob hashes.
- **History** is Fibula's own version graph.

Comparing against Git stays useful for orientation — object model, content
addressing, the separation of core and hosting — and never as an integration
promise.

### Who it is for

Game developers, above all small studios and solo devs for whom Perforce is too
expensive and too much to operate. Secondarily European studios with
sovereignty requirements: self-hosted, EU, open standard.

### What it does differently

- **Two version levels, deliberately separated.** *Auto snapshots* are the
  safety net: time- or space-triggered, uncommented, expiring on a thinning
  schedule. *Deliberate versions* are named, commented and permanent. Perforce
  does not make this distinction.
- **Partial sync.** The client fetches the manifest, checks which chunks it
  already holds and downloads only the difference. This is where Git fails
  structurally on large binaries.
- **Space manager.** Reserve → work → versioned automatically → "clear space" →
  gone locally, present in the store. The metadata stays local, a few KB, so
  restoring is a pure download.
- **Dependency graph between assets** (level → mesh → texture). No established
  tool knows that a level breaks when a texture is replaced.
- **Offline by design**, not retrofitted: snapshot, diff and status work
  without a server.

## Status

Nothing here is usable yet. The repository currently holds the specification,
the backlog and the module skeleton; the object model is decided, the code that
implements it is being written slice by slice.

- [refinements/](refinements/) — **the source of truth.** Architectural
  decisions live here as numbered entries (`E1`, `E2`, …), referenceable from
  code and reviews. A deviation in code is a bug; a deviation in concept needs
  a dated addendum. Start at [refinements/index.md](refinements/index.md).
- [Backlog/](Backlog/) — work items, grouped into slices. Overview in
  [Backlog/index.md](Backlog/index.md).

With slices S0 to S4 done, Fibula is usable for its own purpose: an `fs.Store`
on a second disk or a NAS share is a fully valid deployment. No server, no S3,
no Postgres required.

## Repository layout

| Path | |
|---|---|
| `chunk/`, `hash/`, `manifest/`, `graph/` | core — deterministic, pure, no network, no database, no filesystem layout |
| `format/`, `tuning/` | the constants catalogue: format parameters vs. tuning parameters, strictly separated |
| `store/` | the storage abstraction; backends live behind it |
| `client/` | sync engine, local cache, space manager |
| `cmd/fibula/` | the command line client |
| `internal/` | server internals — not importable, and the AGPL side of the boundary below |

Dependencies point inwards, exclusively. The core knows nothing about network,
database or filesystem layout — the format lives there and nowhere else.

## License

**The client is MIT, the reference server is AGPL-3.0.** A studio should be
able to embed the client library into its pipeline without a second thought;
the reference server stays copyleft.

| | License |
|---|---|
| Core (`chunk`, `hash`, `manifest`, `graph`), `format`, `tuning` | MIT |
| Store abstraction including the `fs` and `s3` implementations | MIT |
| `client`, `cmd/fibula` | MIT |
| Reference server (`internal/server`, `cmd/fibula-server`) | AGPL-3.0 |

[`LICENSE`](LICENSE) at the root is the MIT text and covers everything except
the reference server. The server directory gets its own AGPL-3.0 `LICENSE` with
the first server file — it does not exist yet, and until it does every file in
this repository is MIT.

A hard layering rule follows: **MIT code must never import AGPL code.**
"Dependencies point inwards" is therefore also the license boundary, with the
server on the outside. Every new file must be unambiguously assignable to one
side; when in doubt it belongs to MIT, because moving from AGPL to MIT later is
expensive while the reverse is not.

## Contributing

Not yet open. The CLA question has to be settled before external contributions
can be accepted — without it, later commercial exploitation is blocked,
particularly on the AGPL side. There is deliberately no `CONTRIBUTING.md`
inviting contributions until that is in place.

Issues and discussion are welcome in the meantime.
