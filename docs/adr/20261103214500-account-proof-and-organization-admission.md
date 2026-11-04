# ADR-20261103214500: Account proof and organization admission

Date: 2026-11-03
Status: Accepted
Amends: [ADR-076](076-session-binding-hash.md), [ADR-061](061-organizations-and-memberships.md), [ADR-20261103202000](20261103202000-account-authentication-and-recovery.md)

## Context

A session could obtain a new MFA secret and use it as independent proof to
replace an existing password. Session binding compared only saved values,
so copying a cookie to a different client preserved the comparison. Email
ownership claims cleared pre-verification MFA but returned the old snapshot.
Organization creation and invitation acceptance checked limits without a
shared transaction lock; acceptance also treated a zero limit as unlimited
and ignored deletion state.

## Decision

Verification and recovery require an enrolled factor. Confirming enrollment
requires the current password if one exists, in addition to the new TOTP and
action-bound CSRF token. OAuth-only accounts retain their session-based
opt-in. The management form asks for this proof, including resumed setups.
The existing password-failure budget also covers enrollment proof. Durable
and inline confirmation use the same HTTP proof requirement.

An armed session checks the request's client IP and browser family using the
same trusted-proxy helper as issuance, as well as checking its envelope
against the live row. A mismatch revokes the session, audits and clears its
cookie. Browser version updates within a family are tolerated; changes of IP
or family require sign-in again. Unarmed legacy/internal sessions retain
their existing behavior. Ownership claims reload the account after clearing
pre-verification credentials before deriving the new session's MFA policy.

Shared organization creation locks the owning account, checks its Free
organization count, and inserts the organization and owner in one transaction.
Memory storage performs the same operation under its mutex. A failed owner
insert leaves no organization or reserved slug.

Both invitation acceptance paths lock the target organization with
`FOR NO KEY UPDATE` before checking status, plan and member count. This
serializes concurrent accepts and lifecycle/plan updates while remaining
compatible with foreign-key locks. Zero member limits refuse admission.
Pending deletion, inactive status and abuse holds refuse admission without
consuming the invitation. Removed memberships can still rejoin an active
organization within its plan limit.

These corrections apply directly, without new feature flags or migrations.

## Validation

HTTP regressions cover setup proof, incomplete-factor refusal, ownership
claims, request fingerprint replay, trusted proxy handling and concurrent
creation. Store tests cover memory and PostgreSQL, both invitation paths,
last-seat contention, inactive/downgraded organizations, concurrent creation
and rollback of a failed owner insert.
