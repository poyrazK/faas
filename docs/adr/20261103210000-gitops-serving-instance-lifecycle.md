# ADR-20261103210000: GitOps serving instance lifecycle

- **Status:** accepted
- **Date:** 2026-11-03
- **Related:** ADR-568, ADR-20261103200212

## Context

GitOps deployments retain their frozen runtime inputs after activation. Two
PostgreSQL guards used those inputs to identify qualification VMs, so ordinary
serving instances could neither publish their runtime receipt nor park, stop,
fail or be collected. The normal scheduler's wake transaction rolled back;
cleanup could leave a destroyed VM recorded as resident.

## Decision

Scope qualification receipt authority to an instance's execution journal,
qualification request or restore reservation. A held candidate without an
execution frame remains fenced as uncertain legacy qualification. Frozen
deployment inputs alone do not impose qualification authority on ordinary
instances after activation.

Preserve the journal's dispatch, identity and native retirement checks, including
after request replacement or parent removal. Ordinary instances follow the
existing atomic runtime publication, input freshness and terminal state paths.
Replace the two functions in an additive, replayable migration; retain the
corrected distinction on binary rollback. No new execution protocol or customer
enforcement capability is enabled.

## Validation

Both stores exercise normal cold-boot and restore publication on a fully
qualified, activated and serving graph, including fleet freshness, park, stop,
failure and collection. PostgreSQL reproduces the old failures on populated
rows, then applies and replays the migration to repair them. Legacy instances
missing their execution frame remain fenced. Existing qualification tests cover
private receipt authority, immutable execution identity, restore attempts,
expiry, physical retirement and cleanup after parent removal. These are storage
tests; native KVM acceptance remains a separate gate.
