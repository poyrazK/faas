# Compose profiles from COMPOSE_PROFILES in .env

Date: 2026-10-22
Status: Accepted
Amends: ADR-1337 (Compose key reporting)

## Context

A Compose service with `profiles:` runs only when one of its profiles is
active. Compose activates profiles with `--profile` or `COMPOSE_PROFILES`,
which it reads from the shell or from the project's `.env`. The scan had no
way to activate any profile: every service with `profiles:` was skipped with a
warning, so a file that keeps production-only services behind `profiles:
[prod]` could not deploy them without being edited.

## Decision

The scan reads `COMPOSE_PROFILES` from the source's root `.env`, the file it
already uses for interpolation, with Compose's grammar: comma-separated
profile names, surrounding spaces ignored, and `*` activating every profile. A
service is deployed when it has no profiles or when any one of its profiles is
active. A skipped service's warning names its profiles and points at
`COMPOSE_PROFILES`.

There is no shell environment or `--profile` flag on Gregale, so `.env` is the
only source. A per-project setting can be added later without changing this
grammar.

## Consequences

- A repository that commits `COMPOSE_PROFILES` in `.env` deploys the services
  those profiles enable; a project without it behaves as before.
- A dependency on a service whose profile stays inactive is reported by the
  existing dependency validation, as Compose reports it.
