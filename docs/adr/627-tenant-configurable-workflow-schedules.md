# ADR-627: Tenant-configurable workflow schedules

- **Status:** accepted
- **Date:** 2026-10-06
- **Decision:** An app owner may explicitly mark a published schedule trigger as
  `tenant_configurable`. Each linked tenant can then set its own five-field
  cadence, IANA timezone, overlap behavior, and enabled state through the
  tenant-self API. The tenant schedule API uses separate `automations:read` and
  `automations:manage` token scopes and optimistic versions. Workflow steps,
  scheduled input, credentials, and app-wide concurrency remain app-owned.
- **Why:** Per-tenant schedule cursors already isolate admissions, but customers
  otherwise have to adopt the app owner's cadence even when their operational
  needs differ. An explicit deployment opt-in gives app owners control over
  which workflows customers may adjust while keeping each setting scoped to
  the authenticated tenant and active app link.
- **Consequences:** A tenant starts at version zero with the published schedule
  defaults. Updates must match the latest version and increment it atomically;
  stale updates return 409. A schedule update arms from the update time so the
  current minute is not replayed. Disabling a tenant schedule prevents future
  admissions for that tenant, and already-admitted runs continue with their
  captured definition. Tenant schedules still share the app's concurrency
  quota, while overlap checks remain independent per tenant and workflow.
  Workflows without `tenant_configurable: true` retain app-owned cadence and
  cannot be changed through the tenant API. This refines the app-owned default
  in [ADR-626](626-tenant-scheduled-workflow-starts.md).
- **Rejected alternatives:** Expose every published schedule for tenant edits,
  which removes app-owner control; reuse invocation scopes, which would grant
  schedule mutation to tokens issued only to start or inspect runs; accept
  client-supplied workflow input, which would let tenant schedule settings
  change app-owned workflow behavior.

The API lists only tenant-configurable schedule triggers from the live default
deployment. Reads and writes revalidate the tenant-required app, current
deployment, account plan, active tenant, and active consumer or surface link.
The run admission transaction applies the tenant's effective schedule and
stores it with the cursor, while each run snapshots the current published
workflow plus the effective tenant trigger. Updates are independent by tenant
and workflow and do not affect other linked customers.
