# ADR-1244: Native service and worker rollouts through management MCP

## Status

Accepted. Extends ADR-1241 and ADR-1243; preserves ADR-208 native service handoff.

## Context

Management MCP's request releases stage at zero HTTP traffic and require manual
promotion. Services need readiness, binding, gateway acknowledgement and drain
barriers. Worker selection uses the newest live deployment per environment,
independent of HTTP weights, so a zero-traffic image is not a held worker release.

## Decision

Add read-only workload rollout planning, exact service abort planning and exact
rollout inspection. Add two opt-in write tools for native activation and service
reverse-handoff intent. Bind plans to account, immutable app/environment IDs,
current configuration and releases; include execution mode, replica policy and
stop/readiness settings. Persist the original attempt, key and body before a
write. Existing per-grant encrypted storage, expiry and capacity apply.

Extend image admission with optional `native_rollout: service|worker` alongside
`expected_review`. Require matching target mode, omitted traffic, no runtime or
declaration overrides and an unprotected target. The state transaction checks
configuration/release identity and current protection before creating work.
The native service binding readiness gate reads the recipient deployment's
frozen replica settings, preserving the reviewed count when default intent or a
later named environment head differs. Services retain the native zero-step
`rolling_out` marker; workers retain native
stable activation. Named admission freezes the reviewed workload revision.
No MCP process writes instances, changes controller behavior or calls vmmd.

Exact service recovery optionally carries `expected_review`. Check it inside
native recovery locks, together with candidate/predecessor selection, before
writing intent. Configuration or protection changes fail closed. Existing
callers without this optional guard retain their native recovery behavior.
Acceptance remains asynchronous; binding and routing/drain controllers own
completion. MCP cannot manually advance or force-promote services.

Worker rollback redeploys an exact retained image as a new generation under
current reviewed settings. HTTP weights cannot select the old consumer target,
so the ordinary MCP checked rollback planner rejects workers. Protected targets
use existing approved environment promotion/recovery, not native rollout tools.

## Consequences

Applying a native plan can automatically change serving traffic or consumers;
plans explicitly describe this effect. Live service images remain pending until
the native handoff finishes. Worker lifecycle observations explicitly do not
confirm consumer replacement; scoped instances and queue health provide further
evidence. No synthetic worker completion receipt is introduced.

Git source staging remains request-only. Native image rollout tests exercise
MCP SDK calls, retry/restart behavior, HTTP admission and MemStore/Postgres
transaction guards. End-to-end VM readiness, gateway acknowledgement and consumer
stop behavior still require the existing native KVM qualification environment.
