# ADR-1595 · Internal Product Control owner console

- **Status:** accepted for internal implementation
- **Date:** 2026-10-15
- **Decision:** Integrate Product Control with the existing server-rendered owner
  dashboard, behind the existing default-off flag, using the execution and billing
  safeguards in ADRs 1591–1594.

## Customer management

The app console shows assigned customers, an existing platform tenant selector,
pinned feature grants, UTC-month usage and remaining starts, binding availability,
recent admissions, statements, credits and recorded invoice references. Binding
inspection is independent of entitlement, so suspension is not reported as a
broken deployment. Latest and assigned catalog revisions are identified separately.

Plan changes first compare current and proposed prices, access and start caps.
Existing Auth organization mappings are preserved by the store. First assignments
may provide an organization, subject to existing uniqueness constraints. New terms
apply immediately to future admissions; accepted work keeps its original price
and monthly usage never resets. Scheduling and subscription proration are later work.

The preview hash binds owner, app, full current assignment, current/proposed
catalogs, latest head and UTC month. Confirmation recomputes it under the account
→ shared app → tenant lock order already used by admission and billing. Intervening
assignment, identity, catalog or month changes invalidate a preview before mutation.

Each confirmation has a stable UUID. Assignment and immutable before/after receipt
commit atomically. Same-request retries recover the receipt before inspecting
mutable assignments, so an older retry cannot undo a later change. Reused IDs
with changed fields or scope conflict and roll back. Receipts follow parent deletion
lifecycle. The existing API/CLI assignment replacement contract is unchanged.

## Attention and evidence

The attention queue groups independent receipts by customer and admission month.
Closed months without statements remain queued, including unresolved periods.
Current-month entries require unresolved evidence and a missing execution row,
reconciliation state, or legacy terminal row without durable outcome proof.
Ordinary accepted/running work is excluded from the current-month queue. Missing
evidence never establishes failure. Binding problems are listed separately.

Recent runs use admission-time/UUID keyset pagination and include retained receipts
after result expiry or feature retirement. Detail pages reuse the existing safe
explanation and link to Operations for execution recovery. Input/result bodies,
storage locations and runtime credentials are excluded. Attention/run cursors bind
the query scope; all lists are explicitly paginated at 100 entries.

## Browser and billing boundaries

Routes use live owner dashboard sessions, account status and MFA checks. Writes
also require verified email. API keys and customer portal cookies do not authenticate
the console. The feature gate precedes authentication, covers unsupported console
paths/methods, hides navigation, and marks responses `no-store`.

Forms have centrally bounded bodies, strict fields and existing named CSRF tokens.
Review tokens bind owner/app. Confirmation tokens additionally bind the canonical
action payload, including customer, month, review hash, stable IDs, money, reason
and invoice reference. Hidden-field tampering fails closed. Cookies are Secure,
HttpOnly, SameSite=Lax and scoped to the app console. A newer review of the same
action may replace its sidecar cookie; an older tab then reviews again.

Each financial change has a review and explicit confirmation screen. Existing
transactions recheck closed-month, unresolved-proof, stale-review, credit-cap and
invoice-reference rules. Credit UUIDs survive confirmation retries. Recorded
handoffs are readable after uncertain responses. No external invoicing or payment
request is performed. Audit events omit reason text. Money remains integer
millicents and integer-only formatted amounts; partial document pages claim no
net total.

## Validation and rollout

Memory/PostgreSQL contracts verify stale previews, identity preservation,
concurrent confirmations, original prices, durable recovery after a later change,
credit projections, result-independent receipts and scoped keyset pagination.
HTTP tests verify default-off routes/navigation, owner sessions, CSRF and payload
tampering, customer/account isolation, plan previews, waivers, statements and
invoice handoff recovery. Existing admission, customer portal and billing suites
remain required.

Customer self-service plan changes, an external invoicing integration, and the
separately qualified native export/Auth/file/reconciliation pilot remain later work.
