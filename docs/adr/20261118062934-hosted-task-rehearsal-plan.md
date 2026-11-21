# ADR-20261118062934 · Owner-approved hosted Task rehearsal plans

- **Status:** Accepted for preview planning
- **Related:** [Hosted Task binding qualification](20261117135916-hosted-task-binding-qualification.md), [Contained release commands](20261020150000-rehearsal-contained-release-commands.md), [Isolated PostgreSQL fixtures](20261017201009-rehearsal-postgres-fixtures.md)

Hosted Task source review approves retained bytes. Binding qualification is a short-lived observation of the reviewed native setup. A rehearsal needs its own approval, scoped to an isolated run and independent of release authority.

## Decision

With rehearsal planning, source retention and binding verification enabled, an unscoped personal administrator API key can prepare one immutable plan per qualified source identity. The plan pins the intent, review and binding digests, the observation time and original evidence deadline, and a server-assigned disposable target identity with its own queue namespace. The target identity is reserved metadata. A future executor must allocate fresh contained resources under that identity, including a disposable database. The plan accepts no caller-selected database, namespace, app or secret reference. It has zero traffic authority.

An owner approves or revokes the exact plan digest in a separate request. Approval pins the native administrator key privately so its authority can be rechecked later. Approval and revoke replay the original receipt. Metadata inspection and revocation remain accessible while admission is disabled. The plan never outlives the passing observation, which expires within one minute and cannot extend the retained source lifetime.

A private store operation consumes the approval once. The caller must present the approving personal key and exact plan digest. PostgreSQL checks retained source, binding selection and completed evidence, app and credential pins, and native key authority under the same transaction as the one-time claim. It checks the real clock after lock waits and in the write predicate. Expiry, drift and revocation permanently invalidate unconsumed plans. The native claim transaction also inserts one private run reservation keyed by the retained intent. It pins the plan digest, target identity, claim time and original approval deadline. A reservation failure rolls back the claim. Replays return the same reservation without acquiring execution ownership. Historical claims made before reservations can return their plan status without creating a run. The API and CLI expose planning, inspection and owner decisions. They return closed metadata without source, credentials or binding references.

The plan and run journals follow the binding identity lifetime and are kept outside environment clones. Keeping them separate preserves reservations during rolling upgrades with older plan writers. Plan creation, approval and private claim do not provision capacity, open retained source, run probes or migrations, deploy workloads, or alter traffic. Connecting the claim to a contained runner requires a separate change with execution and cleanup evidence.

## Validation

Native memory and PostgreSQL tests exercise owner authorization, immutable replays, credential or authority drift, and concurrent claims. The OpenAPI and CLI contracts expose the preview owner operations.
