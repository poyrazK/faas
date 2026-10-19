# ADR-1730: Data API runtime connection-demand observations

Status: Accepted

## Context

Connection budgets now have an opt-in qualified capacity observer (ADR-1729),
but consumer demand still uses canonical pool estimates. Resident configuration,
parked snapshots, future wakes and external consumers need separate evidence.
An application report authenticates a workload; it does not attest the integrity
of customer-controlled code or qualify every consumer of a database.

## Decision

Add an opt-in reporter to the generated Data API runtime. Immutable owner
configuration supplies the control-plane HTTPS origin, binding UUID, database
identity digest, database generation and credential generation. The digest is
SHA-256 of `backend_fingerprint + ":" + data_resource_id`; raw placement and
credentials are never sent. The current SQL login is sent privately and must
match the binding's current provider identity. The runtime checks PostgREST
14.3 before reporting and observes its actual engine pool maximum through the
existing bounded admin metrics reader. Coordinated refresh adds its reserved
one-connection reader. The supported model totals two configured connections.
These are configured ceilings, never counts of live, idle or free SQL sessions.

The reporter requests a fresh loopback workload assertion for audience
`gregale:data-api:connections` on every iteration. It reports only to the fixed
`POST /v1/runtime/data-api/connection-observations` endpoint, over verified HTTPS
with redirects refused. Tokens are not cached, logged or exposed through health.
This permits a restored process to obtain its current host-stamped identity.
The interval is 15 seconds after completion, total network deadline five seconds,
and request body at most 4 KiB. Health and reporting share pending metric work;
neither creates another tenant SQL pool. Stopping cancels reporting. Reporting
does not gate serving readiness or initiate wakes.

Apid authenticates only the dedicated workload audience. Customer API keys and
app JWTs cannot publish evidence. A single sqlc statement locks and checks the
active account, app, ready default-key Data API binding, current role and
credential generation, fully observed ready database identity/generation,
running normal wake instance, deployment scope, start time and fresh node
heartbeat. Initial reporting excludes clone-owned databases until that path is
qualified. Apid writes only observation rows; instance lifecycle remains owned
by schedd. The cache records deployment, node and wake identity and refuses
out-of-order observations. Observed times cannot be future or older than
30 seconds. Expiry is bounded by observation time plus 30 seconds and token
expiry, so replay cannot refresh an old receipt. A valid `unknown` report
replaces previous success after metrics failure or contradictory metrics.

Budget reads remain cache-only and atomically join evidence against the current
catalog, identities, credentials, start time, node heartbeat and expiry. Draining
and snapshotting runtimes remain potential consumers; parked instances are
reported separately and never treated as currently observed. Replacements,
rotation, generation changes and changed placement cannot inherit evidence.
The existing lifecycle/rollout planning estimates remain conservative.

An optional `demand.runtime_observations` projection reports observed and missing
resident counts, configured connection ceilings, parked count, source
`workload_report`, and the oldest check/earliest expiry. Its state describes only
resident reporting coverage. Budget expiry is clipped to all included evidence.
Private instance, deployment, node, provider identity and placement stay out of
customer budget responses and CLI health. Older servers/SDKs can omit the field.

## Consequences and validation

The report distinguishes authenticated resident configuration evidence from
planning assumptions without guessing at connection usage. Runtime reports do
not set the complete model/consumer-coverage flags: future instances, parked
snapshots, Auth, migrations, sidecars and external clients remain unqualified.
The overall budget remains `unknown` while any of those gaps apply; `enforced`
stays false. Independent runtime/image qualification and aggregate admission
remain follow-on work.

Tests cover dedicated-audience authentication, rejected body authority and size,
pool/refresh accounting, failure invalidation, replay, restart persistence,
expiry, account/instance isolation, scope, credential and database replacement,
node freshness, lifecycle overlap, CLI consistency and private-data redaction.
See [operator setup](../runbooks/DataAPIRuntimeConnections.md).
