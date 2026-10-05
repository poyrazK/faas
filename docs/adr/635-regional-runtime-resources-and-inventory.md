# ADR-635: Original regional runtime resources and inventory

Date: 2026-10-05
Status: Implementation in progress; startup accounting and native acceptance pending.

## Context

Startup accounting currently reads live instance rows. Issued physical permits
survive deletion, terminal states, newer grants and host incarnations. Their
original resource dimensions cannot be reconstructed from today's app or plan.
ADR-633/634 retain the actual lookup source and recover fenced failures, but that
scan does not discover unfenced or bookkeeping-only historical holds.

## Decision

Dispatch retains guest RAM, guest vCPU topology and the native CPU startup peak
alongside its immutable original launch source, atomically before physical and
initial serving-seat issuance. A new mandatory Store writer receives those
resolved dimensions from the actual scheduler AppSpec. Invalid dimensions refuse
issuance. Resource retries must match exactly. Existing source guards prohibit
updates, deletion and truncation of the resource claims as well as the source.

The additive nullable column permits existing historical records to remain
unknown. Never backfill their dimensions from mutable app, deployment, instance
or node rows. Bookkeeping APIs remain available for historical contracts; new
native dispatch requires the resource writer. Storage bounds reflect the int32
native resource fields and live in `pkg/api/limits.go`.

A cell-wide inventory reads every issued, unreleased physical permit, including
unfenced permits and permits without a retained source. It spans all allocations
and apps without joining current quota heads or customer rows. Composite
app/permit pagination retains identical permit IDs from different app journals.
The inventory retains full original permits, first fences, any original lookup
source and resource claims, and counts all issued unreleased serving seats even
when their seats are fenced. Reserved unissued records and accepted physical
stops are excluded. Unknown source or resources remain explicitly unknown.

This inventory is a foundation for conservative startup accounting, including
orphan residents, replacement runs, retired allocations and independent host
epochs. Ledger integration, inventory traversal barriers and native inventory
reconciliation remain required. A journal scan does not prove global host
inventory completeness or native absence. All admission heads remain frozen.
Capture/pause/migration integration, directory authority, global billing, serving
from all selected healthy eligible regions and native acceptance remain required
for the end-to-end feature.

## Validation

Exercise both stores for immutable resource retries, unknown provenance refusal,
collected apps, unfenced and source-less issued holds, duplicate permit IDs,
composite pagination, held serving seats and accepted-stop exclusion. Verify
scheduler dispatch captures actual resource dimensions and keeps the original
source on an uncertain commit reply. Apply the additive migration, regenerate the
live schema and sqlc output, and run race tests and lint. Native Linux x86_64 KVM
lifecycle and recovery acceptance remain pending.
