---
title: niflheim-local architecture
status: draft
date: 2026-10-23
related: [https://github.com/ouroboric-dev/yggdrasil/blob/main/docs/2-engineering/specs/2026-10-11-yggdrasil-v0-spec.md]
style: reference
---

# niflheim-local architecture

The Niflheim realm's local flavor: the services a product runs on and its start script, for one machine with no cloud.

## Responsibilities

- Is a runtime realm (spec §9.4, as changed by the gallery runtime): it provides the services a product runs on and
  its start script, and no leaf, stack, template or check ladder.
- Will hold `pack.yaml`, which names the services (Postgres 18 as `postgres` on port 5432, SeaweedFS as `s3` on
  port 8333, both by image digest), says which need each one provides (`provides`) and names the product start
  script (`product.start`).
- Will hold the product start script, which Odin's `init` writes at a project's root as `start.sh`.
- Has no graph. A build realm declares what its leaves need (`needs`); this realm answers with the services.
- Its templates are copied into products, so they are MIT-licensed (`templates/`).

## Depends on

- Gungnir, for the pack schema.
- Odin, which reads the pack, starts the services and writes the start script.

## Source

The v0 engineering spec. When this page and the spec differ, the spec wins.
