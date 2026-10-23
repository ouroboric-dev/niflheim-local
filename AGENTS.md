# AGENTS.md: niflheim-local

The Niflheim realm's local flavor: the services a product runs on and its start script, for one machine with no cloud.

Follow the Yggdrasil invariants:
<https://github.com/ouroboric-dev/yggdrasil/blob/main/AGENTS.md#invariants>
This file only adds rules for this repo.

## This repo

- Build and test: `go test ./...`. Lint: `go vet ./...`. Both need Go packages, which come with the pack (T11).
- Interfaces live in `gungnir`, never here (spec §1).
- A runtime realm: services and the product start script, with no stack, leaf template or graph. `pack.yaml` comes
  with the content change.
- Architecture: `docs/architecture.md`.
