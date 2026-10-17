# ADR-20261017094937: Durable internal MFA-disabled security alerts

## Context

Recovery-code use has a durable alert, and email-assisted disable requests have
purpose-specific durable links. Successful MFA removal needs a notification for
the account owner, regardless of whether the proof was a password, recovery code
or email confirmation. Sending after commit could lose the notification; saving
first could notify about a removal that never happened.

## Decision

Add the exact internal `FAAS_INTERNAL_MAIL_MFA_DISABLED_ALERT_OUTBOX_ENABLED=0`
gate. Enabled password, recovery-code and both durable/legacy email confirmation
paths commit factor removal and an encrypted `mfa_disabled` intent together.
Email proof consumption shares that commit. Failure, queue saturation or late
gate withdrawal leaves the factor and proof intact. The final recovery code is
allowed on removal; no separate recovery-code-used alert is generated.

The existing HTTP boundary retains authentication, CSRF and the prohibition on
using a password from a pending MFA session. Password verification runs before
owner locks; the transaction checks that the password and MFA revisions still
match the verified snapshot. Every password/factor writer already advances those
revisions. Recovery hashes are compared in constant time under the account lock;
malformed or duplicate matches fail closed. Removal requires a currently enrolled,
available account at the expected address and preserves explicit MFA policy.
No session or step-up proof is issued by removal.

Transactions take admission, account/update, any credential row and then shared
recovery fences. Durable email links retain all saved address, password, factor,
namespace, generation, cooldown and expiry bindings. Their final consume CAS runs
*after* the alert save, so expiry during a save wait rolls back every write.
Legacy envelopes are authenticated using the existing host identities before the
transaction. Their account, one-shot use and original 24-hour cooldown are
rechecked under locks and database time. A legacy request preceding the current
enrollment is refused. The legacy format has no stored expiry or factor revision;
this change does not invent bindings for old rows.

The alert contains exactly database event time and method (`password`,
`recovery_code`, `email`). It contains no password, token, TOTP secret, recovery
code or recovery HMAC. Reuse the dedicated apid current/previous credential
identities under their historical verification environment names. No origin is
needed, and no apid key reaches maild or mailctl. Authenticated encryption binds
account, recipient, namespace, producer, sender, intent, template/version,
original 24-hour deadline, recovery generation and reader key.

The frozen V1 body and `mfa-disabled:<intent UUID>` admission key preserve the
historical event across retries, restart and lost admission acknowledgement.
Later credential changes, re-enrollment, billing suspension or abuse holds do not
rewrite that event. Address changes or account deletion cancel pending delivery.
Shared suppressions, budgets, leases and generation quarantine remain in force.
No recovery advisory lock crosses the HTTP handoff.

The own child gate independently controls paired removal, claims, marked
transport, maild admission and private receipt reconciliation/audit replay.
Maild checks before reading the body and after owner waits. Enabled startup
checks the migration's alert constraint and every live pending reader reference.
Older workers skip unknown keys/templates. Terminal settlement, maintenance and
reconciliation erase recipient and sealed payload fields.

## Consequences

Apply the additive migration before enabling the capability. Existing flags,
public API shapes, email-link page gating and default-off behavior remain intact.
The new alert flag is independent of the disable-link issuance and recovery-code
alert flags. It can cover legacy inline-issued links without enabling new link
issuance. A failed enabled operation never falls back to removal without an alert.
The request's selected path remains fixed across flag changes during the call.

Disabling the alert flag restores existing removal behavior for future calls and
pauses its pending handoff. Bounded base maintenance can still expire and erase
payloads. Hard account erasure cascades intent; opaque reconciliation audit
survives. Migration Down refuses alert intents or audit history. Admission is
separate from SMTP custody and destination delivery; no forced resend, deadline
extension, operational enrollment, deployment or live qualification is added.
