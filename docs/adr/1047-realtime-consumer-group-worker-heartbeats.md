# ADR 1047: Consumer-group worker health heartbeats

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add opt-in POST `heartbeats` to account group and scoped worker routes. Account
writes require ownership, deploy-write scope and MFA; account GET uses read scope
and MFA. Scoped credentials require the new explicit `heartbeat` permission, bound
again under the group lock. Worker identity is the credential's fixed name and UUID;
account reports are keyed by caller-selected worker name in the account namespace.
Sharing a credential aggregates processes into one identity. Use separate credentials
for independent worker observation. Rotation retains the credential UUID/identity.

Require explicit boolean `ready`; accept a diagnostic `status_code` of at most 64
printable ASCII bytes. Never include secrets. Reporting does not renew a lease,
consume claim-rate credit or change pause/drain. Worker credential HTTP/transaction
attempt limits still apply. No identity is expected or monitored before its first
report. Fixed timeout is 60 seconds; recommend reporting every 15 seconds.

Persist up to 32 tracked identities per group. Last-seen timestamps use database
time. Live means fresh last-seen and, for scoped workers, an existing unrevoked,
unexpired credential with heartbeat permission. Readiness remains the last reported
assertion; available workers are live AND ready. Reads compute liveness independently
of a persisted `lost` decision, which may lag sampling. Neither metric proves
connectivity, handler correctness or successful processing.

Group-locked maintenance marks stale/ineligible identities lost, with a sampler
running every 15 seconds, at most 64 eligible groups per pass with a 20-second
budget. Group due-time updates and SKIP LOCKED coordinate replicas. Disabled endpoints,
deleted apps, suspended accounts and ineligible plans suspend monitoring; pause/drain
do not. Sampling can lag due to locks, fleet bounds or outages. A fresh heartbeat
first checks loss and then updates, so an outage beyond timeout can produce loss
and recovery atomically even when the sampler was unavailable. First registration
and readiness-only changes do not generate transitions.

An AFTER UPDATE trigger snapshots signed webhook recipients and enqueues
`realtime.consumer_group.worker_lost` or `worker_recovered` only when `lost` changes.
Payload includes event/app/endpoint/group IDs, channel/group, heartbeat ID, worker
name, last reported readiness/status, last-seen and occurrence time. No credential
secret, hash, lease token or message body is included. Health transitions and outbox
writes commit together; delivery/retry use the existing dispatcher. Recovery means
fresh liveness, not readiness. Recipients added later do not get a backfill.

Lost rows remain for up to a day, pruned during maintenance. When a new identity
would exceed the cap, replace the oldest lost row; never evict a live identity.
Capacity with all 32 live identities returns 429. Eviction/pruning loses future
recovery history for that identity; its next report is a fresh registration. Queued
notifications survive pruning. Group deletion cascades heartbeat rows without
manufacturing loss alerts.

## Visibility and SDK

GET health returns tracked workers, live/ready counts and timeout. Inspector shows
last-seen/status and flags raw pending work with no ready reporting workers. Legacy
workers that do not report remain unobserved; an empty list does not prove no workers
exist. Raw unassigned backlog may include nonmatching events until filter scanning.
No aggregate backlog-loss webhook is added; worker transition webhooks are explicit.

Generated Node/Python account and scoped request models expose heartbeat reporting.
The isolated Node worker client adds `heartbeat(body)`. Node consumer helpers enable
automatic ready reporting with `heartbeat: true`, off by default: report before
claiming, then every 15 seconds while idle or busy. Explicit scoped heartbeat grant
is required. Reporting errors stop that opted-in helper and abort its handler signals,
following existing credential/transport error behavior. Shutdown cancels the loop;
natural timeout later records loss, including intentional shutdown. Signals remain
cooperative and external writes still need idempotency/fencing. Custom integrations
can report ready=false with a diagnostic while remaining live.

## Rollout and verification

Apply migration `20261012102700509_realtime_worker_heartbeats.sql`, upgrade all server
replicas, then issue heartbeat grants/enable reporting. The new grant vocabulary is
also persisted in credential last-operation history; incompatible grants/history
intentionally block downgrade until addressed explicitly. Rollback otherwise removes
heartbeats/monitoring without changing leases and retains queued webhook vocabulary.
New queries use generated sqlc methods and a migration-owned schema projection.

Go/Node builds, OpenAPI parity, Python static checks and sqlc regeneration parity
are used for this change. Runtime tests, migration execution and inspector rendering
remain deferred at the user's request.
