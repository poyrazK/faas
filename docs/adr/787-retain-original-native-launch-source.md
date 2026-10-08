# ADR-787: Retain the original native launch source

Date: 2026-10-08
Status: Implemented locally; native lifecycle acceptance pending.

## Context

ADR-781 retains the original task and claim in the cell launch journal. The
native host frame retained the placement and footprint but omitted that task
relationship. An observed permit could therefore match a cell hold without
showing that the native producer retained the same complete original source.
Startup row charges still cannot safely merge with physical holds on that basis.

## Decision

New regional native ownership frames use version 4 and retain the complete
`LaunchSource`: the original permit, exact native lookup spelling, and original
task ID where applicable. Manager derives it only from the captured dispatch
request. For an app task, the task and claim must produce the original instance
UUID, the claim must equal the permit run ID, and the request must be task-only.
Other physical purposes cannot borrow task metadata. Native capability checking
requires the new source-aware producer.

The source is copied and validated against the original lease, placement and
resources before the ownership frame is durably written and before network,
artifact staging or native process effects. Invalid task provenance is refused
before launch registration and snapshot prefetch. Source fields are canonical and bounded by
their existing protocol shapes. Missing, null, duplicate, unknown, mismatched or
substituted source fields refuse version-4 ownership. There is no new operator
readiness switch or customer API.

Manager's native instance binding also compares its captured source before
regional control operations and app-task payload dispatch. A missing or changed
source at command dispatch retains ownership for the original regional retirement
owner instead of performing generic teardown. Authorization, observations,
revocation, exit/resource acknowledgements and
retired-generation archival compare the complete source with the existing
immutable regional binding. Restore-to-cold-boot fallback keeps that source
exactly; it cannot borrow a replacement task claim. Fully retired new dispatches
retain their own source in a new generation and cannot downgrade known provenance.
Version-1..3 history remains readable. Fallback never backfills missing original
source from the current task, app or instance row.

Fresh native inventory carries a copied optional original source. The scheduler
requires exact equality with the issued cell source before reporting a matched
hold, including the original task relationship. Older frames with no source
remain unmatched. Advisory inventory cannot merge startup row charges, release
physical holds, grant current task authority or activate regional quota heads.

## Validation

Contracts cover source persistence before network effects for all physical
purposes, authorization/restart, copied observations, exact lookup spelling,
task/claim substitution, damaged journal shapes, fallback and archive retention,
retirement acknowledgement binding, downgrade refusal and historical uncertainty.
The scheduler tests retain holds while refusing absent or changed native sources.
These are portable protocol/ownership tests. Dedicated native Linux x86_64 KVM
metal, leakcheck and fleet recovery acceptance remains required.

## Remaining work

The end-to-end multi-region goal remains active. Complete physical inventory,
original row/run/task binding and first-fence capacity reconciliation, native
snapshot capture/pause/migration and immutable-input latency qualification,
shared execution authorization and global billing, trusted ingress client/session
identity, independent regional provisioning/stores/replication and actual serving
from every selected healthy eligible region remain required. Regional admission
stays frozen. No live configuration, deployment or infrastructure changes are
made here. Older binaries reject version-4 frames; rollback requires a compatible
binary or a safely drained ownership journal.
