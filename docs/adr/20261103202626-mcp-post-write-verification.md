# ADR-20261103202626: MCP post-write verification

Status: accepted

Extends [ADR-20261103193000](20261103193000-mcp-recovery-contracts-and-focused-discovery.md).

## Context

A completed mutation receipt records native acceptance or a committed change.
It does not establish restart processing, serving health or worker generation
convergence. In particular, a core configuration change could request a restart
without listing the read tool needed to follow it. Generic apply and inventory
schemas also lacked required result identities and explicit pagination endings.

## Decision

Apply results and plan receipts include a separate `verification` contract with
closed statuses, a goal, value-free checks and, where supported, a next read.
`gregale_verify_plan` uses the original plan ID to reconstruct exact targets
from the existing journaled native receipts. It performs bounded passive reads;
it never repeats a mutation, wakes an app, promotes a deployment or broadens
consent. The mutation receipt and its recovery action retain their existing
meaning. A successful verification reports its observation time separately.

Restart verification follows the exact accepted `wake_id`. Queued, retrying and
running processing remain pending; failed processing is reported as failed.
After completion, health must be fresh and evaluated at or after the native
completion time. Configuration revision must still match the batch receipt
before and after health inspection. These observations establish restart
processing and passive app health, not the values loaded by a particular
replacement instance, public reachability or business behavior.

Deployment verification reuses native rollout completion criteria and exact
app, environment and deployment identity checks. A request deployment at zero
traffic remains blocked until an explicit promotion. Service completion needs
native handoff evidence and fresh health. Worker completion establishes resident
generation convergence only; it never claims queue delivery or processing.
Verification uses deployment lifecycle evidence without requesting account-level
build details, so app-bound credentials need no broader build-read access.

Current authorization, cancellation and immutable identities are checked before
and after evidence reads. If access is revoked during inspection, the result is
withheld. Transient verification reads retain structured read-retry guidance.
Verification obligations survive reconnects and bridge restarts because their
targets come from the durable mutation receipts. Health observations are not
stored as enduring success claims: another verification always reads again.
`get_plan` continues to describe the obligation, rather than cached health.

Core includes `gregale_get_restart` and the shared `gregale_verify_plan` tool.
Verification also accompanies release, provision and operate groups. A
configuration change without a requested restart identifies its native commit
goal and requires no additional runtime verification. Workflows without an
implemented verifier, including aggregate environment promotions, service abort
reverse handoff and provider
activation after provisioning, explicitly retain `unknown` / `manual_review`.
They are never silently reported as verified.

Apply schemas require their workflow's stable result identity, including
deployment IDs, restart wake IDs and native promotion/review/revision fields.
MCP-owned inventory tools require item identity and a typed `pagination` object
with explicit `has_more` and the matching continuation selector. Existing native
fields and exact integers are preserved. Diagnostic projections keep their
separate evidence contracts. The catalog byte budget rises deliberately from
48 KiB to 56 KiB to include verification helpers and stronger schemas; it remains
bounded and substantially smaller than the explicit full catalog.
Plan inventories now default to 20 receipts, with optional limit and offset,
so the larger verification metadata does not require returning an entire journal.

## Validation

Protocol evaluations cover delayed and failed restarts, post-restart health
freshness, configuration drift during inspection, revocation before and during
inspection, durable target reconstruction, staged deployment promotion and
absence of duplicate mutations. The published schemas are independently
validated. Existing rollout tests cover service handoff and worker residency
criteria. The deployed app-agent journey additionally applies a configuration
restart, reconnects, polls verification and checks an unchanged replay receipt.
Executing it requires a configured deployed target and credentials.

This slice changes no native REST DTO, database schema or scheduler behavior.
