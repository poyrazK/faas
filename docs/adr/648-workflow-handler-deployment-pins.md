# ADR-648: Workflow handler deployment pins

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-081, ADR-487, ADR-646 and ADR-647
- **Decision:** Newly accepted manual, application/tenant schedule, internal event,
  and verified webhook runs capture an immutable `deployment_id` alongside their
  definition snapshot. Every same-app handler, condition check, failure handler,
  and loop item executes that deployment across waits, retries and resumptions.
  Event and webhook acceptance retain captured code before run admission.
- **Why:** A workflow can wait for days. Deploying a new handler during that wait
  must not silently change the code that performs the next step.
- **Consequences:** Authenticated scheduler delivery carries the durable run ID.
  An explicit revision selector in transport makes older gateways select the
  exact code or reject delivery. Updated gateways resolve private retention from
  the ledger and require any transport selector to match it. The admission check
  validates run, tenant, step and attempt before assigning internal `Invocation.WorkflowRunID`. Version selection reads the
  authoritative run and routes through existing exact-deployment wake and target
  verification. Customer JSON and headers cannot assign this private identity.
  Missing, foreign, deleted or non-live pinned code fails closed.
- **Retention:** Retained runs, including terminal history eligible for resume,
  and captured event outbox recipients privately retain their source deployment.
  Atomic database guards lock app, deployment and immutable artifacts before
  publishing references; deployment identity cannot change after insertion.
  Deployment retirement keeps referenced code live at zero public traffic.
  Expired cleanup receipts enable ordinary retirement after the last reference
  is pruned. Existing terminal-run/event retention and app/account deletion
  continue to determine lifetime. No public revision deadline is extended.
- **Compatibility:** Nullable migration preserves legacy runs without inventing
  code identity. API/SDK responses omit `deployment_id` for those runs; CLI labels
  them `legacy (unpinned)`. Legacy routing remains best effort. Existing event
  snapshots with captured deployment identity use that identity and fail closed
  if the code had already been retired before migration. Deploy the schema first,
  upgrade all internal gateways, then upgrade the run producers and workers. This order ensures older workers cannot deliver a new
  pinned run through an older gateway. Older producers can still create unpinned
  legacy rows during a rolling upgrade.
- **Scope:** Pins cover the source app's deployed handler code. They do not freeze
  provider behavior or downstream project release graphs. Runtime configuration
  and credential revocation follow existing deployment and integration rules.
  Existing deployment disk accounting applies to retained artifacts; no instance
  stays resident merely because its code is pinned.
  Resuming retains the original pin. To use new handler code, start a new run.
- **Rejected alternatives:** Resolving live code at each step (mixed versions);
  public revision TTLs as workflow retention (separate lifetimes and access);
  guessing historical run deployments (unverifiable); deployment TTL alone
  (durable waits can outlive a fixed deadline).

This changes only admission, persistence, retention and selection of an existing
exact-deployment wake. It introduces no VM lifecycle transitions or new wake API.
