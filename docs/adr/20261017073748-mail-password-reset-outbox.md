# ADR-20261017073748: Durable internal password-reset email

## Context

The inline forgot-password flow persists a shared login-token hash before
calling a mail transport. A failed send leaves no durable notification intent.
The private producer outbox already supports encrypted, address-bound verification
credentials and generation-fenced maild admission.

## Decision

Add `FAAS_INTERNAL_MAIL_PASSWORD_RESET_OUTBOX_ENABLED=0` independently in apid
and maild. Enabled apid issuance commits a separate `password_reset_tokens` hash
and encrypted `password_reset` intent together. It requires the existing producer
outbox prerequisites. It adds no customer mail API, listener or entitlement.

Use the dedicated apid current/previous credential identities, with an explicitly
configured HTTPS `FAAS_INTERNAL_MAIL_PASSWORD_RESET_ORIGIN` and frozen `/auth/reset`
path. The historical outbox `verification_*` columns also hold reset credentials;
the authenticated binding includes the template, account, address, hash, origin,
deadline, producer, intent and recovery generation. maild receives no apid key.

Retries preserve the same 32-byte token, rendered body, `password-reset:<intent>`
idempotency key and original 15-minute database deadline. Namespace admission,
shared pending limits, recovery quarantine, suppressions, leases, terminal secret
erasure and audited admission reconciliation continue to apply. Both ends
independently gate the template, including after admission lock waits. Unknown
reader keys and changed configured origins prevent claims.

Bind reset credentials to normalized address and a persistent account password
revision. The only production password writers, `PgStore.SetAccountPassword`
and `DeleteAccountPassword`, now use sqlc account-first CTEs that advance this
revision atomically with their write. Deleting and restoring the same PHC cannot
revive an older link. Dispatch holds account/share, outbox/update and token/share
locks across bounded handoff; password mutation and redemption lock account first.
No recovery advisory lock is held across HTTP.

Dedicated reset redemption changes password, advances revision and consumes the
token in one transaction. It checks active verified account, address, revision,
consumption and expiry, including the database deadline after password-row waits.
Two sibling links have at most one winner. Reset credentials also save their
namespace and recovery generation. Redemption holds its shared recovery fence
and rejects a saved hold or generation mismatch after release or flag rollback. Enforce saved credentials with the
issuance/delivery flags off, without loading keys or origins. New reset hashes
never enter `login_tokens`, so magic-link login cannot consume them.

The existing inline token family remains compatible when the new issuance flag
is off, including already-issued legacy links. Its shared login/reset semantics
are unchanged; the new purpose and revision bindings apply to durable reset
credentials. The public forgot-password response stays uniform, including queue
failure. Enabled queue failure never falls back inline or leaves an orphan token.

## Consequences

Apply the additive migration before starting the new binary. The base outbox
readiness check requires the reset schema. Expired reset hashes/address bindings
are purged in bounded owner maintenance; account erasure cascades tokens/intents.
Rollback refuses any reset intent, credential or reconciliation history, and
restores the prior verification constraints only after that state is absent.

Maild admission is separate from delivery. A message already admitted can arrive
after account or credential changes; redemption still rejects its stale link.
An expired link requires a fresh forgot-password request. This change performs
no live qualification, operational key creation, DNS change or deployment.
