# internal/

Everything in here is **not** part of the public API and cannot be imported by
anyone outside this module. That is deliberate: what studios embed into their
pipeline — core (`chunk`, `hash`, `manifest`, `graph`), the store abstraction,
`client` — lives in importable packages at the repository root. Server
internals live here.

## License boundary

This directory is where the **AGPL-3.0** side of the repository will live
(`internal/server`, together with `cmd/fibula-server`). Everything outside it is
MIT.

`internal/server/LICENSE` (AGPL-3.0) is added with the first server file. Until
then there is nothing to license here — every file currently in the repository
is MIT.

**MIT code must never import AGPL code.** The rule "dependencies point inwards"
is also the license boundary, and the server sits on the outside. When in doubt
a new file belongs on the MIT side: moving from AGPL to MIT later is expensive,
the reverse is not.

See [CLAUDE.md](../CLAUDE.md) § License and [README.md](../README.md).
