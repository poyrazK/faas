# ADR-1613 · Product Control notification recovery

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Let a customer explicitly recover a personal hard-bounce pause
  after verifying a different address, with immutable receipts and recipient
  generations. Complaint holds require review and cannot use this action.

## Address evidence and authority

The sender now seals a SHA-256 digest of the normalized resolved recipient
address in a separate subscription-bound age envelope before a Resend request.
Only ciphertext is persisted with the accepted delivery and copied into its
pause. Neither the digest nor the address enters public responses, receipts or
logs. Lowercasing prevents case-only address edits from qualifying as a different
recipient. This reuses the existing host identity and recipient-grant protocol;
Application Auth runtime, issued migrations and wire protocol are unchanged.

Recovery requires the current authenticated customer, owned Auth realm, mapped
workspace, verified recipient identity, current product permissions and explicit
selection of at least one notification category. The service opens the paused
address evidence, requests fresh Auth consent, resolves that scoped capability,
compares the current verified address evidence and rechecks product access.
The customer cannot supply an address, actor, tenant, grant or evidence field.
Missing legacy evidence or an unavailable decryption identity leaves the pause
under review. A plaintext database edit or preference save is not a recovery.

Auth rotates a recipient capability when consent is renewed. Preference changes
and recovery confirmations therefore serialize per subscription **before** any
Auth renewal. Memory uses a cancellable scoped gate; PostgreSQL uses a separate
advisory transaction for that consent scope, bounded by the five-second consent-phase
budget. A waiting contender releases its connection and waits 25 ms before
trying again. The holder retains one connection while Auth runs; its short
repository writes use the existing pool. Capacity or Auth outages can fail the
request without bypassing verification. No global feedback lock or delivery row
lock is held during network calls. Callbacks remain able to establish a new hold.

## Recovery and replay contract

`POST /v1/product-control/customer/{slug}/email-recovery` accepts exactly a stable
UUID `id`, `expected_version`, `expected_generation`, `work_outcomes` and
`help_replies`. The explicit action itself confirms renewed personal consent.
An unchanged retry first looks up the scoped immutable receipt, before issuing
another grant. A reused UUID with changed choices or expectations conflicts.
Ordinary saves keep the existing pause and recipient generation.

The final repository transaction rechecks the observed preference version,
generation, hard-bounce reason and retained evidence. It shares the short feedback
advisory lock and existing preference scope lock, coordinating callbacks, normal
saves and recovery. The same transaction records the confirmation, installs the
fresh sealed grant, clears only the hard-bounce pause, increments both preference
version and recipient generation, resets category start times to confirmation
time and resets the scan cursor. Receipt timestamps use microsecond precision in
both stores so first responses and durable replay agree exactly.

Recovery sends no email, reopens no delivery and changes no attempt budget or
original deadline. Only future eligible source updates can enqueue; cancelled
messages remain cancelled. Leased messages still face the final version/access
check. Already in-flight provider calls may complete and retain their original
generation. Concurrent identical confirmations converge on one receipt; stale
requests and a complaint arriving during verification cannot clear a newer hold.

Deliveries retain their enqueue generation. A hard bounce from an earlier
recovered generation updates that message's history but cannot pause the
replacement subscription or cancel its queue. A bounce in the current generation
pauses it normally. A complaint from **any** retained generation establishes or
escalates a review hold. Replaying an earlier recovery receipt acknowledges the
old confirmation without clearing that hold.

## Observation and release boundary

Customers read their own recovery history at `GET .../email-recoveries`, while
owners use `GET /v1/apps/{slug}/product/email-recoveries`, the dashboard recovery
history and `gregale product email recoveries`. Owner reads retain account/app
ownership, read scope and MFA policy; browser reads require an owner session.
There is no owner or CLI resume action. Receipts expose opaque subscription and
customer references, selected categories, versions, generations and confirmation
time, never addresses, Auth identities, capabilities or message content.

History has 25 records per page, newest first, with time/UUID ordering and cursors
bound to the resource, owner scope and optional personal recipient. Retention is
30 days and capacity is 50 confirmations per subscription within that window.
Capacity errors preserve the hold. The receipt is historical evidence, not a
promise that the subscription remains active after a later complaint or bounce.
The portal preserves an uncertain request in memory for an explicit unchanged
retry, clears it on sign-out/workspace change and never automatically resubmits.

Append-only migration `20261017170000000_product_control_email_recovery.sql`
adds encrypted evidence, checked generations and recovery receipts. Existing
issued migrations are unchanged. All routes and assets remain under the existing
default-off Product Control gate. Email still defaults disabled and real sending
keeps its independent switch. Local signed provider fixtures and disposable
PostgreSQL exercise the implementation without credentials or external mail;
live Auth, replacement-address verification and provider policy qualification
remain separate release evidence.

See [ADR-1612](1612-product-control-email-provider-feedback.md) and the
[recovery runbook](../ops/product-control.md#recovering-notifications-after-a-hard-bounce).
