# ADR: Reviewed SQL compensation for typed pipelines

Status: Accepted (SDK preview)

## Context

Typed pipelines can import application data before a later reporting stage fails.
Developers and agents need an inspectable recovery action that preserves original
history and cannot replay business changes after a lost acknowledgement. Native
failed code workflows cannot continue execution, and generic rollback cannot
safely undo external services or unverified partial writes.

## Decision

Add matching Node/Python preview compensation definitions over existing typed
Jobs, pipeline tools, approval gates and native code workflows. Each definition
registers one typed SQL compensation Job and requires an explicit code/policy
revision. Planning validates a failed original root and all declared source Job
receipts. Writing Jobs must have succeeded; unresolved or failed writers require
application-specific reconciliation. Read-only stages are explicitly declared.

Persist one immutable bounded command plan per original run and definition.
Derive commands from verified original results, including succeeded validation
stages. Bind contracts, owner, scope, policy revision, order and business inputs.
Use a separate durable tool start receipt and native recovery workflow, with a
fresh exact-plan human approval for each explicitly chosen attempt. Require
current application access, backend execution authorization and transaction-time
business authorization. MCP adds planning and reuses the existing lifecycle;
human decisions stay outside agent tools.

Execute commands sequentially in exclusively owned application PostgreSQL
transactions. Lock each original effect, verify prior outcomes and atomically
commit business changes with a retained typed outcome. Roll back business writes
for `needs_review` before recording it and stopping. Unknown COMMIT outcomes
never imply rollback; a task retry reads the same effect receipt. Applications
must enforce row ownership and original version/fingerprint guards in callbacks.
Remote effects are outside this slice.

Use shared generated schemas, identities and SQL for plans, attempts, runs and
effects. Introduce no native endpoint, platform migration, admission owner or
plan quota. Existing callers remain unchanged. Keep old workers/policies while
their plans can execute; configuration drift conflicts rather than replanning.

## Consequences and evidence

Applications migrate private receipt tables, declare every source Job honestly,
retain original evidence and supply current authorization and SQL guards. The
SDK cannot infer handler semantics from contracts or certify read-only code.
Failed writing Jobs and changed rows need explicit application recovery. New
attempts require fresh human approval and do not override a retained review stop.
Cancellation stops orchestration without reversing committed or inflight work.

Shared fixtures and installed package consumers verify cross-language plans,
contracts, public types and MCP surfaces. Tests exercise concurrent planning,
lost commits, retry receipt reuse, approval/source binding, current access,
ordered execution, rollback, deadlines and cancellation. The CSV journey uses
real PostgreSQL and Chrome: report failure, reviewed cleanup, a dropped task
acknowledgement, mixed Node/Python retry, and preservation of a later row edit.
Native control-plane and MCP transport remain fixtures in that journey.
