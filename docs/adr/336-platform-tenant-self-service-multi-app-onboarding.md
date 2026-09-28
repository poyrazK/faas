# ADR-336: Tenant-scoped self-service multi-app onboarding

- Status: accepted
- Date: 2026-09-27
- Owners: Platform API
- Related: ADR-284, ADR-288, ADR-290, ADR-291, ADR-292

## Context

A downstream platform tenant can provision one customer identity at a time on an active surface already linked to its tenant. Platforms that represent the same customer in several apps must repeat that call and coordinate partial failures themselves. This is awkward for automation and can leave a customer's platform footprint inconsistent across apps.

The existing model deliberately keeps consumer identities app-local. A surface maps to one app; a customer therefore needs a distinct consumer identity for each app, even when the platform uses the same external reference and name. Tenant access tokens must not be allowed to choose arbitrary apps, link new surfaces, or bypass the account owner's provisioning policy.

## Decision

Add `POST /v1/platform-tenant-self/consumers/apply`, authorized only by a tenant-bound `platform_tenant:consumers:manage` token. The tenant is derived from the authenticated token. The request supplies one `external_ref`, one `name`, 1-100 unique surface IDs, and an optional `dry_run` flag. Every surface must already be active and linked to that tenant. Gregale derives the app from each surface and rejects a batch that selects multiple surfaces in the same app.

The operation validates the entire batch before mutation and creates or safely replays one app-local identity per surface in one database transaction. Any conflict rejects the whole batch. New identities require the owner's separate provisioning policy to be enabled, and the tenant-wide active-customer cap is checked against the full number of identities the operation would create. A batch consisting entirely of exact active replays remains successful after provisioning is disabled or the cap is lowered. A suspended tenant cannot create identities.

`dry_run: true` performs the same authorization, surface, identity-conflict, policy, and quota validation without writes. Its per-surface response says whether the real call would create or leave an identity unchanged. It omits IDs for planned creates and emits no audit event. A committed request returns one redacted result per surface and emits a single audit event only when at least one identity was created. The API never returns app IDs, account IDs, key material, or plaintext credentials.

Customer creation and credential issuance remain separate operations. This keeps the owner's identity-onboarding gate distinct from credential delegation policy, gives platforms a clear reconciliation boundary, and avoids implicitly generating secrets that must be delivered or stored.

## Consequences

- Platforms can onboard one customer consistently across a selected set of linked apps with a single retry-safe request.
- The database transaction prevents partial bundles, while the tenant row lock serializes batches against the customer cap.
- A preview is advisory: policy, cap, surface status, or conflicts may change before the real apply, which revalidates all conditions.
- Platforms still need to make a separate credential-apply request after customer IDs exist.
- The per-app consumer identity model and owner-controlled provisioning limits remain unchanged.
