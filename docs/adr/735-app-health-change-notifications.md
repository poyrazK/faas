# ADR-735: Opt-in application health change notifications

Status: accepted · 2026-10-05

## Context

ADR-734 retains application health observations independently of dashboard
visits. Customers need notification of meaningful status changes through the
existing signed webhook delivery system. Missing evidence must not imply an
outage or recovery, and flapping assessments must not flood receivers.

## Decision

Produce `app.health.changed` inside successful background assessment completion.
The latest assessment, retained transition, bounded notification comparison and
outbox intent commit in the same transaction. An expired lease, observation
conflict, serialization or outbox error publishes none of them. MemStore prepares
all fallible work before publishing under its existing lock. New SQL uses sqlc.

Require explicit selection of this event on an enabled app-owned webhook. Empty
filters retain standard platform events and do not opt in to health changes.
Account and platform-tenant receivers remain outside this source. Capture
recipient IDs and subscription revisions when the status changes; late or
reconfigured subscribers do not receive historical or already pending changes.
Remove recipients whose captured subscription revision changed before a
deferred event commits. Existing delivery controls govern committed events.

The first observation silently establishes a comparison, including after an
upgrade. Changed findings, releases, counters or phases without a status change
do not notify. A change without recipients advances the comparison silently.
Expired-evidence gaps discard pending notifications and reset the comparison;
resumed collection alone cannot imply recovery. Fresh known-to-unknown and
unknown-to-known changes are `unconfirmed` and `confirmed`. Comparable known
statuses use `worsened` or `improved`, describing sampled assessments only.

Queue at most one event per app every five minutes. During cooldown retain one
bounded pending transition and recipient snapshot. Subsequent status changes
replace it; returning to the last announced comparison status cancels it. The
next successful collection after cooldown queues the latest observed status,
even if that sample adds no new history entry. Collection failure or a gap
cannot flush an old pending success. Cooldown and pending state survive restart
and bounded history pruning; state occupies at most 8 KiB per app and recipient
reads reject more than the central 100-recipient bound. Normal subscription
quotas remain unchanged. App/account deletion cascades remove the state.

The versioned payload identifies the original retained status transition and
its observation time, the comparison status, the current sampled status and
phase, and the fresh assessment and queue times. Coalescing is explicit.
Release identifiers and an authenticated history path support investigation;
the path may no longer contain an entry after retention. Do not include probe
output, request data, infrastructure addresses or secrets. These events do not
establish public reachability, continuous health, exact incident duration,
deployment causality, or authorize automated recovery.

Reuse the existing outbox relay, signed dispatcher, delivery ledger, retry and
replay controls. The event/source key and delivery source/subscription key
deduplicate publication and ledger fan-out; HTTP delivery remains at least once.
Receivers deduplicate attempts using the stable delivery ID. Publish the event
and typed payload in the API and SDKs, and expose explicit selection in the
existing console and server-rendered webhook forms. No new sender or worker.

## Consequences

One nullable bounded JSON column and an expanded outbox event constraint require
a forward-only migration. No new settings resource or plan entitlement is
introduced. Existing webhook plan gates apply when subscriptions are configured.
The notification cooldown is fixed in this slice. A stopped collector cannot
send a collection-stalled alert; existing fleet metrics remain the signal for
that failure. Public probes and automatic recovery remain later work.
