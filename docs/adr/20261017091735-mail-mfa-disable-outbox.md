# ADR-20261017091735: Durable internal MFA disable-email requests

## Context

The existing email-assisted MFA disable endpoint saves a token and then sends
inline. A crash or transport failure can lose the link. Confirmation consumes
the token and clears MFA in separate transactions. Saved requests also need an
explicit expiry and binding to the factor they are allowed to remove.

## Decision

Add the exact internal `FAAS_INTERNAL_MAIL_MFA_DISABLE_OUTBOX_ENABLED=0` gate
to apid, maild and private reconciliation. New issuance remains disabled by
default. The existing authenticated endpoints, CSRF actions, session requirements
and response shapes remain in place; this adds no public mail service or listener.

Enabled issuance takes admission, account/update and shared recovery locks. It
checks the current sign-in account policy, address and MFA enrollment, invalidates
older durable and inline requests, and saves a purpose-specific token hash and
encrypted `mfa_disable_request` mail intent in one transaction. Save failure or
late gate withdrawal rolls back replacement as well. Enabled issuance never falls
back to inline delivery. A deliberate new user request starts a fresh cooldown;
worker retries never issue a new request or change its token or timestamps.

Use three explicit database deadlines: delivery expires 24 hours after request;
confirmation becomes available at 24 hours and expires at 48 hours. The latter
provides a full confirmation day after the existing cooldown, rather than making
the link expire when it first becomes usable. The frozen V1 body gives the
confirmation and expiration times. The old inline format keeps its existing
behavior, including its historical absence of a stored expiry.

The new `mfa_disable_tokens` table is separate from inline requests, verification,
reset and sign-in tokens. It binds account, normalized address, password revision,
MFA revision/enrollment timestamp, namespace, recovery generation and absolute
timestamps. MFA secret replacement, enrollment, clear and actual policy changes
advance a persistent account MFA revision even with every mail flag off. Recovery
code burns retain the same enrolled factor. Restoring old secret bytes or password
hashes cannot revive an earlier link. Inline reissuance also invalidates pending
durable credentials under an account-first lock.

Confirmation locks account before token and recovery, rechecks every binding and
the database clock, and clears the factor and consumes the token in one commit.
Early confirmation returns the existing cooldown problem without consumption.
Concurrent confirmation has one winner; storage failure rolls back both writes.
Explicit MFA policy is retained, and confirmation issues no session or step-up
proof. This credential enforcement remains wired after issuance/handoff rollback
and requires no mail encryption key, host MFA identity or trusted origin. Only an
unknown hash in the legacy sealed-token format may enter the old inline reader;
unknown raw 32-byte tokens remain invalid without consulting host keys.

Use the dedicated apid current/previous credential readers under their existing
historical verification identity names. The trusted canonical HTTPS origin is
`FAAS_INTERNAL_MAIL_MFA_DISABLE_ORIGIN`; the frozen path is
`/auth/reset/mfa`. Request authority never selects a link.
Encryption binds the raw random token to immutable intent metadata, its hash,
template, origin, original mail deadline, account and recovery generation. No
TOTP secret or recovery code enters notification intent. Stable admission key
`mfa-disable:<intent UUID>` and frozen body survive restart or lost admission
acknowledgement. Maild never receives apid credential identities.

The same default-off child gate protects `GET /auth/reset/mfa` before
authentication or token parsing. It renders a confirmation form for the current
account with no token consumption, automatic request or change to MFA. The page
uses no-store, no-referrer, CSP nonces and removes the query from browser history.
Only the explicit button obtains the existing account/action-bound CSRF token
and posts to the existing confirmation endpoint. Pending MFA sessions can reach
this gated recovery page. Flag rollback hides the new page while saved credential
checks remain enforced in the existing POST endpoint.

The page stays under the existing `/auth/reset/` control-plane reservation, so
the gateways need no new reserved root or route configuration.

The own child gate controls issuance, claims, marked transport, maild admission
and receipt reconciliation/audit replay independently of sibling templates.
Maild checks it before the body and again after owner waits. Claims require a
readable key and matching saved origin. Startup requires all readers referenced
by its unexpired pending payloads. No recovery advisory fence crosses HTTP.

## Consequences

Apply the additive migration before the new binary. Producer readiness checks
the MFA revision and token table. Shared queue limits, budgets, suppression,
lease fencing and recovery quarantine apply. Address, password or MFA changes,
account unavailability, used tokens or expiry cancel pending credential delivery.
Recovery holds refuse issuance and confirmation; a new recovery generation
quarantines saved credentials and mail even after global release.

Terminal settlement, expiry and reconciliation erase recipient and sealed
credential fields. Bounded base maintenance purges expired token hashes and
bindings even while the child gate is off. Account erasure cascades credentials
and intent; opaque reconciliation audit survives. Migration Down refuses token,
intent or audit history. Flag rollback preserves all saved security bindings.

Maild admission is distinct from destination delivery. Qualification remains
separate. There is no forced resend, cooldown bypass, deadline extension, key
enrollment, DNS mutation, deployment or flag activation in this change.
