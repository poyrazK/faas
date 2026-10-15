# ADR-1246: Reviewed native source rollouts through management MCP

## Status

Accepted. Extends ADR-1243 and ADR-1244.

## Context

Commit-pinned MCP source staging supports request apps, while services and workers
need automatic native activation after their artifacts build. Zero HTTP traffic
cannot hold a worker generation and manual promotion bypasses service handoff.

## Decision

Extend the existing source planning tool with explicit `rollout_mode` service or
worker, requiring a matching reviewed workload. These plans use the existing
workload apply tool. The source API accepts `native_rollout` only with an exact
reviewed source identity, omitted traffic and an unprotected matching target.
Enqueue carries transient reviewed rollout metadata into the native store's
locked review admission. Named target settings and declarations remain pinned;
repository settings, triggers and release commands are not imported.

Source builds stay inside native builderd microVMs. Native service readiness,
routing acknowledgement and drain gates and worker generation reconciliation
remain authoritative. MCP does not add lifecycle writers or promote weights.
Encrypted plans preserve source identity, retry key/payload and build/deployment
receipts across restart. Ordinary API idempotency and review admission apply;
this does not add a new durable source admission journal.

## Consequences

Request staging remains the default. Service/worker source plans require explicit
mode and the workload apply tool. Protected targets require approved environment
promotion. Worker convergence remains residency evidence, not queue completion.
Retained source rollback is still outside the retained-image rollback tool.
Native build and lifecycle qualification must run on a nonproduction KVM host;
portable tests and protocol fixtures do not establish native qualification.
