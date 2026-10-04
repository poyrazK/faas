# ADR-375 · Declarative platform tenant ingress for project workloads

- **Status:** accepted
- **Date:** 2026-09-29
- **Decision:** Compose workloads may declare `x-gregale-platform-tenant-required`
  as a boolean. Project scan/apply and source-reference deploys expose the
  existing app ingress policy without changing its identity sources.
- **Why:** A customer platform must enforce verified customer identity from
  app creation and retain its policy through repository reconciliation.
- **Consequences:** Scan exposes the requested policy. Reconciliation writes it
  atomically with app intent before enqueuing builds. Omission preserves a live
  or restored app's policy; explicit false disables it. A new app defaults to
  false. The existing Hobby entitlement applies before any app mutation.
- **Rejected alternatives:** A post-deploy PATCH leaves a window before policy
  enforcement. Treating omission as false silently opens previously protected
  apps during unrelated repository changes.

The project request's optional `platform_tenant_required` field overrides only
selected workloads for that scan/apply pair and is bound into the plan token.
It persists on the affected app rows but is not a project-wide default for
future workloads. Compose carries durable source intent for push reconciliation.
Managed services cannot declare this app-specific policy.

Atomic update and restoration use an explicit presence bit so an omitted field
cannot overwrite a concurrent app policy update. Restoring a soft-deleted app
preserves its previous ingress policy unless the source explicitly overrides it.
Operator authentication and public-auth gates remain independent; enabling the
tenant policy does not relax them.
