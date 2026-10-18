# ADR-20261017133739: Durable internal paid-tier quota warnings

## Context

Meterd emits a warning when paid monthly usage reaches the included GB-h quota,
then deduplicates by UTC day. The legacy daily stamp precedes inline email, so a
crash or send failure can consume the day's warning. Retries must retain the
original usage snapshot while account, plan or usage-period changes invalidate
stale mail. Live delivery qualification is separate work.

## Decision

Add exact independent default-off
`FAAS_INTERNAL_MAIL_QUOTA_WARNING_OUTBOX_ENABLED=0` across apid, meterd, maild and
private reconciliation. The selected path delegates issuance through apid's
existing loopback operator listener. It adds no listener, customer API permission
or SDK method. Requests contain only the account path, expected UTC day, expected
paid plan and request UUID. Callers cannot supply usage, quota, recipient or body.
The dedicated `FAAS_INTERNAL_MAIL_QUOTA_WARNING_TOKEN_PATH` differs from producer,
Safe Deploy and enabled suspension/non-payment deletion credentials. Maild does
not receive this token or apid's payload readers. Disabled setup reads only its
own exact flag. Enabled meterd startup requires the parent mail gates and an
authenticated readiness probe; selected failures have no legacy mail fallback.
The bounded five-second client accepts only plain HTTP literal loopback origins
with an explicit port, disables proxies and refuses redirects.

Apid locks namespace admission and the current account, then reads the database
UTC day. It verifies a current Hobby, Pro or Scale plan and a non-deleting active,
past-due or suspended account. It sums authoritative `usage_minutes.mb_seconds`
for the same UTC calendar-month range used by meterd. The existing six-decimal
GB-h rounding and canonical included quota determine eligibility; pricing,
billing writes and free-tier parking rules do not change.

The daily stamp, an encrypted `quota_warning` intent and a per-account/day
business receipt commit together. Admission capacity, cancellation, stamp,
intent, receipt, recovery hold or final gate failures roll back all writes.
Concurrent ticks reuse one intent. Legacy stamps for today or a future date
receive no backfill. Receipts retain only the original plan, usage, included
quota, day and observation time needed for notification recovery, never an
address, body or credential.

A separate database-maintained `accounts.quota_mail_revision` advances for plan,
address, status, overdue anchor, deletion request or daily stamp changes,
including changes while issuance is off. Explicit counter writes cannot rewind
it; unrelated password and MFA changes do not invalidate warnings. Existing
billing-mail revision semantics remain separate. The intent records the saved
plan, UTC day and post-stamp revision. AAD binds those values, the original
snapshot, account, sender, recipient, template/version, intent, namespace,
producer, recovery generation, reader key and expiry.

The frozen V1 text and HTML alternatives use the saved usage and plan. An
optional validated `FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL` is encrypted with the
snapshot, preserving the original RFC 8058 pair across retries. Stable identity
is `quota-warning:<intent UUID>`. Expiry is the earlier of observation plus
24 hours or UTC day end, so no warning survives into another usage day/month.
Current/previous dedicated payload readers are held only by apid under the
historical verification identity configuration names.

Apid checks the current account, day, plan, stamp and exact quota revision before
handoff. Maild repeats metadata checks under an account share lock before
committing SMTP submission start. Address/status/plan/stamp cycles, deletion,
erasure and UTC rollover that commit first cancel stale notices, including
already-admitted mail. Maild reads only leaf sqlc metadata and does not write
billing or producer state. Changes after submission start cannot recall SMTP
custody; submitted or uncertain mail is never automatically resent. Shared
suppression, budgets, retries and recovery quarantine apply.

A five-minute follow-up lease permits recovery of meterd's existing quota
notification after lost responses. The same request retrieves its live lease;
a later tick can reclaim an expired lease only while the original day, stamp,
plan, revision and recovery generation remain current. Notifications use saved
values and observation time. Exact account/intent/lease completion is idempotent
and retains opaque proof. Individual notification failures remain best effort;
crash recovery can attempt notifications more than once. The selected path sends
no inline mail and calls no scheduler. A command failure still permits existing
free-stop restoration after a paid upgrade.

## Consequences

Apply the additive migration before opting in. Units and examples retain zero.
Own flags independently gate issuance, claims, transport, admission, delivery and
private reconciliation/audit. Enabled readiness checks schema, lifecycle trigger
and pending reader coverage. Rollback pauses pending durable mail and follow-up;
base maintenance can expire and erase payloads. Legacy warnings for later days
resume with the flag off. A saved stamp still deduplicates today's legacy path,
so review incomplete receipts before rollback and re-enable during the original
day to recover eligible follow-up. Old days cannot be replayed or backdated.

Erasure cascades account intents and receipts. Terminal settlement erases
recipient and ciphertext; opaque reconciliation audit survives. Down refuses
used intent, receipt or reconciliation history. Subscription-ended confirmation
remains separate work. This change does not activate, deploy, enroll credentials
or send real email.
