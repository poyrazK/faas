# ADR-20261019120018: Durable operator resolution after schema cleanup

## Status

Implemented. Schema cleanup admission remains default off pending native
qualification. Resolution can close an existing incident even when that admission
flag has been disabled; it creates an audit record and retirement fences, not SQL
execution or a new cleanup task. Native recovery qualification is pending.

## Context

A failed or lost schema cleanup result cannot authorize replay or resurrection of
retired code. Operators need to inspect the database, stop or establish quiescence
of the original execution, repair SQL when necessary, and deploy compatible code.
The cleanup receipt must preserve the distinction between a known failure and an
unknown exit while recording that the incident has been resolved.

## Decision

`gregale deploy resolve-contract FAILED_ATTEMPT` records explicit operator
attestations, a SHA256 reference to private recovery evidence, the inspected task
and cleanup generation, and a different verified replacement attempt. Database
state and execution quiescence are operator attestations; HTTP probes cannot prove
these facts. Raw SQL, connection credentials and private evidence contents are not
accepted in this request or included in the public receipt.

The replacement must belong to the same account/project, be complete and verified
successful, have paired configuration, and be the active production graph. Fresh
attributed critical-route probes run on that graph. Artifact checks and all member
runtime/configuration fingerprints must match again in the state transaction.
Monitoring must have completed when requested by the replacement policy.

Resolution waits for retained graphs/code pins, old traffic, work and instances
to drain. The original cleanup's uncertain task is excluded only when its explicit
quiescence attestation is present; other unresolved old task executions still
block. No missing task receipt can be resolved through this action. Once eligible,
resolution atomically extends the original retirement fences to all non-current
production deployments and inserts an immutable recovery record. Existing retired
identities remain fenced forever. The task outcome and exit code are unchanged.

The record binds the operator account, request identity, task/generation, evidence
hash, replacement attempt/graph/configuration, all runtime fingerprints and a
finite timestamp. Same-request replay returns that record before checking newer
production intent; changing any attestation or identity conflicts. The endpoint
uses the existing MFA and deployment-write authorization boundary and strict
bounded JSON decoding. It accepts neither force nor caller-supplied probe evidence.

An operator-resolved lost task no longer permanently blocks future cleanup through
its missing exit code. This records execution quiescence; it never makes that task
replayable. Restored or new deployments cannot cross its retirement fences.
Recovery records are operational state and are excluded from environment cloning.
Rollback of their database migration refuses to discard recorded resolutions.

## Consequences

A corrected release can use the existing atomic verified deployment workflow.
Resolution is the final incident closure after verification and drain, rather than
permission to run another copy of cleanup. Operators must keep private SQL and
execution evidence and supply its hash; the platform does not inspect its contents
or infer semantic compatibility of arbitrary application SQL. Worker/application
behavior beyond configured probes remains subject to the replacement's existing
verification policy and operator checks.

The workflow does not overwrite production intent, remove retirement fences,
fabricate a successful exit, or provide an automatic SQL rollback. Native
qualification of the new resolution path remains required before production use.
