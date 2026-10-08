# ADR-432: Start workflows through durable event fanout

- Status: Accepted
- Date: 2026-10-02
- Amends: ADR-081, ADR-345, ADR-346, ADR-487

## Context

Customers can manually start workflows or declare recurring schedules. Internal
event subscriptions currently invoke an app handler, requiring customers to
write an adapter to start a multi-step workflow from an event.

## Decision

Workflows accept a `type: event` trigger with required `source` and `event_type`
patterns, an optional JSON object `filter`, and optional `enabled` flag. The
existing event matcher and filter validators define the language. Matching runs
receive the complete canonical CloudEvents envelope as their input. Schedule
options, fixed input, and overlap settings are rejected on event triggers.

The event insert transaction adds workflow candidates from each app's preferred
live default deployment to the existing durable recipient snapshot. Eligible
apps belong to an active paid account without an abuse hold, are not deleted or
in maintenance, and do not require platform tenant context. Selection mirrors
scheduled workflows. Preview uses the same database candidate function and
matcher, with bounded paging and samples. Workflow recipients expose their name
and deployment ID in preview results. Their stable recipient UUID is the MD5
digest of `gregale.workflow.event:<canonical app UUID>:<workflow name>`; it is a
routing identity, not a security primitive.

Each recipient includes its complete workflow definition. Subsequent edits,
disabling, or removal affect future events; accepted events keep their original
definition and filter. Historical outbox receipts are unchanged and do not gain
new workflow recipients. Step execution still routes to the app's serving
deployment, as with manual and scheduled runs; handler images are not pinned.

Schedd admits a matched recipient with the existing app advisory quota lock,
shared by manual and scheduled starts. The workflow run and an outbox-scoped
admission receipt commit together. Receipts survive workflow history pruning
through a nullable run foreign key and expire when their outbox identity is
pruned. A stale outbox claim cannot admit work. The scheduler then records the
ordinary recipient checkpoint; interruption between admission and checkpoint
does not create another run.

ADR-648 extends admission to independent recipient leases. For adopted receipts,
the run, admission receipt, recipient checkpoint and routing history commit
together. Explicitly disabled adoption retains the original parent-lease path
for new workflow receipts.

Quota pressure and temporarily unavailable targets use the existing bounded
fanout retry and recipient replay mechanism. Deleted targets and invalid
definitions produce terminal failures. Workflow recipients stay pending without
consuming recipient retry attempts while the workflow runtime is disabled;
ordinary subscriptions on the same event can finish independently.

## Consequences

The feature remains part of the preview workflow runtime, gated by
`FAAS_WORKFLOWS_ENABLED=1` on apid and schedd with a gateway executor. Event
publishes remain account scoped and deduplicate by account, source, and ID for
the existing 30-day retention window. Workflow recipients increase publish
transaction work and snapshot storage in proportion to matching definitions.
An event can start several workflows subject to the app's active-run quota.
Handlers still require idempotent side effects because step execution can retry.

Runs use the existing workflow list, status, attempt, and cancellation surfaces.
Admission failures and their histories use the existing event fanout inspection
and replay surfaces; the `subscription_id` field also identifies workflow
recipients. Event-delivery invocation rows remain the view for ordinary app
subscriptions. Preview reports matching intent and does not promise execution
availability or reserve quota.

Apply the migration and upgrade schedulers before deploying event trigger
manifests. Older schedulers do not understand workflow recipients and could
route them as ordinary app invocations. Before rolling back binaries, drain
event receipts containing workflow recipients and finish or cancel their runs.
