# Observed HTTP cron delivery outcomes

Date: 2026-11-16

## Context

Command-cron admission evidence does not describe HTTP cron delivery attempts.
The durable invocation drain already records settled HTTP cron attempts in
`schedd_cron_delivery_total`, with five closed outcomes. Scheduled fires and
manual runs share this metric, and retries can create several observations for
the same invocation. Application errors and delivery failures need to remain
visible independently of later quiet or recovered observations.

## Decision

Expose optional advisory `http_cron_delivery` evidence in the existing protected
runtime-build read for explicit single-box-v2 and control-plane-v2 profiles.
Observe schedd and, on the control-plane profile, schedd-compute. Retain the fixed
outcome order: success, retry, app_error, failed, dead_letter. Reuse existing
metrics; do not alter producer behavior, scheduling policy or durable state.

Use the existing exact process-local counter protocol. Comparable observations
require fresh original sample clocks, unchanged sampled process proof, strictly
advancing samples and uptime advance within the existing one-second tolerance.
First usable samples are baselines. Resets, new or changed processes, repeated
samples, uncertain windows and source gaps establish no delta. Valid sibling
outcomes and scheduler processes remain useful independently.

Summary counts classify counter signals across the full target set. Exact
bounded target deltas describe settled attempts, not unique jobs or deliveries.
Retry does not establish its cause or a final result; app_error remains separate
from the producer's failed/dead_letter classifications. Success does not prove
unique or exactly-once execution. No failure-rate threshold or deployment gate
is inferred.

Carry the section through independently validated API/CLI transport, SDKs,
projected reports and compact bounded history. Preserve a canonical sticky union
of increased outcomes, observation clocks, baselines and evidence gaps without
summing overlapping poll deltas. Preview details yield before previous sections
under the existing response/report limits. New source reads yield to loop reads
and share the same deadline; protected HTTP poll counts remain unchanged.

## Consequences

Operators can distinguish retries, application errors and delivery failure
classifications without conflating them with command-cron admission. Sampled
process continuity cannot exclude every intervening restart. Older profiles and
readers retain compatibility, and invalid optional evidence stays isolated from
verification results.
