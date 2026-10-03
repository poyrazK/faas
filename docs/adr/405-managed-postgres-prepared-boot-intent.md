# ADR-405: Record prepared-network boot provenance before setup

Date: 2026-10-03

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-404 retires absent unclaimed prepared reservations only when both namespace
and veth creation checkpoints establish one earlier kernel boot. A process crash
before either checkpoint leaves a reservation that remains quarantined even
after a host reboot has destroyed all of its volatile network resources.

## Decision

Write new prepared records as version 6. Capture the creator kernel boot ID
before setup, commit it with the initial network-only lease intent and fsync the
journal directory before any physical creation. Missing, invalid or noncanonical
boot IDs prevent setup. Every later asset namespace/mount and guest process
checkpoint must agree with this boot. Preserve the boot through transfer intent,
guest adoption, asset retirement and stable-source record replacement.

For version 6, an unclaimed network-only spare may use that initial boot ID for
ADR-404 retirement even when creation checkpoints are incomplete or already
retired. Require a different current kernel boot and the existing complete
inventory and absence checks: no observed source/slot, no ordinary/private link
name, no source namespace/jail path, and no process UID claim. Recheck eligibility
under the journal lock and fsync record removal before admission. Failure keeps
capacity reserved and prevents startup; a confirmed retry can finish retirement.

Continue reading versions 1–5. Version 5 rejects an initial boot field and retains
its complete-checkpoint retirement rule; incomplete legacy records stay
quarantined. Older binaries reject version 6, so rollback requires a stopped,
drained journal or another binary that understands version 6. Do not downgrade
records or erase boot provenance to bypass that boundary.

This changes only vmmd's private journal format. It adds no database schema,
RPC, quota, dependency or deployment environment setting. vmmd remains the sole
physical-resource owner; schedd remains the sole lifecycle and ledger writer.

## Boundaries

Same-boot records remain quarantined even when names are absent: a setup child,
renamed link or held namespace may survive the daemon. Pending transfers and
adopted guests never qualify. Recovery deletes no physical resources, reconstructs
no live ownership, returns no recovered spare to the pool and provides no
customer drain receipt. Concurrent privileged foreign creation and forged boot
provenance remain outside the exclusive physical-owner assumption of ADR-404.

Customer activation stays disabled. Supported native host-reboot acceptance,
filesystem power-loss qualification and guest serving recovery remain pending.

## Validation

Portable regressions cover boot fsync before creation, unavailable/changed boot
probes, contradictory records, boot preservation across adoption, and reopened
partial records across prior/same boots, transfer/guest state, UID collisions and
failed-fsync retry. Existing version-5 partial and mixed-boot regressions remain.

Metal diagnostics add separate-process SIGKILL checkpoints before and after
namespace/veth creation, alongside the existing five handoff checkpoints.
Injected prior-boot records exercise absence and preservation of real foreign
namespaces, veth/dummy links and UID holders across legacy and partial version-6
records. These diagnostics perform no actual host reboot; the dedicated GCP node
uses nested virtualization and cannot qualify native or power-loss behavior.

Final [nested diagnostics](../ops/evidence/20261003-managed-postgres-prepared-boot/README.md)
passed 83 selected top-level tests (347 including subtests), all 11 SIGKILL stages
and all 35 prior-boot collision cases, full Linux/macOS race suites and three
leak checks. Changed-code lint, egress and generated deployment checks passed.
Two Linux-only fixture corrections account for virtual-address fault-around
alignment and give foreign links explicit addresses to avoid external MAC-policy
races. Failed test/build attempts are preserved in the evidence. Native acceptance
remains pending; customer activation stays disabled.
