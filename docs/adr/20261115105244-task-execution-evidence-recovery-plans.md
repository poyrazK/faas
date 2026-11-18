# ADR-20261115105244: Durable Task execution evidence and recovery plans

Status: Accepted
Amends: ADR-20261115101539

## Decision

Retain bounded local-adapter checkpoints in the existing native Task operation
journal. Derive a deployment correlation ID from the account, intent and operation
identities. Fsync local state and obtain native checkpoint acknowledgement before
the next release effect. Persist pending submission identity before a deployment
POST, then append its deployment ID when acknowledged. A missing checkpoint
acknowledgement stops the adapter. Never erase an uncertain submission without a
deployment receipt. Never replace captured baseline, deployment, worker or parked
identities, regress stages or rewrite a checkpoint revision. Exact repeats remain
idempotent. Recorded completed stages derive from checkpoint fields.

The authenticated claim holder supplies checkpoints; they are execution records,
not independent health proof. Recovery reads separately verify identities and
current state through native APIs. Old clients and operations without checkpoints
remain compatible, but their missing evidence blocks recovery planning. Retain
checkpoints after expiry or failure. Existing owner recovery/retirement stages
keep their independent native authority and do not renew the agent claim.

Checkpoint payloads retain at most 1,024 Task worker IDs and 32 previous-worker
entries. Revisions stay within JSON's exact integer range. These are journal
bounds, not deployment or billing quotas. They fit the existing request and
persisted payload limits; no migration or new execution authority is needed.

## Read-only agent recovery

Add `gregale_inspect_task_tool_recovery` and `gregale_plan_task_tool_recovery` to
local personal-account `build-tools`, including read-only connections. Inspection
and the CLI `execution inspect|plan-recovery` use only native GET APIs. They load no
local artifact, binding profile or credential file, run no customer code, connect
to no Task database and make no application endpoint requests. Hosted,
organization and app-scoped connections remain excluded.

Compare saved identities with current app/configuration reviews, deployment
status, serving revisions, worker generations/instances and observer telemetry.
An uncertain POST is observed through all bounded app-history pages: exactly one
matching operation reason and app identity may identify the lost reply. Zero or
multiple matches, incomplete pagination, repeated cursors, failed reads or changed
generations remain blocked/unknown. Observation never writes recovered IDs back
or repeats a submission. Reread the operation journal at collection end; changed
records block planning. Controller reads are observations, not an atomic snapshot.

Report recorded completion separately from current facts. A running journal
entry cannot prove that its local executor lives, so report
`running_or_interrupted`. Show concrete blockers and remaining owner checks for
resume, restore and candidate retirement. Captured promotion and observed traffic
must agree; restoration requires old serving/worker evidence, and retirement
requires baseline restoration and the exact candidate generation. Observed
zero-live previous workers may support an owner-reviewed resume after a lost park
reply, without asserting the cause of that boundary.

Plans expire after one minute and hash their complete report, with an independent
plan identity. They always report `read_only=true`, `ready_to_apply=false` and
`owner_authorization_required=true`. An action is `blocked` or
`requires_owner_checks`; it never claims qualified executable readiness. Source,
dependencies, database/key compatibility, live catalog/role readiness and a new
separate owner recovery authorization remain mandatory. No endpoint consumes a
plan hash as authority. Implementing recovery execution is a later milestone.

## Validation

Cover monotonic checkpoints, duplicates, lost deployment/traffic/park replies,
complete and ambiguous history, cursor cycles, missing local files/checkpoints,
changed journals/generations, stale telemetry and unavailable facts. Assert GET
only inspection, no local CLI use from MCP, opaque native errors, both MCP
protocols and false authority flags. Exercise memory/PostgreSQL checkpoint
persistence and stop execution before effects when native acknowledgement fails.
Preserve all existing release gates. Regenerate preview clients and validate
schema parity, OpenAPI, ADR and formatting contracts. Live staging stays skipped.
