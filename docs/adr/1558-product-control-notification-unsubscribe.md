# ADR-1558 · Product Control notification unsubscribe

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Include a narrowly scoped unsubscribe capability in each new
  notification email. Opening a link only shows confirmation; explicit browser
  or mailbox POSTs stop that person's notifications in the original workspace.

## Capability and confirmation

Before sending, the worker creates a random 32-byte capability for that delivery.
The outbox persists its SHA-256 lookup hash and a separately delivery-bound age
envelope, using the existing host identity. Neither an Auth session nor a scoped
recipient grant is exposed. Preparation keeps the original capability across
automatic retries and owner retries, preserving the provider's idempotent body.
Messages cannot send without a usable unsubscribe link. Existing delivered mail
does not gain a capability retroactively.

Text and HTML links use `/v1/product-control/email/unsubscribe#token=...`.
Fragments stay outside server access logs and referrers. GET and HEAD serve a
static confirmation page without reading a subscription or changing preferences.
The page removes query/fragment credentials from browser history, keeps the
capability only in memory, and submits a credential-free JSON POST after an
explicit click. It loads only local assets, with no-referrer, no-store, restrictive
CSP, no framing and no indexing. It never stores a capability in local storage.
An uncertain response permits only a manual repeat of the same capability.
Opening another fragment link in the same tab resets the confirmation without a
POST; a delayed response for an older link cannot replace the newer screen.

The message also carries `List-Unsubscribe` and `List-Unsubscribe-Post` headers.
The HTTPS header URL places the capability in its query so mailbox providers can
POST `List-Unsubscribe=One-Click` with URL-encoded or multipart form data, without
cookies or an Origin header. GET of this URL still requires human confirmation.
The browser confirmation uses JSON at the fixed endpoint; queries, unknown form
fields, duplicate form fields, files and mixed selectors are rejected. Body and
query parsing are bounded to 1,024 bytes. When present, Origin must identify this
HTTPS host; cross-origin browser requests are rejected. Public routes use the
existing IP limiter and all-method Product Control feature gate.

Both URLs are bearer capabilities that authorize only opt-out. They cannot read
workspace data, change an address, choose a different person or tenant, resume
mail, or clear a delivery hold. Do not log bodies, outbound unsubscribe headers or
raw query strings at a proxy; the platform limiter logs only the fixed path. Real
mailbox behavior, tracking settings and DKIM coverage of the unsubscribe headers
remain part of provider qualification.

## Consent, queue and replay

Lookup joins the retained delivery to its exact subscription. A first opt-out
requires its original preference version and recipient generation to remain
current. Saving preferences, renewing consent or recovering a recipient prevents
an older link from cancelling that later decision. The capability expires when
its original delivery leaves the existing 30-day window; it does not depend on
Auth or provider availability, the recipient grant's expiry or decryption keys.

Unsubscribe serializes with preference changes and recovery before entering the
short feedback/preference transaction. The transaction records one immutable
receipt per subscription and original consent version, turns both notification
categories off, erases the sealed recipient grant and expiry, resets the scan
cursor, and cancels queued messages with reason `unsubscribed`. It advances the
preference version. Stopping notifications also works at the version ceiling;
that case keeps the ceiling version and clears consent normally. Bounce and
complaint holds, address evidence and recipient generation are preserved.

Different email links for the same consent converge on the same receipt.
Unchanged repeats read that receipt without another mutation. The capability
response contains only its confirmation ID/time and a separately checked
`current` boolean. A later opt-in makes a historical replay report `current=false`
and leaves all newer state untouched. A stale link with no retained receipt
returns a generic expired/replaced response without revealing recipient details.

Leased jobs still face the final durable readiness check directly before sending.
An external request already in flight may complete and retains its provider
receipt and subsequent feedback. Unsubscribe never recalls a sent message,
reopens a cancelled job, changes an attempt budget, or sends a confirmation email.
Re-enabling requires the current authenticated customer, verified recipient and
explicit new consent; only future eligible source updates can enqueue.

## History, storage and release boundary

Customers read their personal `email-opt-outs` history in the portal. Owners read
account/app/optional-tenant scoped receipts in the console, the private API and
`gregale product email opt-outs`. Owner scope, session and MFA policy remain in
place. There is no owner resume action. History exposes opaque references,
versions, generations and confirmation time, without addresses, Auth actors,
capabilities, ciphertext or message content. It identifies historical decisions;
current preferences remain a separate observation.

Pages contain at most 25 receipts, ordered by time and UUID, with cursors bound
to the resource and account/app/tenant/personal-recipient scope. Receipts cascade
with their source deliveries. The existing 1,000-delivery subscriber bound and
30-day source window bound history without imposing a capacity error on opt-out.
Previously issued migrations, Auth templates and SDKs remain unchanged; an
append-only migration adds checked capability fields and receipt storage.

All routes remain behind default-off `FAAS_PRODUCT_CONTROL_ENABLED`. Email mode
still defaults to disabled and real Resend sending retains its independent flag.
Opt-out works with email mode disabled while Product Control itself is enabled.
Implementation and verification use fictional identities, a disposable local
database and loopback delivery fixtures; no deployment or real credentials are
needed. See the [operator runbook](../ops/product-control.md#unsubscribing-from-notification-email).
