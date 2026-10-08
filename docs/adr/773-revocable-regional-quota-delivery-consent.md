# ADR-773: Revocable regional quota delivery consent

Date: 2026-10-06
Status: Accepted; regional activation remains frozen.

## Context

ADR-772 requires a regional account-admin credential for every grant retry. It
deliberately does not retain that credential. Image-release delegation has a
different purpose and must not acquire quota authority. Durable automatic quota
delivery therefore needs independent regional consent before its worker can run.

## Decision

A personally owned regional app can retain a public runtime delivery consent UUID.
The immutable scope names the operator-trusted directory issuer, global app and
global owner, and the complete original regional binding including origin and
registry generation. Consent is limited to six days and 32 active records per app,
using `pkg/api/limits.go`. It contains no secret. Separate consent UUIDs permit
explicit renewal; an expired or revoked UUID is never renewed or resurrected.

Regional account-admin authentication, MFA, verified email and personal app
ownership protect `PUT /v1/cells/{cell_id}/apps/{app_id}/runtime-consents/{id}`.
Its body is canonical `runtimequota.Consent`. GET and DELETE use the same admin
principal and retained original account/app/cell identity, allowing review and
revocation after app collection. The origin and database cell must match regional
apid configuration. Shared organization consent remains unavailable.

Credential-free grants use the existing canonical delivery object with optional
`consent_id`, at the quota path with `/delegated`. The directory signature and
allocation path are verified before reading consent. The regional store checks
the complete scope, expiry, revocation, current personal tenant and current active
verified account while holding the quota journal lock. Receipt insertion and
consent revocation serialize on that same lock; tenant/account changes are
rechecked under their own locks. Expiry is checked again after waiting for those
locks, including on retained-receipt retries. Accepted receipts retain their
original consent UUID. Later authorization or key rotation cannot replace that source.

The directory's explicit deliver operation accepts `runtime_consent_id` instead
of `regional_api_key`. It sends no customer authorization header. Issuance still
commits before network I/O, lost responses retain the same allocation and charge,
and retries preserve original receipt envelopes. Retirement strips consent and
requires only its original directory signature, including after revocation,
expiry or app collection. The two request authority mechanisms cannot be mixed.

SQL guards enforce original identity, local owner eligibility, active-record
bounds and delegated receipt source. Revocation freezes an existing admission
head. Consent rows and original receipt relations survive collection and refuse
removal or destructive downgrade. Neither consent, expiry, revocation nor receipt
storage proves native absence, drains a VM, releases directory quota or activates
a regional head. Existing credential-authorized delivery remains available.

This change establishes the receiver and explicit directory delivery path.
Directory retention/provisioning of consent and a durable delivery outbox/worker
remain required next; no automatic worker is enabled yet. Native producer
inventory/reconciliation, snapshot provenance and capture, global/shared billing,
independent regional provisioning/stores/replication and cached ingress remain
required. Serving from every selected healthy eligible region remains the default.
Dedicated native Linux x86_64 KVM metal/leakcheck/recovery acceptance is still
mandatory; the authorized internal nested GCE node supplies diagnostics only.

## Validation

Exercise original scope and canonical wire spelling, missing/foreign/revoked
consent, renewal under a new UUID, lost-response credential-free HTTP retries,
original receipt source retention and retirement after revocation. Check active
bounds concurrently in both stores. Use private PostgreSQL for restart and app
collection, direct-write immutability, retained downgrade refusal, and an observed
waiting delivery that must reject after a competing revoke commits. Run affected
race tests, pinned lint, sqlc consistency and applicable repository policies.
Exercise current account/tenant suspension, null JSON scope rejection, and fresh,
replayed and direct-SQL receipts whose account-lock wait passes consent expiry.
