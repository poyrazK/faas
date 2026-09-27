# ADR-244: Attribute verified JWT identities to platform tenants

Status: Proposed

## Context

Platforms often authenticate users with their own identity provider and serve
those users through several Gregale apps. App-local consumer keys and verified
tenant hostnames cover two useful cases, but neither represents a platform's
already-signed customer identity when the same token is sent to multiple apps.
Without a trusted mapping, those requests cannot share tenant suspension,
request budgets, cross-app usage, or tenant statements.

## Decision

An account may opt a JWT edge rule into platform-tenant attribution by naming
one custom claim that contains the tenant's existing `external_ref`. Gregale
first performs the rule's existing signature, issuer, audience, algorithm,
expiry, and required-claim checks. Only then does the gateway resolve that
claim within the app's account and require the matching tenant to be active.
Unknown tenants, suspended tenants, mismatched identities, and unavailable
lookups fail closed before the request reaches the guest.

The request carries the tenant UUID through the existing shared tenant request
budget and durable usage path. Anonymous JWT-attributed usage is keyed by the
JWT authorization-rule UUID; raw claim values and JWT subject values are not
added to the financial ledger. Consumer-key and verified-surface attribution
remain unchanged and retain precedence when they already provide the request's
tenant source.

Configuration is opt-in and is scoped to an account-owned tenant external
reference. Clients cannot submit tenant UUIDs or override the claim mapping in
request headers. Deploy the apid usage receiver before gateways that emit the
new attribution field, then enable the rule; older receivers must not
acknowledge events that include the new source.

## Consequences

- A platform can reuse its signed tenant identity across apps without creating
  app-local consumer credentials solely for metering.
- Suspension and shared tenant budgets apply at the gateway before wake or
  guest execution.
- Daily tenant usage, immutable statements, and duplicate-invoice protection
  retain the originating JWT rule as a source dimension.
- The platform must provision a tenant record and configure the same stable
  `external_ref` claim in each relevant app's JWT rule.
- A token whose mapped tenant is inactive or cannot be resolved is rejected;
  this integration has no fail-open mode.
- Rule UUID attribution is intentionally less granular than the raw external
  reference and avoids persisting a potentially sensitive customer identifier.
