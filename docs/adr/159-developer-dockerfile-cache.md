# ADR-159 · Dockerfile developer cache parity

- **Status:** accepted
- **Date:** 2026-09-06
- **Decision:** enable the existing tenant-scoped BuildKit dependency cache for
  `gregale dev` Dockerfile builds, with the same disposable-cache and cold-build
  fallback contract as Railpack builds.

## Context

ADR-153 made repeated Railpack developer builds fast, but explicitly left
Dockerfile projects on the cold path. The guest already uses BuildKit's local
cache importer/exporter for developer builds; the remaining gap was the
builder-side eligibility gate, so Dockerfile users paid a full dependency
reinstall on every sync.

## Decision

Developer Dockerfile builds use the same account, developer-app, workspace,
framework, and runtime-base partitioning as Railpack builds. BuildKit remains
the authority for instruction and input invalidation: changing the Dockerfile,
context, lockfiles, base image, or build arguments can only reuse matching
records from that partition.

Cache data is disposable and never contains an executable VM or cross-tenant
state. Import, export, validation, size, expiry, and publication failures
degrade to a cold build and never fail an otherwise valid deployment.
Production and pull-request preview builds remain ineligible.

## Consequences

Custom Dockerfile projects now get the same warm developer loop as Railpack
projects while retaining ephemeral Firecracker builder VMs. The CLI's existing
`dependency cache restored` / `dependency cache cold` progress lines apply to
both build paths.

## Validation

Pure tests pin Dockerfile eligibility and framework partitioning. The existing
guest BuildKit argv tests cover local cache import/export, and the metal
acceptance remains the authority for real Dockerfile layer reuse.
