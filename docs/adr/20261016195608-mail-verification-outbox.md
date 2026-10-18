# Durable verification credentials in the internal producer outbox

- Status: accepted for an internal experiment; activation remains disabled.
- Date: 2026-10-16
- Builds on: [ADR-1738 producer outbox](1738-mail-producer-outbox.md), [reconciliation](20261016192204-mail-outbox-reconciliation.md), ADR-982 apid ownership.

## Context

Verification-token issuance and email sending previously committed separately.
After account signup, a process failure could leave a live hash with no durable
notification. Replaying a handoff must preserve the original link and expiry.
Verification links contain credentials, so the non-secret deletion-intent
projection alone cannot represent them safely.

## Decision

Add the exact default-off `FAAS_INTERNAL_MAIL_VERIFICATION_OUTBOX_ENABLED` gate
alongside the existing producer outbox and its internal transport prerequisites.
apid atomically stores the hash/address-bound authentication token and an intent
with age-encrypted raw token bytes. The token remains 32 bytes, single-use and
valid for 24 hours. The versioned renderer, explicitly configured HTTPS origin,
recipient, expiry and `verify-email:<intent UUID>` key remain immutable across
worker retries. New user requests retain the existing fresh-link behavior.
Public signup/error shapes, customer routes and SDKs are unchanged. No listener,
automatic activation or live qualification is introduced.

A dedicated apid-only X25519 identity seals credentials; current and one previous
reader support bounded overlap. The authenticated envelope binds every immutable
routing, token and renderer input, including recovery generation. Enabled startup
refuses missing dependencies for this producer's unexpired pending payloads.
Workers claim only compatible origin/reader keys so an older worker cannot fail
a newer writer's payload. Neither maild nor mailctl receives these private keys.
Terminal states and account erasure clear producer credential material.

Dispatch locks account, intent and live token across the bounded HTTP admission.
PostgreSQL token consumption acquires account before token too, avoiding inversion
with dispatch and account deletion. Before handoff, consumed/missing/expired
tokens, verified/inactive accounts, changed addresses and suppression block the
credential. New token/address binding remains enforced after maild handoff and
after flag rollback. Legacy tokens retain their existing semantics. No maild or
relay custody is retracted after acceptance.

maild independently gates the verification-template header and validates the
saved generation inside its admission transaction. Existing generation, policy,
quota and custody fences remain. Private receipt lookup derives the template's
saved key. Local apid reconciliation requires the extra gate, records template
in its opaque audit and atomically erases remaining ciphertext. It never resends
mail, resets attempts, extends a credential deadline or changes generation.

The additive migration expands the owner outbox and binds new tokens to an
address. It refuses rollback while any verification intent, address binding or
verification audit remains. Configuration with the new gate off reads no token,
origin or key and retains the existing inline mail path. Previously saved
deadlines and address bindings survive rollback of the feature gate.

## Validation

Disposable PostgreSQL and the real protected maild handler exercise a separately
disabled template, accepted response loss and producer restart, leaving one
admission and one fake SMTP handoff. Other tests cover atomic rollback, public
signup behavior, consumption/handoff races in both lock orders, ciphertext
binding and erasure, canonical origins, key overlap, recovery quarantine,
private authorization and independent late flag revocation. Validation sends no
external messages and changes no deployed settings.
