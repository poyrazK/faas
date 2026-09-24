# ADR-226 · Account-level platform tenants

- **Status:** accepted
- **Date:** 2026-09-24
- **Decision:** Introduce one account-owned end-customer identity that can bind existing API consumers and tenant surfaces across apps, report their durable raw usage together, and suspend their linked credential and hostname paths.
- **Why:** API consumer identities, usage, and tenant surfaces are currently app-owned. A platform with several apps must maintain an external join table and perform multiple independent revocations to manage one of its customers.
- **Consequences:** The account-level identity has a stable `external_ref`, a bounded account quota, and a reversible `active`/`suspended` state. Existing app resources remain unbound until explicitly linked. Account deletion cascades; application deletion removes its linked resources without deleting the account-level tenant.
- **Rejected alternatives:** Inferring tenant identity from equal app-local `external_ref` values, copying keys between apps, and revoking each key irreversibly on suspension.

## Ownership and lifecycle

`platform_tenants` is keyed by `(account_id, external_ref)`. Repeating a create with the same name returns the existing row; a different name conflicts. The account row serializes creates and the maximum number of tenants follows the existing `ConsumerKeysPerAccount` plan ceiling. This avoids an unbounded second customer registry. GET lists are paged at at most 100 rows.

An app consumer or tenant surface has at most one platform tenant. The nullable link is account-checked in the API, store, and a composite foreign key. No migration guesses a link from matching names or references. A resource already attached to a different tenant conflicts; attaching it twice to the same tenant is safe to retry. Revoked app consumers remain linked for historical usage attribution.

Suspension does **not** revoke or mutate the linked credentials. The gateway's consumer-key lookup fails closed while their tenant is suspended, and key issuance is refused. When tenant-surface routing is enabled, the tenant-surface router treats a linked hostname as claimed but unroutable while suspended; it must not fall back to a legacy custom-domain row for the same host. Resuming restores previously active keys and surface routes. This is an access gate for **linked** credentials and hostnames, not a claim that all possible access to an app is disabled: anonymous requests, unrelated app domains, and independent JWT auth retain their existing policy.

The runtime claim is sourced only from the verified consumer record, never from a request header or hostname. The gateway stamps it as `X-Faas-Platform-Tenant-Id` while retaining `X-Faas-Tenant-Id` for the owning account, and adds a `platform_tenant.id` trace attribute. A linked consumer key used on a different tenant's linked hostname is denied before wake. Route caches hold the surface ID separately from the app row, and cached custom-domain hits re-read current surface/tenant status so suspension and binding changes cannot be bypassed by a warm route. This adds an indexed read to cached custom-domain requests and fails closed if that guard is unavailable; stale-route fallback cannot revive a linked or suspended surface.

Usage reads join the existing idempotent per-minute consumer ledger through linked consumer IDs and return raw counts grouped by UTC day, app, and consumer. They neither reprice app-specific rate cards nor create an invoice. The existing 90-day maximum usage window applies.

## Activation and compatibility

The migration is additive and replay-safe if its schema is present but its goose ledger row is missing: existing consumers and surfaces have null `platform_tenant_id` and retain their present behavior. Public routes use the existing MFA-gated read and deploy-write scopes. The tenant feature is unavailable on plans that do not allow consumer keys. Platform tenant suspension requires both API and gateway binaries to understand the new schema before customers link resources; deployment must run migrations first.
