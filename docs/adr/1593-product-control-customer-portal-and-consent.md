# ADR-1593 · Product Control customer identity, consent and browser recovery

- **Status:** accepted for internal implementation
- **Date:** 2026-10-15
- **Decision:** Connect the internal customer portal to Gregale Application Auth,
  require a confirmed commercial quote at new admission, and persist scoped
  browser request metadata before sending work. Keep all surfaces behind the
  existing Product Control flag and the independent Operations gates.

## Identity boundary

The latest catalog's optional `auth_app` selects an account-owned Application
Auth app with a managed PostgreSQL Auth binding. It controls the portal's realm
without migrating customers' pinned commercial revisions. apid calls only that
app's platform HTTPS hostname, with bounded bodies, timeouts and no redirects;
request data and JWT claims never select an upstream URL. Custom-domain URLs
are not used for this server-side bridge. This is a deliberate end-customer
password/session proxy, separate from the owner dashboard's admin-key flow.
No Auth admin key or user registry is fetched or retained by Product Control.

Login and refresh use the Auth app's existing GoTrue endpoints. Access and
rotating refresh credentials are stored in Secure, HttpOnly, SameSite=Strict
cookies, scoped to this product's customer route prefix and named by Auth app
identity. Tokens never enter browser storage, JSON responses, URLs or logs.
Mutation requests require an exact same-HTTPS-origin Origin header. The shared
API authentication failure limiter wraps this surface; the flag wraps it first.
This initial portal supports invited users signing in with email/password.
Hosted OAuth, magic-link and self-service organization onboarding are outside
this internal milestone.

Each request verifies its session and current memberships through
`/authz/v1/orgs`, whose SQL authorization checks the live session, user status and
memberships. An owner maps an Auth organization UUID to one platform tenant per
product; uniqueness is enforced in PostgreSQL. The browser may select only a
currently verified mapped organization. Active account and tenant checks apply.
Every member may use that organization's assigned features and read its runs.
App-scoped operation reads, events, cancellation and downloads reuse Operations'
tenant boundary after checking the product app. They grant no owner authority.

Admission rechecks the organization mapping and current Auth realm under the
Operations account/tenant locks, before duplicate lookup. This fences an owner
mapping change between the initial HTTP check and the database transaction.
Auth database verification and platform admission are separate transactions:
in-flight membership revocation has the normal request authorization race; this
ADR makes no cross-database atomicity claim. Subsequent requests check again.
An Auth outage fails closed; it never falls back to a platform owner session.

## Commercial consent

The customer's view contains feature input schemas and quote hashes. Each hash
binds account, product app, tenant, feature, assigned immutable catalog revision,
plan, currency, allowance, unit price and UTC month. Deployment identity is
excluded so an ordinary code deployment does not reprice a feature.

New Product Control starts require `expected_quote`. The admission transaction
compares it with current assigned terms after retained duplicate lookup and
before consuming allowance or saving work. Missing/stale quotes return
`409 product_quote_changed`. The portal refreshes terms and requires another
click and visible confirmation; the CLI reads `product quote` and submits an
explicit `--quote HASH`. Confirmed quote evidence is stored with the independent
receipt. It authorizes no access and is not a finalized invoice or reservation.
Existing retained retries preserve their original price and key across customer
plan changes, suspension of new starts and deployments.

## Browser contract

The portal renders guided controls for flat scalar object schemas and a JSON
fallback for other contracts. Server validation remains authoritative. Progress
and usage refresh every four seconds. Customers can request cancellation and
inspect succeeded JSON or download verified private artifacts through the same
cookie-authenticated, tenant-and-app-scoped boundary. Reconciliation is owner
work; the portal never starts a replacement for an unknown outcome.

Before POST, local storage must durably save a versioned receipt containing only
account/app/tenant/feature scope, a random request key, canonical input SHA-256,
confirmed quote, timestamps and eventual Operation ID. Storage failure prevents
POST. Web Locks serialize same-feature submissions and session refresh across
tabs. A stale tab observes a prior acknowledgement instead of replacing it.
BroadcastChannel clears other tabs' workspace memory after login/sign-out.
Raw inputs remain in memory; credentials live only in the HttpOnly cookies.

Reload/sign-in discovers matching receipts, including retired features, with a
read-only product-namespace lookup. It never sends work automatically. A pending
retry requires the original inputs, key and explicit confirmation, within a
one-day browser replay bound. Expired or malformed metadata requires review;
no new key is silently created. An accepted ID resumes retained status/history.
Sign-out leaves non-secret recovery metadata for the next authorized sign-in.
Epoch checks and aborted requests fence UI changes after workspace/identity
changes, including stale responses and file transfers.

## Validation and rollout

Portable Memory/PostgreSQL tests check unique identity mappings, missing/stale
quotes, unchanged original retry prices, no quota on rejection, scoped lookup
and mapping/realm changes at admission. HTTP tests use three customer logins and
two plans, with the real Product Control/Operations handlers and a simulated
Auth/execution boundary. Browser tests cover durable uncertain requests,
changed-price reconfirmation, reload discovery and identity fencing. These do
not qualify native execution or a real Auth deployment.

The existing exporter includes a reviewed two-plan pilot configuration and a
script for three existing customer assignments. Run its real success, failure,
cancellation, lost-response, redeploy, reconciliation and private-file trials on
an Operations-qualified native host before expanding beyond internal use. The
flag defaults off, and public OpenAPI/SDK exposure remains excluded. Payment,
invoice finalization, credits and request-monetization integration are deferred.
