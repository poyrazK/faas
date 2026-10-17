# ADR-20261017102802: Durable internal MFA-enabled security alerts

## Context

MFA removal, recovery-code use and password changes have durable security
notifications. A newly confirmed authenticator also needs an alert that cannot
be lost between account mutation and mail handoff. The existing confirmation
reads and verifies the factor before a separate enrollment write. A concurrent
setup can replace that factor between those operations, and a setup request
started earlier can overwrite a factor after it was confirmed.

## Decision

Add the exact internal default-off
`FAAS_INTERNAL_MAIL_MFA_ENABLED_ALERT_OUTBOX_ENABLED=0` capability. Enabled
confirmation verifies the account's locked factor and commits its activation
timestamp, policy clear, revision and encrypted `mfa_enabled` intent together.
A failed write, saturated outbox, recovery hold or late gate withdrawal leaves
pending setup, policy and recovery codes intact. Once selected, this path cannot
fall back to confirmation without an alert.

The existing HTTP boundary keeps authentication, purpose-bound CSRF, TOTP rate
limits, response shapes and audit kinds. The domain locks namespace admission,
account/update and shared recovery. It reads only the sealed factor and account
metadata needed for confirmation, without reading password or recovery hashes.
The existing host current/previous identities decrypt the locked factor; the
same six-digit SHA-1, 30-second, ±1-step TOTP check runs after owner waits and
again after the alert save. A code expiring during a blocked save rolls back
activation and alert. Host keys and plaintext secrets never enter intent.

Enabled setup uses a conditional pending-only write under the same owner order,
so an older setup request cannot replace a confirmed factor or its recovery
codes. It preserves the existing once-only setup response and deliberate
replacement of unconfirmed setup. Preparation emits no alert. Setup and new
confirmation refuse held recovery generations and unavailable/changed accounts.
Other state-store callers and default-off behavior keep their existing contracts.

Repeated confirmation still verifies possession of the current factor before
returning success and allowing the existing session reissue. It does not rewrite
the original activation timestamp or revision, duplicate an alert, or backfill
an enrollment that predates opt-in. A concurrent first confirmation therefore
has one activation event. After an explicit disable and new setup, a newly
confirmed factor creates its own event. Later policy changes are not cleared
by replaying confirmation of an already-enrolled factor.

The existing post-commit cookie reissue clears `mfa_pending` for the same SID
without granting a step-up stamp or creating a session row. Cookie reissue
failure remains non-fatal under the existing wire contract; committed enrollment
and its alert survive it. Audit aliases remain `account.mfa_enrolled` and
`account.mfa_confirmed` with method `totp`.

The sealed payload contains exactly database activation time, `enabled_at`.
It contains no TOTP secret, code, QR URL, recovery code/hash, password or token.
Dedicated apid current/previous credential readers encrypt/decrypt alert payloads
under their historical verification environment names. No origin is required;
these readers remain separate from host MFA identities and never reach maild
or mailctl. Encryption binds account, recipient, namespace, producer, sender,
intent, template/version, original 24-hour deadline, recovery generation and key.

The frozen V1 body and `mfa-enabled:<intent UUID>` admission key retain the
original activation across retries, restart and lost acknowledgement. Later
factor removal/replacement, password changes, suspension and abuse holds do not
rewrite that event. Address changes or account deletion cancel pending handoff.
Existing suppressions, budgets, leases and recovery-generation quarantine apply.
No recovery advisory lock crosses the HTTP handoff.

Independent child gates cover paired state changes, claims, marked transport,
maild admission and private acknowledgement/audit replay. Maild checks before
reading the body and after owner waits. Enabled startup checks the migrated
constraint and every unexpired pending reader reference. Older workers skip
unknown keys/templates. Terminal settlement, maintenance and reconciliation
erase recipient and sealed payload. Account erasure cascades intent; opaque
audit survives. Down refuses remaining activation intent or audit history.

## Consequences

Apply the additive migration before opting in. Units, examples and the
environment contract default to zero. Flag rollback restores previous setup and
confirmation for future calls and pauses pending alerts; base maintenance can
still expire/erase them. No new public API, listener, enrollment credential,
resend override, deployment or live qualification is introduced. Admission
receipts remain separate from SMTP custody and destination delivery evidence.
