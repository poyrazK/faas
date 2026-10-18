# ADR-20261017101020: Durable internal password-change security alerts

## Context

Password changes in account settings and completed password resets need a
notification that cannot be lost between the password write and mail handoff.
Sending before commit could notify about a change that never happened. Sending
inline after commit could lose the event during an outage or process failure.

## Decision

Add the exact internal default-off
`FAAS_INTERNAL_MAIL_PASSWORD_CHANGED_ALERT_OUTBOX_ENABLED=0` capability. When
selected, dashboard password additions/replacements and successful resets commit
the password revision, password hash, and encrypted `password_changed` intent in
one apid owner transaction. Both purpose-specific durable reset tokens and
legacy shared login/reset tokens are supported. Token consumption shares the
commit. No enabled operation falls back to an unalerted mutation after a
configuration failure, queue cap, recovery hold, or late gate withdrawal.

The dashboard retains its session authentication, purpose-bound CSRF, password
validation, proof policy, rate limiting, redirects and audit fields. A fresh
MFA step-up can authorize a change; otherwise pending explicit MFA policy or
an enrolled factor blocks it, an existing password must be verified, and a
password-free account can add one using its session. Read the password/MFA
snapshot in one statement and verify the current password outside owner locks.
The resulting opaque authorization retains no password/hash. Account and
password/MFA revisions must still match under the transaction's account lock.
Concurrent use of that authorization has at most one winner. Internal password
cleanup and signup writers are not alert producers.

Transactions lock namespace admission, account/update, any credential row, and
then shared recovery. Durable resets retain every saved address, password
revision, namespace, generation and one-shot expiry binding, including after
reset issuance is disabled. An alert must belong to the token's namespace.
Legacy tokens have no stored purpose, address, revision or generation; do not
invent retrospective bindings. Their account must currently be active with a
verified email, and their existing absolute deadline and one-shot use are
rechecked under locks. Both reset consumers run the final database expiry CAS
*after* password and alert writes, so expiry during either wait rolls back all
writes. The pure durable consumer remains available after alert flag rollback.

Post-commit session behavior stays intact: replacing a password in settings
revokes other sessions while preserving the requesting session; adding a first
password retains the existing behavior. A reset revokes old sessions before
issuing the existing new session, including its current MFA policy. Revocation
remains best-effort under its existing contract. Failure after the committed
password change does not retract its security alert.

The sealed payload contains exactly `changed_at` from database time and method
(`dashboard` or `password_reset`). No password, PHC, reset token, token hash or
reset URL enters the alert payload, rendered email or logs. Reuse the dedicated
apid current/previous credential identities under their historical verification
names. No origin is required; apid keys never reach maild or mailctl. Encryption
binds account, recipient, namespace, producer, sender, intent, template/version,
original 24-hour deadline, recovery generation and reader key.

The frozen V1 renderer and `password-changed:<intent UUID>` admission key retain
the original event across retry, restart and lost acknowledgement. Later
password/MFA changes, billing suspension or abuse holds do not rewrite history.
Address changes and account deletion cancel pending delivery. Suppressions,
budgets, leases and recovery-generation quarantine retain their existing
semantics. No recovery advisory lock crosses the HTTP handoff.

The independent child gate controls paired mutation, claims, marked transport,
maild admission and private receipt reconciliation/audit replay. Maild checks it
before reading the body and after owner waits. Enabled startup checks the new
schema constraint and all unexpired pending reader references. Workers skip
unknown keys/templates. Terminal settlement, maintenance and reconciliation
erase recipient and sealed payload. Hard erasure cascades intent; opaque audit
survives. Down refuses any remaining password-change intent or audit history.

## Consequences

Apply the additive migration before opting in. Units, examples and the
environment contract default to zero. Existing public routes and shapes remain;
no customer mail API, listener, credential enrollment or delivery qualification
is introduced. The alert gate is independent of reset-link issuance. Turning it
off restores existing future mutations and pauses pending alert handoff, while
base maintenance can expire and erase payloads. Admission receipts do not prove
SMTP custody or destination delivery. Live qualification remains separate.
