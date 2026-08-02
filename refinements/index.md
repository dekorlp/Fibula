# Refinements

All architectural and process decisions for Fibula. **The refinements are the
architecture** — a deviation in code is a bug, a deviation in concept needs a
dated addendum on the affected entry.

## Conventions

- Filename: `YYYY-MM-DD-<slug>.md`
- Decisions are numbered (`E1`, `E2`, …) and referenceable from code, reviews and
  other refinements — e.g. "object model E17"
- If a later decision changes an earlier one, the **original entry gets a dated
  addendum**; the original text stays untouched. Never silently reinterpret.
- An addendum is always its own commit with a `spec:` prefix and a justification

## Entries

| Date | Entry | Status | Contents |
|---|---|---|---|
| 2026-08-02 | [Object model](2026-08-02-object-model.md) | Decided | Object types, identity, manifest, version graph, space, dependency graph, store interface, serialization |

## Open — no refinement yet

- **Wire protocol** — deliberately deferred until after phase 1 (see CLAUDE.md,
  sequence). Will emerge as `PROTOCOL.md` from the distillation.
- **Concrete chunking parameters** — algorithm (Rabin vs. Buzhash), min/avg/max,
  window size. Turned into an efficiency rather than a correctness question by
  object model E3, and therefore decidable later.
- **Conflict and locking strategy** — binary assets have no merge; whether
  locking, conflict copies or "last one wins" applies is unresolved.
- **Extractor interface** — the concrete shape of the integration feeding the
  dependency graph (object model E19).
- **Auth beyond tokens and project roles** — SSO, groups, finer-grained
  permissions.
- **CLA** — required before external contributions (see CLAUDE.md).
