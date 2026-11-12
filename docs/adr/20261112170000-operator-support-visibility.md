# ADR-20261112170000: Operator support visibility

- **Status:** accepted (2026-11-12)
- **Date:** 2026-11-12
- **Related:** ADR-091 (operator observability backend),
  ADR-20261023214253 (operator reads need an MFA session),
  ADR-20261111130000 (native customer support),
  ADR-20261112134100 (support notifications)

## Context

Operators already had a tenant 360 (`/v1/admin/obs/tenants/{id}/360`): plan,
status, apps, orgs, key and session counts, usage and invoices. Support still
could not answer the two questions every ticket starts with:

1. **Which account is this?** Customers report an email, an app URL, a custom
   domain, a deployment id or the request id from an error. The tenant list
   only paged through every account by cursor.
2. **Why is it not working?** The 360 showed what the account owns and pays,
   not what is stopping it: suspensions, abuse holds, failed deployments with
   their error, broken custom domains, enabled previews. Nothing recorded the
   API errors the customer was shown, so "I keep getting an error" had no
   server-side trail.

The staff support inbox showed the conversation without any of this.

## Decision

### 1. Identifier lookup

`GET /v1/admin/obs/lookup?q=` (session-only operator read, like the rest of
the tenant surface) resolves `q` by every interpretation its shape allows:

| Shape | Resolved through |
|---|---|
| UUID | account id, app id, deployment id |
| email | `AccountByEmail` (emits a `pii.accessed` audit event) |
| hostname or URL | custom domain; `<slug>.<apps domain>` and `<slug>--<preview>.<apps domain>` |
| app slug | `AppBySlugIncludingDeleted` |
| request or trace id | recorded customer API errors (section 3) |

Matches carry account and app identifiers and a short detail, never an
email. At most 20 are returned. `gregalectl accounts find <identifier>` wraps
it.

### 2. Diagnosis in the tenant 360

The 360 gains a `diagnosis` section, computed from rows apid already stores:

- **restrictions:** suspended, past due (since when), deletion pending,
  account and organization abuse holds with their reason, unverified email,
  overage cap reached, and the platform release pause;
- **failed_deployments:** the last 10 failed deployments from the past 7
  days with the error code, message and hint the customer saw;
- **domain_issues:** custom domains that are unverified, failed issuance,
  drifted DNS, pending or expired certificates; healthy ones are counted;
- **enabled_flags:** platform flags decided on for the account;
- **customer_errors:** the last 24 hours of recorded API errors (section 3).

A section that cannot be read is listed in `unavailable` and left empty, so
an empty list never hides a failure. `gregalectl accounts 360` prints the
section as `key=value` lines.

Binding health is not included: there is no account-level read of binding
state today, and failed binding checks already surface as failed
deployments.

### 3. Recording the API errors customers see

`recordCustomerErrors` is the outermost account-handler layer of the
authenticated `/v1` chains (`auth`, `authLimited`, `authVerifiedLimited`).
After the handler returns it reads the problem code `api.WriteProblem`
stamped in `X-Faas-Error-Code`. It does not wrap the `ResponseWriter`, so
streaming, hijacking and HTML problem pages are unchanged, and the HTTP
status is not recorded. Operator routes (`/v1/admin/*`) and requests that
fail authentication (no account) are not recorded.

A row in `customer_api_errors` holds only identifiers: account, time, the
matched route **pattern** (never the raw URL or query string), the problem
code, the app slug path value, the edge request id and the trace id. No
request body, response body or problem detail is stored.

Recording never blocks a request. `pkg/apid/customererrors` validates the
row, spends one unit of a per-account budget (60 per minute per replica) and
hands it to a 4096-row channel; a full channel or exhausted budget drops the
row and counts it (`dropped` in the summary). A worker copies batches of up
to 256 rows every second and deletes rows older than 14 days every hour in
bounded batches. Without PostgreSQL the recorder is nil and nothing is
recorded.

The table is operational data: excluded from environment clones, deleted
with the account (`ON DELETE CASCADE`), not exported.

### 4. Account panel on staff tickets

The staff ticket page (`/dashboard/admin/support/{ticket}`) shows a
**Customer account** panel: account id, organization, plan, status, app
count and the diagnosis above. The panel loads only after the support
service has authorized the viewer (MFA-completed session and a current
roster entry, ADR-20261111130000) for that ticket. It shows no email. The
account is the ticket's personal owner, or for an organization case the
member who opened it. A failure renders "could not be loaded" and never
blocks the conversation.

Support agents therefore see diagnosis data for the customers whose cases
they can open, without being operators. The full 360, usage, invoices and
lookup stay operator-only.

## Consequences

- One additive migration (`customer_api_errors`); deploy it before apid. An
  apid N-1 replica simply does not record.
- At the per-account cap one replica writes at most 60 rows per account per
  minute; retention bounds the table to 14 days.
- The 360 response grows by a bounded section; the diagnosis adds a few
  indexed reads per request.
- Not covered: HTTP status of recorded errors, unauthenticated failures
  (bad API key), dashboard HTML form errors, binding health, and a web
  operator console for lookup and the 360 (the CLI and the support panel are
  the interfaces).
