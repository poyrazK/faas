# Release tags no longer wait for full CI

Date: 2026-10-18
Status: Accepted (owner decision: "fully separate full CI and tagging/release, I accept the risk")
Related: ADR-093 (release pipeline), ADR-1650 (per-unit releases), ADR 202610171900 (release compatibility gates), ADR 20261018084150 (expand-only migrations in the unit lane)

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

`release.yml` called the mega tier (`ci.yml`) as its first job and built,
signed and published nothing until every one of its 41 jobs passed on the
tagged commit. Measured on the October 16 tags:

| Tag | Full CI | Build, sign and publish |
|---|---|---|
| rc.268 | 66 min | 13 min |
| rc.269 | 86 min | 13 min |
| rc.270 | failed after 107 min (drift checks, flags suite); nothing published | — |

Eight tags (rc.266 to rc.273) were pushed that day, and each started its own
full run. Most of a run's length is runner capacity, not test time: in rc.269
the two longest jobs waited about 51 minutes for a runner before starting.

Running the mega tier more often to have a green commit ready (continuously,
or on every merge) needs CI compute the project does not have; PR queues would
pass three hours. Requiring a tag to point at the last nightly-green commit
was rejected by the owner: it takes away the choice of what to release.

Pull requests already run the light tier (`ci-light.yml`): affected-package
tests, lint and vet, migration ID, expand/contract, apply and replay checks,
N±1 version skew and the boot contract. The per-unit lanes (ADR-1650) already
ship on that tier alone.

## Decision

1. `release.yml` has no CI dependency. A `v*.*.*` tag builds, signs and
   publishes at once.
2. `ci.yml` runs nightly on main and on `workflow_dispatch` for any ref,
   including a tag. It stays callable (`workflow_call`) but no release
   workflow calls it or reads its result.
3. Nothing in the deploy workflows (`cd-platform`, `cd-controlplane`,
   `cd-compute`, `release-units`, `cd-edge-units`) consults full CI. A
   release's protection in production is the deploy itself: canary first,
   the SLO gate and automatic rollback (ADR 202610171900, ADR-1650), and
   `cd-platform`'s acceptance gate.
4. An operator who wants the full suite on a release dispatches `ci.yml` on
   the tag and waits for it before deploying. That is a choice per release,
   not a rule.

## Consequences

- Tag to signed release takes about 13 minutes instead of 1.5 to 3 hours.
- Full-CI compute drops to one nightly run plus manual dispatches; a busy
  release day no longer starts a full run per tag.
- **Accepted risk.** A regression that only the mega tier catches can reach a
  published release and production: e2e journeys, the full migration history
  replay, `-race` across whole packages, the container contracts, the SDK and
  load suites, and the drift checks. A flaky mega-tier job no longer blocks a
  release, and a real failure no longer does either.
- A red nightly is now a signal to act on before the next full deploy, not a
  gate. Triage it the next morning; until it is understood, prefer the
  per-unit lanes and dispatch `ci.yml` on any full tag before deploying it.
- Contract migrations, `deploy/` changes and the VM runtime ship only in full
  releases, and are the changes a canary catches latest. They have the least
  pre-release coverage now; this is part of the accepted risk.

## Alternatives considered

- **Continuous full CI on main, release a green commit.** Needs compute the
  project does not have.
- **Tag only the newest nightly-green commit.** Rejected: removes the choice of
  what to release.
- **Build and prestage while full CI runs, and gate the deploy.** Keeps the
  wait before production, saving only the build time.
- **Rerun only what changed since the last passing release.** Cuts the full
  run's time and compute but keeps the gate. It can still be done later to
  make the nightly and dispatched runs cheaper.
