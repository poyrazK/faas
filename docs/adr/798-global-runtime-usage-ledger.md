# ADR-798: Global runtime usage ledger and complete billing windows

Status: accepted · 2026-10-11

## Context

Regional allocations conserve serving capacity and retain the original cell,
tenant and resource identity. The ordinary meter still attributes compute to
the regional account. Enabling globally managed execution needs one accounting
root, and hourly provider receipts cannot be settled before a delayed region's
consumption has arrived.

## Decision

A trusted cell meter signs an immutable complete UTC minute for one original
issued allocation target. Claims repeat the exact grant, integer MB-seconds,
the count and SHA-256 of retained original resource measurements, and the actual
closed-minute observation time. Zero consumption is explicit coverage. Missing
minutes do not mean zero. Signatures authenticate the cell meter's assertion;
they do not establish native residency, native absence, or a physical drain.
Historical policy identity remains readable without becoming a new entitlement.

Meterd's importer resolves the original committed issuance in the directory.
It derives the personal global account and global app identity there, rather
than accepting a submitted billing account or consulting current regional
ownership, current plan, registry generation or attached target. Shared tenant
billing requires separate shared authority and remains unavailable. Retired,
detached and replaced targets retain historical billing identity. A verified
drain bounds the required coverage at its UTC minute ceiling; neither outage,
expiry nor detach bounds it.

Retain the first signed receipt and its immutable claims on
`(global_app_id, allocation_id, cell_id, minute)`. Concurrent delivery, restart,
lost commit responses and signature rotation replay that receipt. Conflicting
quantity, evidence digest, observation time or original identity is rejected.
Use a deterministic namespace for the canonical billing row, keeping cells and
allocations distinct from independent instances. Receipt, canonical
`usage_minutes` and financial evidence commit atomically. Financial attribution
uses the global app and original allocation plan; recorded prices remain
integer-based. Receipts are append-only while the account exists. Account
erasure removes its retained evidence; imports cannot recreate an erased owner.
Populated schema rollback refuses to discard receipts.

Both current-hour and backlog provider delivery require complete global
coverage for the full calendar-month prefix through the billed hour, including
explicit zeros and historical issued targets. Checking only the current hour
would silently spend included allowance against an incomplete month. Hold
provider delivery and its success receipt on a missing minute or read error.
Independent accounts continue through the usual billing path. Secondary meter
delivery also waits because its combined spend cap depends on compute totals.

## Implementation and remaining integration

`runtimequota.RuntimeUsage` is the bounded cell-signed protocol.
`meter.GlobalRuntimeUsageImporter` is the meterd write boundary; memory and
PostgreSQL import into the existing monthly, hour, financial and provider
billing surfaces. Ordinary provider delivery invokes the coverage guard.

ADR-799 adds retained regional run intervals and original launch resource reads
across instance and app collection. The regional producer and its transport
remain pending. The producer must capture every closed
minute once, preserve history across instance/app collection and replay outages.
It must route managed app compute to this ledger instead of charging the
regional account, and define min-instance floor and app-task accounting before
those paths can activate. Compute receipts do not establish egress coverage.
Global free-tier/cap enforcement, shared authorization/billing, native capacity
qualification and positive ingress eligibility remain required for end-to-end
activation. Existing execution fences stay in place until those requirements
are implemented and the dedicated native KVM acceptance gates pass.

## Validation

Protocol tests cover strict canonical encoding, role and purpose separation,
invalid/open observations, quantity bounds, explicit zeros and historical
policy identity. Shared memory/PostgreSQL contracts cover concurrent replay,
signing key rotation, original personal ownership, region separation, missing
coverage, identity conflicts, future observations, plan/origin changes and detach.
PostgreSQL tests exercise financial-write rollback, restart, migration replay and audit guards.
Meter tests exercise the normal hour and backlog pushers with two selected
regions and verify one global bill only after the final receipt arrives,
including a gap in a previous hour of the month.
