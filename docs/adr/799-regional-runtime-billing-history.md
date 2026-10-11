# ADR-799: Retain original regional runtime billing history

Status: accepted · 2026-10-11

## Context

ADR-798 requires complete regional compute observations from original run and
resource history. The ordinary billing intervals cascade with instance deletion
and do not identify wake attempts. The sampler also reads today's deployment
sidecars and app RAM. Neither surface can reconstruct an unreported original
regional run after collection or configuration changes.

## Decision

Retain a separate billing interval journal with the original app, instance,
deployment, node and wake identity. Record every committed billable transition,
including short runs between meter ticks, before a regional launch source is
issued. Use the existing billing state set and mirror exclusion. Running
publication may replace `started_at`; it must not reset the retained interval.
Normal departure from the billable state set closes an interval. Re-entry opens
another interval. A changed run identity while billable, or deletion of an open
interval, marks its history uncertain from the change time. Neither operation
proves native absence or permits a zero observation.

The journal has no foreign key to collected apps, deployments, instances or
nodes. Retain closed intervals and unresolved open intervals across collection.
Identity and start timestamps cannot change; end or uncertainty is recorded
once by the lifecycle trigger in the same transaction, under the original app
quota journal lock. Reparenting takes both roots in canonical order. Read the
change clock after acquiring these locks. SQL rollback leaves no
history. Direct edits, deletes and truncation are refused. Populated migration
rollback refuses to destroy retained history.

Read original issued capacity and launch resource claims by the complete run
identity, independently of current app ownership, plan, sidecars, registry or
quota head. Expose the number of matching issued sources: missing or ambiguous
provenance must remain visible, and cannot be repaired from current rows. The
portable store mirrors transitions under its existing mutex and clock.

Hold instance writers while installing the journal and retain its installation
time after that lock is acquired. Existing billable rows at installation
have an explicitly unknown start; do not backfill their historical identity or
invent pre-installation coverage. A future producer must check this boundary,
all interval uncertainty, exact original issuance and resource provenance,
and prevent late history from changing a sealed minute under the app journal
lock. Reads of this journal alone do not establish complete minute coverage.

## Integration boundary

App-task VMs use a separate durable task/lease identity and do not enter the
ordinary instance journal. Matching a capacity kind alone does not establish
task history or coverage. ADR-800 adds their separate retained journal. Complete
minute production still requires both evidence sources and native issuance
reconciliation.

This is the retained evidence source for the producer, not permission to sign
complete compute receipts. Minute sealing, retained delivery, min-instance
floors, managed app-task attribution, egress, daemon wiring and global policy
enforcement remain required. Regional execution fences remain in place. Billing
history is scheduler state evidence; it does not establish native absence,
capacity qualification or positive ingress eligibility.
