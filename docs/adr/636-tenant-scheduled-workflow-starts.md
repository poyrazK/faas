# ADR-636: Tenant-scoped scheduled workflow starts

- **Status:** accepted
- **Date:** 2026-10-06
- **Decision:** Evaluate schedule-triggered workflows on tenant-required apps
  once per active tenant-to-app link. Each tenant has an independent durable
  schedule cursor, and admitted runs carry that tenant identity through the
  ordinary workflow dispatch path.
- **Why:** Tenant-bound workflows could start manually and from events, but
  recurring work had no safe admission path. Reusing the app-level schedule
  cursor would let one tenant suppress or overlap another tenant's run.
- **Consequences:** The schedule and input remain defined by the app's live
  default workflow publication. All tenant runs share the app's concurrency
  quota, while `overlap: skip` is evaluated separately for each tenant and
  workflow. Missed minutes are skipped. Candidate scans are paged by app and
  tenant identity, so an app with many linked tenants remains resumable across
  scheduler ticks. Admission rechecks the current deployment, account plan,
  tenant status, and active consumer or surface link in the transaction that
  writes both the run and cursor. Per-tenant cadence overrides are added for
  explicitly opted-in triggers by [ADR-637](637-tenant-configurable-workflow-schedules.md).
- **Rejected alternatives:** Share the app schedule cursor, which makes tenant
  overlap and duplicate suppression incorrect; derive tenant identity from the
  schedule's input or customer payload, which is untrusted; admit using only
  the candidate scan's link result, which leaves a revocation race.

The scheduler considers only active platform tenants that have an active API
consumer or active surface linked to the tenant-required app. It continues to
use the workflow's five-field cron, IANA timezone, fixed input, and overlap
policy. Each run snapshots the published workflow definition and records the
tenant in `workflow_runs.platform_tenant_id`. A tenant suspension, app-link
revocation, inactive account, maintenance mode, or deployment change prevents
new admission. Existing runs are not cancelled by those changes.

Qualification covers MemStore and PostgreSQL candidate paging, per-tenant
cursor isolation, duplicate-minute admission, tenant identity on runs,
concurrent app quota use, and suspension or link revocation before admission.
