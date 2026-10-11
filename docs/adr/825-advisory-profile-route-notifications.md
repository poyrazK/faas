# ADR-825: Advisory route profiling notifications

Status: Accepted

## Context

Automatic deployment and canary checks retain CPU evidence but require operators
to inspect each result. Route CPU/request comparisons already apply capture,
traffic, attribution, and request-label consistency requirements. Notifications
must preserve those requirements and must not change rollout decisions.

## Decision

An optional `notify_route_regressions` setting on the automatic profile policy
subscribes its explicit advisory routes to regression and recovery transitions.
It defaults to false. Existing thresholds and evidence requirements apply.
Terminal deployment and canary worker results produce these observations; this
is not a periodic steady-state application monitor.

Persist one incident state per application, scope, baseline deployment,
candidate deployment, policy revision, and route. A candidate window-end and
assessment timestamp watermark rejects late observations. Missing evidence
advances the watermark without resolving an open incident. A healthy observation
can resolve only an incident in the same context. Changing policy or deployment
pairs does not imply recovery.

Hold the owned application lock while committing the result, alert state,
optional investigation, and recipient-snapshotted webhook outbox entries.
Memory storage uses the equivalent critical section. Current policy revision and
notification opt-in are checked at completion. Use existing signed webhook
retries and delivery semantics; receivers deduplicate by event delivery identity.
A state transition is enqueued once, while delivery remains at least once.
Disabling notifications does not retract events already committed to the outbox.

Payloads contain bounded route metrics, comparison windows, deployment and policy
identities, and retained evidence paths. Save an investigation for canary
transitions when quota permits; use retained canary history otherwise. Include
at most five bounded application hotspot frames, explicitly distinct from
route-specific code attribution. No raw profile samples or source URLs are sent.

## Consequences

Operators can route advisory notifications through their existing app webhooks.
No event alters canary advancement, deployment health, or rollback. Contexts can
remain unresolved when deployments end or evidence disappears; recovery always
requires positive evidence. Alert state survives receipt pruning and disappears
with physical deletion of its owning application or deployment in PostgreSQL.
