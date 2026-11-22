# ADR 20261122180000 · Standalone app default RAM is capped at 512 MB

- **Status:** accepted
- **Date:** 2026-11-22
- **Amends:** ADR-20261029234500 (default RAM capped at 1024 MB)
- **Related:** ADR-20261111114059 (agent budgets), `ProjectWorkloadDefaultRAMMB`

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A standalone app created without `ram_mb`, `resource_profile` or `vcpu` got
the plan's RAM capped at 1024 MB. Agents (agent keys, MCP sign-ins) deploy
without a size almost every time, so on Pro and Scale every agent-made app
started at 1024 MB. Apps are billed at their configured RAM, so customers
paid for a larger instance than the app needed. A production snapshot is
about 200 MB, and memory right-sizing only suggests a larger size; it never
shrinks one.

## Decision

1. `api.StandaloneAppDefaultMaxRAMMB` is 512 MB. A standalone app created
   without a size gets the plan's RAM capped at 512 MB: Free 256 MB, Hobby,
   Pro and Scale 512 MB. The same default applies to every caller, person or
   agent, so the price an agent previews is the price a person sees.
2. Everything else from ADR-20261029234500 stands: an explicit `vcpu` without
   `ram_mb` keeps the plan's RAM, `--ram` and `--size` still accept up to the
   plan's RAM, plan limits do not change, and project services keep the
   128 MB Compose default.
3. Existing apps keep their stored RAM. Nothing is resized.

## Consequences

- New Pro and Scale apps reserve and bill half the RAM they did. An app that
  needs more must ask for it (`--ram`, `--size`, `ram_mb`, `resource_profile`);
  the OOM explanation and memory right-sizing already point there.
- An agent's budget preview for a new app (`appRAM` in
  `pkg/apid/apidserver/agent_budget.go`) prices the smaller default.

## Verification

- `pkg/api`: `TestStandaloneAppDefaultRAMMB`.
- `pkg/apid/apidserver`: `TestCreateAppDefaultRAMIsCappedForScale`.
- Journey: `TestBazaarRegressions` check `standalone_default_ram`
  (`tests/journeys/bazaar_test.go`).
