# ADR-20261017084607: Durable internal MFA recovery alerts

## Context

The existing `/v1/account/mfa/recover` flow consumes a recovery code, reissues
the current session, then sends an alert inline. A crash or transport failure
after consumption loses the alert. The producer outbox already provides bounded
handoff, encrypted payloads, private template admission and recovery quarantine.

## Decision

Add the independent exact `FAAS_INTERNAL_MAIL_MFA_RECOVERY_ALERT_OUTBOX_ENABLED=0`
gate in apid and maild. Enabled recovery atomically consumes a non-final recovery
code and saves an encrypted `mfa_recovery_used` intent. Disabled issuance retains
the existing inline recovery flow. This is internal only and adds no public mail
API, listener, entitlement or recovery-code format.

The owner transaction takes namespace admission, account/update and shared
recovery fences in that order. It rechecks address, enrollment, sign-in account
policy and constant-time recovery-hash matching under the account lock. The last
remaining code is refused without mutation, including when concurrent requests
use different codes. A consumed-code replay cannot create another intent. Only
the existing account code array is updated; no recovery code, recovery hash,
HMAC key or sealed/plaintext TOTP secret enters notification intent.

The sealed payload has exactly two fields: the database event time and the
remaining-code count after consumption. Dedicated apid current/previous readers
are reused through their existing historical verification identity names; no
origin or new operational key is needed. Use separate `sealed_security_alert`
and `security_alert_key_id` columns with pending/terminal shape constraints.
Authenticated encryption binds intent, account, namespace, producer, sender,
recipient, template/version, deadline, recovery generation and reader key.

The frozen V1 renderer preserves the existing alert wording. Retries retain the
event time, count, body, `mfa-recovery-alert:<intent UUID>` admission key and
original 24-hour database deadline. Credential changes or MFA disable/re-enroll
after the event do not rewrite its history. Changed addresses, account deletion,
expiry and suppressions stop delivery. Historical alerts remain deliverable to
the same address through billing suspension or a later abuse hold.

The child gate independently controls issuance, claims, transport marking,
maild admission and reconciliation/audit replay. Maild checks it before reading
the body and again after owner lock waits. Workers skip unknown reader keys;
enabled startup requires readers for every unexpired pending alert. Terminal
settlement, bounded expiry maintenance and reconciliation erase event ciphertext,
reader key and recipient. No recovery advisory lock is retained across HTTP.

Maild downtime cannot fail a completed recovery because delivery happens later.
If the paired intent cannot be persisted, the code stays unused and no cleared
MFA session is issued; there is no inline fallback. A later session reissue
failure leaves the committed code-use alert durable. Existing CSRF checks,
pending-session clearance, session-ID reuse, enrollment and step-up rules remain
in the current handler. MFA disable and email-assisted disable use their existing
flows; this flag covers the existing recovery alert.

Recovery holds stop opted-in paired operations without consuming codes. Saved
intent generations remain quarantined after a later recovery generation, even
after global release. Disabling this child flag pauses its delivery while bounded
base maintenance can still expire and erase pending payloads; it restores inline
issuance for subsequent recovery requests.

## Consequences

Apply the additive migration before starting the new binary. Base outbox
readiness checks the new event columns and existing MFA account dependencies.
Shared pending limits, producer budgets, suppressions and delivery controls apply;
queue saturation refuses the paired transaction with the code still available.
Migration rollback refuses any recovery-alert intent or reconciliation history.
Account erasure cascades intent while opaque reconciliation audit remains.

Maild admission is separate from final delivery. No forced replay, deadline
extension, operational enrollment, DNS change, deployment or live qualification
is introduced. All new defaults remain off.
