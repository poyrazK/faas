# ADR-639: Verified MCP promotion and resource policy

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Stage MCP releases through the existing zero-traffic deployment
  mechanism, including first deployments and multiple dark candidates without
  reweighting siblings, verify the exact deployment preview, and promote through the existing
  locked traffic update. An explicit empty expected-serving ID requires no live
  sibling with positive traffic, allowing guarded first promotion. Optional
  role baselines gate caller-visible catalog changes before promotion. Extend
  the existing JWT edge action with an opt-in MCP resource policy, rather than
  adding a new authentication service or bypassing app/API ownership.
- **Why:** Verification after activation can expose broken servers and a failed
  candidate should not put an existing serving app into maintenance. Metadata
  discovery alone cannot prove bearer validation. Customer server runtimes
  benefit from a shared gateway JWT and execution-scope gate.
- **Consequences:** Deployment previews are required for MCP promotion. Preview
  verification uses the production OAuth resource audience. Existing serving
  apps must already have MCP ingress and streaming configured; the command
  refuses an implicit change to their shared ingress policy. App-wide bindings,
  schema migrations and gateway policies remain separately reviewed operations.
  Gateway policies advertise resource metadata, reject missing/malformed/expired
  JWTs, bound request bodies, inspect exact JSON-RPC fields, enforce execution
  scopes, and put an expiry deadline on downstream streams. Router-authored
  canonical hosts carry the same policy onto previews and aliases; JWT policy
  read failures fail closed. Providers retain
  registration, login, consent and issuance. Applications retain catalog
  filtering, task ownership and domain authorization.
- **Task operations:** Starter admission limits are atomic PostgreSQL namespace
  and owner limits, with defaults centralized in `pkg/api/limits.go`; these are
  application safeguards, not new plan quotas. Workers claim only compatible
  handler versions and may retain previous version implementations. A separate
  read-only observer can publish custom scaling metrics while workers are at
  zero. The observer itself must remain running. Publish failures emit a
  payload-free event. Delivery remains at least once and external side effects
  require idempotency using the stable task ID.
- **Release gate:** Preview status remains until untouched-lockfile native
  deployment, cold boot/restore, real OAuth/client login and native task
  restart/replica/scale-from-zero evidence passes the qualification checklist.
- **Rejected alternatives:** Enable maintenance for a rejected candidate;
  assume any nonempty Bearer is authenticated; count queue capacity before an
  unlocked insert; fail old tasks during a worker upgrade; claim scale from zero
  using only a publisher located inside the zero-replica worker pool.

This refines [ADR-426](426-mcp-hosting-contract.md), using the existing
deployment and edge-rule boundaries. It changes no VM boot or snapshot format.
