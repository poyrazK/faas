# ADR 20261104170000: Account mutations preserve retryable failures

Date: 2026-11-04
Status: Accepted
Amends: ADR-032 (password signup), ADR-039 (account sessions), ADR-20261103131742 (billing contacts)

## Context

Password signup committed the account and personal organization before saving its
credential. A password write failure left an unverified account without a password:
retrying signup returned invalid credentials, and neither verification nor password
reset mail could complete the original signup. Both password signup endpoints had
this ordering.

The rotation grace-window endpoint ignored its persistence error, returned the
requested value and emitted a success audit event. Billing-contact removal and
account-deletion guards likewise treated some failed lookups as proof that the
account was not a billing contact.

## Decision

Account creation accepts an optional, precomputed `InitialPasswordHash`. PostgreSQL
commits the account, personal organization, owner membership and credential in one
transaction, using the existing revision-aware password writer. MemStore publishes
the same state under its mutex, after uniqueness and cancellation checks. The
optional field is excluded from JSON serialization; creation results never carry
the credential. Trusted creation without a password retains its existing behavior.

Both password signup endpoints hash before creation and pass the hash into that
atomic mutation. Signup accounting, verification mail, sessions and API-key issuance
remain after a successful commit. Duplicate-account retries continue to verify the
existing password; they never replace it. A failed credential insert therefore
leaves the address available to the next signup attempt.

A failed grace-window save returns retryable capacity 503, preserving the cache and
omitting the success audit event. PostgreSQL reports a missing account instead of
silently succeeding after updating zero rows, matching MemStore.

Billing-contact checks return errors separately from a confirmed negative result.
Member removal and account-deletion scheduling return 503 on a lookup failure,
before any mutation. A confirmed billing contact still receives 409. A missing
member account retains the removal endpoint's existing not-found behavior.

## Validation

Tests cover atomic account/credential creation, duplicate and canceled creation,
credential serialization, and a real PostgreSQL credential-insert failure that
rolls back all four resources and permits a same-address retry. HTTP regressions
cover both signup endpoints, failed grace writes and recovery, and billing-contact
lookup failures across memory and PostgreSQL stores. The latter verify that
membership and account status remain unchanged, healthy guards still reject the
contact, and moving the billing contact permits the requested mutation.
