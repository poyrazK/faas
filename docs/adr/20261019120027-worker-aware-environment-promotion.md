# ADR-20261019120027: Coordinated HTTP and push-worker environment promotion

## Status

Implemented behind `FAAS_PROMOTION_WORKERS_ENABLED=1` and the existing promotion
health gate. Native x86_64 KVM qualification remains pending. Admission defaults
off; admission changes do not interrupt already accepted promotions.

## Context

A tested HTTP release can be promoted with its configuration and dependencies,
but an API and queue consumer also need a common release boundary. Selecting the
worker independently can execute new jobs against incompatible code or move a
retry between versions. ADR-20261019120008 provides startup/configuration proof, ADR-20261019120009
provides real delivery evidence, and ADR-20261019120011/1012 preserve job identity and retained
worker pools. Environment promotion must participate in those existing contracts.

## Decision

`projects environments preview/promote --workers --sync-config --verify` admits
HTTP services and exactly one existing production push consumer. Both environments
must have complete active release graphs with pinned HTTP configuration. A worker
without a variable snapshot additionally needs a live startup/configuration proof
matching its current source runtime; untested live variable edits are refused. The destination must be production.
The source and retained destination worker must have frozen worker execution settings
and the same bounded stop grace. Multiple consumers, pull/job workers, sidecars,
reload hooks, owned work policies and migration combinations (including mixed-worker repair for a migration incident)
remain unsupported in
this first contract. A separate qualified migration release can precede promotion.

The capture hashes the production queue binding and consumer identities, settings
and revisions, together with the source artifact and destination runtime identity.
Destination credentials, queue assignments and consumer policy remain destination
owned. Source non-secret variables and artifact code follow the tested release;
source queue policy never replaces production intent. Preview tokens, approvals,
idempotent requests and publication bind the captured worker contract.

Candidate ownership comes from the persisted promotion and prepared deployment spec.
A derived view authorizes scheduler startup and prevents stale drain decisions during
preflight. The scheduler can recover an absent candidate after a restart, using
existing worker placement, quotas and the bounded overlap grant. This hold never
admits production jobs: the current project graph remains authoritative. Startup
can still cause arbitrary customer-code external effects, as for existing worker
releases; no exactly-once execution claim is made.

Preflight requires current immutable worker configuration/startup proof, healthy
baseline delivery evidence and the existing HTTP probe rounds. Graph, variables,
workload settings and new-job admission publish in one project transaction. Startup
proof and queue intent are checked again at that boundary. Post-cutover health
requires fresh attributed delivery windows alongside HTTP health; low traffic and
missing results stay unknown. Worker failures, retries, duration, queue delay and
backlog use the existing worker health policy. There are no synthetic production
jobs and no client-supplied worker verdicts.

A job's durable first-admission graph survives retries and rollback. Retained worker
pools continue to serve eligible pins within existing retention/capacity limits.
Rollback warms and verifies the captured previous worker before restoring the graph
and configuration; a durable bounded rollback hold permits recovery even after the
old pool drained. Unknown baseline health, changed bindings, newer destination intent
and expired retention fail closed. External job effects are never reversed.

Worker callers may use their frozen declared HTTP service bindings in the selected
candidate or retained graph. A worker is never an HTTP binding target. Tenant,
scope, caller grants and target protocol guards remain unchanged.

## Validation

Portable Memory/PostgreSQL, API, CLI, scheduler and SDK checks exercise the contract.
Native acceptance must prove guest startup/configuration delivery, queue isolation,
job pinning, worker loss/restart, controller recovery, rollback and leak freedom
before enabling admission. See the promotion native acceptance runbook.
