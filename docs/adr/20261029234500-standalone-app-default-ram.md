# ADR 20261029234500 · Standalone app default RAM is capped at 1024 MB

- **Status:** proposed
- **Date:** 2026-10-29
- **Amends:** Hunt 14 H14-36 (a standalone app created without a size gets
  the plan's RAM)
- **Related:** spec §4.3 (plan RAM and vCPU shapes), `api.PlanResourceShapes`,
  issue #5879 (booked vCPU admission), `ProjectWorkloadDefaultRAMMB`

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

`POST /v1/apps` without `ram_mb`, `resource_profile` or `vcpu` gave the app
the plan's maximum RAM. On Scale that is 2048 MB (2056 MB billable with the
8 MB VM overhead) and four guest vCPUs. A customer deploying several
standalone Scale apps hit `capacity_unavailable` while their first
deployments primed, because each one reserved 2056 MB of a node's RAM
ceiling; lowering `--ram` or `--size` cleared it. Most apps use far less: a
production snapshot is about 200 MB.

## Decision

1. A standalone app created without a size gets
   `api.StandaloneAppDefaultRAMMB`: the plan's RAM, capped at
   `api.StandaloneAppDefaultMaxRAMMB` = 1024 MB. Free (256 MB), Hobby
   (512 MB) and Pro (1024 MB) are unchanged; Scale starts at 1024 MB.
2. An explicit `vcpu` without `ram_mb` keeps the plan's RAM, because an
   explicit vCPU must match the plan's joint RAM/vCPU shape.
3. Nothing else changes: `--ram` and `--size` still accept up to the plan's
   RAM, existing apps keep their RAM, and project services keep the 128 MB
   Compose default.

## Consequences

- A new Scale app reserves and bills half the RAM until its owner raises it.
  An app that needs more than 1024 MB must ask for it; the OOM explanation
  already points at `--ram`.
- Guest vCPU booking is unchanged: every Scale VM still books four vCPUs at
  admission. Admission by measured CPU is issue #5879.

## Verification

- `pkg/api`: `TestStandaloneAppDefaultRAMMB`.
- `pkg/apid/apidserver`: `TestCreateAppDefaultRAMIsCappedForScale`.
