# niflheim-local

The Niflheim realm's local flavor: the services a product runs on and its start script, for one machine with no cloud.
`pack.yaml` provides Postgres 18 (`postgres`) and SeaweedFS 4.45 (`s3`), each pinned by image digest.

Part of [Yggdrasil](https://github.com/ouroboric-dev/yggdrasil), the agentic factory by
[Ouroboric](https://github.com/ouroboric-dev/ouroboric). Work on it from the Yggdrasil umbrella,
which holds every component as a submodule and builds them together through `go.work`.

## Build and test

```bash
GOWORK=off go test -race ./...   # loads pack.yaml and checks the services it provides
go vet ./...
```

## Docs

- Architecture: [docs/architecture.md](docs/architecture.md)
- Engineering spec: <https://github.com/ouroboric-dev/yggdrasil/blob/main/docs/2-engineering/specs/2026-10-11-yggdrasil-v0-spec.md>

## Contributing

Run `uvx pre-commit install` once after cloning, so `gitleaks` checks each commit for secrets.

## Licence

Code is AGPL-3.0-only. `docs/`, this README and `AGENTS.md` are CC-BY-4.0. See `REUSE.toml`.
