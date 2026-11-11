# ADR-20261103171917: Atomic MCP configuration and durable provisioning

Status: accepted

Extends [ADR-20261103155008](20261103155008-management-mcp-agent-workflows.md),
the configuration revision/guard design and the native resource APIs.

## Context

An agent's reviewed multi-variable change previously issued individual writes.
A failure or another writer could leave only part of the approved configuration.
Asynchronous provisioning kept the running process responsive, but its unfinished
CLI inputs and resource progress disappeared when the bridge restarted.

## Decision

`POST /v1/apps/{slug}/env/batch` accepts a UUID request ID, an expected scope
revision, non-secret values and keys to delete. It uses the existing `env:write`
authorization, scope resolution, plan limits, GitOps ownership rules and config
guard policy. One store transaction compares both the history revision and the
live configuration, applies every key, records one `env.batch` revision,
invalidates snapshots, stamps runtime configuration and saves a value-free
receipt. A missing history starts with baseline revision 1. Failures within the
batch transaction roll back every effect. No secrets are added to this endpoint.

The PostgreSQL transaction takes the existing scope advisory lock, an app-wide
quota lock and the parent app row lock. Value-table triggers make legacy env and
secret writers participate in those advisory locks. Live-state comparison
refuses a legacy write whose separate history recording has not finished,
including the very first write: revision zero requires an empty scope. An older
nonempty scope without history gets a metadata-only baseline initialization:
the request commits that baseline, refuses all variable changes, and returns
the same revision conflict that requires a fresh review and new plan. It creates
no guard, runtime invalidation or batch receipt. A concurrent first legacy write
is therefore preserved and older manifests do not need a manual variable write
to enter the versioned workflow.
Deadlock/serialization aborts retry the entire batch up to twice under the
original revision and request ID. Memory storage provides the same atomic
contract under its lock. Native database GitOps triggers remain authoritative.

Receipts are scoped to the owning account, app, scope and request ID. They are
stored with a hash of the normalized original body, retained at least 24 hours,
and replayed before checking the current revision. Changed bodies conflict.
The receipt table's `request_sha256` is a one-way SHA-256 digest of non-secret
configuration, not ciphertext or a decryption key. It is classified as an
unsealed digest alongside the existing request hashes in `sealdomaincheck`.
After receipt pruning, the original expected revision still prevents repeating
the mutation. MCP derives the request ID from the immutable plan ID and keeps
the original revision on every retry. A fresh restart, if requested, remains a
separate idempotent operation after the config transaction.

With a persistent bridge state store, provisioning uses native domain, cron,
bucket, PostgreSQL and binding APIs. The existing encrypted connection journal
stores validated inputs, phase, resource/binding IDs, cancellation and sanitized
errors. Each admission/checkpoint persists before its next external mutation.
An incomplete operation remains resumable for 24 hours from first admission;
a completed value-free receipt remains available for 24 hours after completion.
The operation ID is the plan ID. Polling never resumes or creates resources.
An interrupted status supplies a structured same-plan apply call; an explicit
apply rechecks current connection consent and native authorization.

Native cron, bucket and binding reservations already recognize identical
retries. Domain recovery reads the existing claim and verifies app and scope
before adopting it. PostgreSQL accepts an optional UUID `request_id`, passed
to its existing reserved-ID workflow. The bridge derives and journals that
identity before creation, so a response-cache loss or renewed credential cannot
adopt another workflow's database by name. Native binding admission opens only
the database egress it needs; the bridge does not overwrite general egress.

Cancellation stops the worker and persists its intent. It does not delete
resources or undo bindings. Explicitly applying the same plan resumes the
approved operation under current authorization. Existing worker bounds,
authorization deadlines and lifecycle cancellation continue to apply.
Ephemeral local connections retain the CLI flow and its in-memory unfinished
plans. Temporary source uploads retain their separate lifecycle.

## Validation and rollout

Contract tests run against memory and migrated PostgreSQL, including competing
revisions, receipt replay after later writes, quota refusal and an injected
mid-batch database failure. Native HTTP tests cover the batch and reserved
database identity. Bridge tests lose responses after resource and binding
commits without an HTTP replay cache, reopen the encrypted journal, poll without
writes, and resume each checkpoint; cancellation and revoked credentials are
covered separately. The deployed app-agent journey checks a multi-key batch and
its repeated receipt. Deployment execution requires a configured target.

The migration expands revision source checks online, creates the receipt table,
adds value-write serialization triggers and grants the runtime role access.
Deploy the migration before the new server and bridge. Down refuses while
`env.batch` history exists, because old checks cannot represent that history.
