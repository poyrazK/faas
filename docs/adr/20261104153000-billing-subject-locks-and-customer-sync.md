# ADR 20261104153000: Billing mutations lock their financial subject

Date: 2026-11-04
Status: Accepted
Amends: ADR-20261103131742 (organization billing email), ADR-20261104135500 (account mutations)

## Context

Account review found four billing failures. Provider-qualified identity writes
locked only human account rows, so PostgreSQL refused every organization binding
creation or subscription update. Overlapping organization billing email changes
could both succeed while leaving different emails locally and at the provider.
Billing information synchronization used the legacy customer cache even when the
active provider had a different binding. Clearing a billing address omitted the
address from Stripe's update, preserving its old value.

## Decision

Identity writes retain their existing human account lock, or lock the shared
organization row when that is the financial subject. The organization lock is
`FOR NO KEY UPDATE`, compatible with foreign-key key-share locks. A personal
organization is not a separate billing subject.

Organization billing email changes hold that organization's row lock from the
current billing identity read through the provider update and local email save.
Provider synchronization receives the qualified customer and requested email from
the same transaction and performs no store calls. It uses one database connection
and is not retried within the transaction. Independent apid processes therefore
serialize the entire change. Membership fencing remains in the transaction;
provider errors roll back the local save and keep their existing HTTP mapping.
MemStore provides the same ordering under its mutex.

The provider and PostgreSQL remain separate systems: an ambiguous provider result
or a failed local commit after a provider success still requires retrying the
requested email. The change does not claim a distributed transaction or background
reconciliation. Successful overlapping requests commit in the same order on both
sides.

Billing information updates resolve the configured provider's qualified customer
before synchronization. A missing binding skips synchronization; another
provider's compatibility cache is never used as a fallback. Local partial updates
retain their existing success behavior during provider outages.

Stripe customer updates explicitly send an empty `address` form value when the
saved address is empty. Customer creation and nonempty address updates keep their
existing form representation.

## Validation

Regression tests cover account and organization billing identity creation,
subscription replacement and clearing; overlapping email mutations and provider
refusal/retry against memory and PostgreSQL; a one-connection pool; real HTTP
organization checkout and email mutation on PostgreSQL; active-provider selection
with stale, empty and missing caches; and Stripe address clearing against a local
HTTP endpoint.
