# customer-platform

A Node.js + PostgreSQL starter for serving multiple customers on Gregale.
It includes tenant-scoped document CRUD and local operator tools for onboarding,
credential issuance and rotation, usage, suspension, and resumption. Requires
Node.js 22+ and a Gregale Hobby or higher account for tenant ingress. A remote
database on the conventional TCP port 5432 requires Pro/Scale egress configuration.

## Deploy

```sh
gregale init --template customer-platform --path customer-platform
cd customer-platform
```

Use an existing Gregale managed PostgreSQL database, or create one with
`gregale postgres create customer-db --region <region>`. Reserve the app and
attach separate runtime and migration bindings:

```sh
gregale deploy --create-only --template customer-platform --name <slug>
gregale postgres attach customer-db <slug> --access read_write --env DATABASE_URL
gregale postgres attach customer-db <slug> --access migration --env MIGRATION_DATABASE_URL
# Pro/Scale: permit PostgreSQL on TCP 5432 when required by your egress policy.
gregale app <slug> egress-ports add 5432
gregale deploy --name <slug> --platform-tenant-required --no-require-authn
```

Template deployment enables `platform_tenant_required` from app creation.
The Procfile release command installs the tenant-scoped schema through
`MIGRATION_DATABASE_URL` before activation. The managed migration binding reaches
only the release task; serving processes use `DATABASE_URL` without DDL access,
SUPERUSER, or BYPASSRLS. Migrations take an advisory lock, with a 30-second lock
timeout and a 120-second statement timeout. A failure keeps the previous app
serving; committed schema changes survive an application rollback.

`--no-require-authn` selects the open account-auth mode; verified customer identity
is still required by the independent tenant policy. If your existing app has
another account-auth policy, configure it before onboarding. Use `/healthz` as
the health path; `/` also provides a database liveness response for rollout probes.
Both contain no customer data. Configure network access and TLS certificate
verification for your PostgreSQL host and port.

For external PostgreSQL, keep the schema-owner connection on your operator
machine and run `MIGRATION_DATABASE_URL=... npm run migrate` locally. Remove the
`release:` line from the Procfile, grant the runtime login data access to
`public.customer_documents`, and supply only `DATABASE_URL` in a 0600 secrets
file outside the source directory:

```sh
# File contents: DATABASE_URL=postgres://runtime:password@host:5432/database?sslmode=require
chmod 600 ../customer-platform.secrets
gregale deploy --name <slug> --platform-tenant-required --no-require-authn \
  --secrets-file ../customer-platform.secrets
```

The runtime login must have neither SUPERUSER nor BYPASSRLS. An ordinary secret
named `MIGRATION_DATABASE_URL` lacks a managed binding's delivery restriction.
After app creation, `gregale secrets set --app <slug> DATABASE_URL=...` updates
an external connection. Start a new deployment after changing that credential;
this starter reads it at process startup.

## Onboard and issue a customer key

Run `tools/customer.js` on your operator machine. It uses the existing Gregale
REST API. Set `FAAS_TOKEN` to your account-owner credential using your secret
manager or CLI environment; optionally set `FAAS_API` (default
`https://api.gregale.dev`). **Never put the owner token in the app's secrets.**
Owner API scopes, MFA requirements, and quotas still apply.

```sh
node tools/customer.js onboard <slug> customer-42 "Customer 42"
```

The receipt contains `tenant_id` and `consumers[0].id`. Keep the external reference
stable (for example, your billing system's customer ID). Repeating identical
onboarding returns the same identities; it never silently resumes a suspended
customer.

```sh
# Create this private directory outside the source checkout.
mkdir -p ../customer-credentials
chmod 700 ../customer-credentials
node tools/customer.js issue <tenant-id> <consumer-id> customer-42-v1 \
  ../customer-credentials/customer-42-v1.json
```

The tool generates a `ck_` key, exclusively creates a mode-0600 journal, and
flushes it to disk **before** submitting the prefix and SHA-256 digest to Gregale.
The API receipt printed to stdout contains metadata and key IDs, never plaintext.
The journal's `plaintext` field is the customer's bearer key. Transfer it through
your customer secret-delivery channel. Store journals securely outside source
archives, image build contexts, and version control; Gregale cannot recover keys.
The Dockerfile copies only `app/` into the guest, excluding the owner tools.

If the API response is lost or the request fails, replay the same journal:

```sh
node tools/customer.js retry ../customer-credentials/customer-42-v1.json
```

Retry reuses the same credential material and returns the same key ID. An issue
command refuses an existing journal path. A journal is bound to the API origin;
changing credentials to another Gregale account will fail ownership checks.

## Customer API

Send the customer's key as `Authorization: Bearer <customer-key>` to
`https://<slug>.gregale.dev`:

| Method | Path | Body / result |
| --- | --- | --- |
| POST | `/documents` | `{"title":"Welcome","content":"Private customer data"}`; returns a document ID |
| GET | `/documents` | Latest 100 documents for this customer |
| GET | `/documents/<id>` | A document for this customer |
| PUT | `/documents/<id>` | Full replacement of title and content |
| DELETE | `/documents/<id>` | Deletes a document for this customer |

Foreign and missing IDs both return 404. Request bodies cannot select a tenant;
query parameters are rejected. Responses use `Cache-Control: no-store`. The
sample operator tools issue `write` scope, which allows all app methods; use
Gregale's credential API to issue narrower `read` keys when appropriate.

## Durable workflow boundary

This starter requires a verified customer identity on every app request.
Manual workflow runs can use the authenticated tenant's identity through
`POST /v1/platform-tenant-self/apps/{slug}/workflows/{name}/runs`. Gregale
persists that tenant ID and checks it again before dispatching each step. Tenants
can inspect, cancel, and safely resume their own runs through the
`/v1/platform-tenant-self/workflows/runs/{id}` endpoints. They can also list
callback handles, complete callback waits, and inject events through the
tenant-self continuation endpoints, which verify the token's tenant identity
and the active tenant-to-app link when recording each continuation.

Use `platform_tenant:invocations:read` to list callback handles and
`platform_tenant:invocations:manage` to complete a callback or send an event:

- `GET /v1/platform-tenant-self/workflows/runs/{id}/callbacks`
- `POST /v1/platform-tenant-self/workflows/runs/{id}/callbacks/{callback_id}`
- `POST /v1/platform-tenant-self/workflows/runs/{id}/events` with an
  `event_name` and optional JSON `payload` (retries can reuse `Idempotency-Key`)

Tenant-bound workflows support managed-operation steps with effects delivered
to a receiver owned by that same tenant. They can also call an existing
customer-managed outbound integration explicitly bound to the app. The run's
tenant identity is included in private authorization and rechecked against the
active tenant-to-app link before dispatch and by outboundd. Integration
credentials and route permissions remain app-owned and shared across tenants;
per-tenant credentials are not part of this workflow interface. A published
schedule trigger starts one run per active tenant link with independent overlap
and duplicate-minute state. By default, the app owner controls the cadence,
timezone, overlap behavior, and input. A schedule trigger may opt in to
tenant-specific cadence controls with `tenant_configurable: true`; the app owner
still controls the workflow definition and input, and the app-wide concurrency
limit is shared across tenants. Grant a tenant token
`platform_tenant:automations:read` to list opted-in schedules and
`platform_tenant:automations:manage` to change that tenant's schedule:

```http
GET /v1/platform-tenant-self/apps/{slug}/workflows/schedules
Authorization: Bearer <tenant-token>
```

Use the returned `version` as `expected_version`. Zero creates the tenant's
first override; subsequent updates must send the latest version or receive a
409 conflict. Omitted `timezone` and `overlap` use the published defaults, and
`enabled: false` pauses only this tenant's future runs. Runs already admitted
continue with their captured workflow definition:

```http
PUT /v1/platform-tenant-self/apps/{slug}/workflows/schedules/nightly
Authorization: Bearer <tenant-token>
Content-Type: application/json

{"expected_version":0,"schedule":"0 6 * * 1-5","timezone":"Europe/Istanbul","overlap":"skip"}
```

Published workflows with an `event` trigger can also start automatically from
events published by the authenticated tenant. Grant the tenant token
`platform_tenant:events:manage` to publish and `platform_tenant:events:read` to
inspect receipts:

```http
POST /v1/platform-tenant-self/apps/{slug}/events:publish
Authorization: Bearer <tenant-token>
Content-Type: application/json

{"id":"invoice-42","source":"billing.stripe","type":"invoice.paid","data":{"amount":125}}
```

The response includes a platform event `id`, the original `client_event_id`,
and a tenant-authenticated `receipt_url`. Repeating the same caller id, source,
and content is safe. The platform derives tenant identity from the bearer token,
routes only to that tenant's linked app workflows, and rechecks the active link
before accepting the event and admitting each workflow run. The receipt reports
captured workflows and their run IDs without exposing account-operator recovery
actions.

Account-scoped apps can use managed workflow transactions and app-owned effects.
The [transaction guide](https://github.com/poyrazK/faas/blob/main/docs/operation-transactions.md)
and [effect delivery guide](https://github.com/poyrazK/faas/blob/main/docs/managed-operation-effects.md)
show the handler, receipt schema, receiver setup, and recovery contract.

## Rotate, inspect usage, and suspend

```sh
node tools/customer.js rotate <tenant-id> <consumer-id> customer-42-v2 <old-key-id> \
  ../customer-credentials/customer-42-v2.json
node tools/customer.js usage <tenant-id> 2026-09-01T00:00:00Z 2026-10-01T00:00:00Z
node tools/customer.js suspend <tenant-id>
node tools/customer.js resume <tenant-id>
```

Rotation atomically creates the new key and revokes the selected old key. Prepare
the customer for the switch before rotating; use separate issue and revoke
operations if you need an overlap window. Retry a rotation with its new journal
after a lost response. Suspension blocks the tenant's credentials at the gateway.
Resumption restores active credentials; revoked keys remain revoked. Usage comes
from Gregale's attributed request counters and may lag while its outbox drains;
this starter does not invent counters or turn them into invoices.

## Review a completed billing month

Configure an app or tenant request rate card through the Gregale owner API
before billing. Missing rates remain explicitly unpriced; the server refuses
to finalize them. The price is what your customer pays for request units,
not the platform's infrastructure cost.

```sh
node tools/customer.js billing-month <tenant-id> 2026-08 > ../billing-review.json
```

This command creates or refreshes **drafts** for stable UTC-day periods within
a completed UTC calendar month. It skips days with no usage and no prior
statement. It never finalizes a statement or records an invoice handoff.
Gregale's statement response groups invoice lines by app, consumer/surface/JWT
identity, and effective price source while retaining exact minute coverage
privately for adjustments. The JSON review groups those lines across the month.
Quantities and millicent totals are decimal strings to keep aggregate arithmetic
exact. Values beyond JavaScript's safe integer range in server responses are
rejected instead of silently rounded.

Daily periods let a customer active every minute across two apps review a full
31-day month (89,280 usage-minute coverage records) as two compact invoice lines
per day. The API line spans bound the contributing minutes and may include gaps;
they are not a minute-by-minute usage export. Exact minute coverage remains
stored for later adjustments, so its storage still grows with attributed usage.
Keep the daily periods stable for this customer's billing; do not mix monthly,
daily, and app-local handoffs for overlapping usage.

Inspect the rates, unpriced units, daily statement IDs and totals. Then finalize
each reviewed draft explicitly:

```sh
node tools/customer.js statement-finalize <tenant-id> <reviewed-statement-id>
```

Create the corresponding invoice line in your billing system using its own
idempotency mechanism, keyed by Gregale statement ID. Only after that system
confirms the line, record its unique reference:

```sh
node tools/customer.js statement-handoff <tenant-id> <statement-id> <unique-external-invoice-line-reference>
```

One external invoice reference cannot identify multiple Gregale statements.
Your billing system must support a distinct reference for each daily statement
and adjustment. Finalize and handoff calls safely replay the same statement and
reference after a lost response. Gregale records a handoff; it does not create
an external invoice or collect money. Owner scopes and MFA requirements apply.

Repeating `billing-month` refreshes open drafts and discovers late-usage
adjustments without editing finalized revisions. Its cumulative total includes
all finalized revisions plus current drafts; **do not charge that total again**.
The separate draft/finalized totals and per-statement statuses support review;
use the handoff API to check whether a finalized statement was already invoiced.
Each new adjustment needs its own statement ID and external reference.

A mid-run failure may leave some daily drafts created. Repeat the same command
to recover; it does not silently finalize partial work. The review reads each
day independently and is not an atomic month-wide snapshot. Wait for accounting
backlogs to drain before approval. Gateway exit-time metering can still miss a
served request on a crash before fsync; this workflow does not strengthen that
financial completeness guarantee.

## Isolation contract and extension points

The app trusts `X-Faas-Platform-Tenant-Id` only on Gregale's private guest listener.
The gateway strips caller-supplied values and supplies verified identity. Never
expose this listener directly to the Internet. UUID validation checks the header's
shape; it is not standalone authentication. Local tests supply headers through a
trusted test seam. To use another proxy, add its authenticated identity verifier.

Every query includes `tenant_id`. PostgreSQL additionally enables and forces
row-level security, using transaction-local tenant context on one checked-out
connection. Commit/rollback clears context before returning the connection to the
pool. Startup rejects superuser/BYPASSRLS credentials and missing forced RLS.
See [PostgreSQL row security](https://www.postgresql.org/docs/16/ddl-rowsecurity.html)
and [node-postgres transaction guidance](https://node-postgres.com/features/transactions).

Extend the same pattern to every new customer-owned table, join, cache key, object
storage prefix, and background job. Carry verified identity into jobs when you
enqueue them; workers must derive their database scope from that trusted job
record. Add user roles within each customer before exposing team administration.
The database owner can alter policies; use a separate migration role and a runtime
role with only SELECT/INSERT/UPDATE/DELETE privileges when hardening the deployment.
For that setup, run migrations from your trusted deployment pipeline with the
migration role and remove the `Procfile` release entry; the guest keeps only the
runtime credential.
Keep owner APIs on your control plane. This sample does not implement customer
login, payments, hostname provisioning, or a customer-facing administration UI.

## Run and test locally

```sh
npm ci --ignore-scripts
# Set MIGRATION_DATABASE_URL to the direct schema-owner connection.
npm run migrate
# Set DATABASE_URL to the restricted runtime connection.
HOST=127.0.0.1 npm start
npm test
# Uses a disposable database; this explicit gate fails if no URL is supplied.
CUSTOMER_DATABASE_URL="$DATABASE_URL" \
  CUSTOMER_MIGRATION_DATABASE_URL="$MIGRATION_DATABASE_URL" npm run test:postgres
```

The dependency-free tests cover tenant-scoped HTTP CRUD, invalid input, private
journals, hash-only API calls, and lost-response recovery. PostgreSQL tests prove
forced RLS, forbidden cross-tenant writes, and connection reuse after commit and
rollback. The Gregale repository's `make test-customer-platform` gate runs this
starter behind the actual gateway and PostgreSQL-backed owner API: two customers,
forged tenant headers, denied cross-customer reads/writes, atomic rotation,
suspension, resumption, and usage lookup. It requires disposable `DATABASE_URL`
and `CUSTOMER_DATABASE_URL` databases, plus `CUSTOMER_MIGRATION_DATABASE_URL`
for schema changes; no KVM is needed.

The gate also exercises monthly draft review, explicit finalization, replayable
invoice handoff, and late-usage adjustments through the real owner API and
PostgreSQL. Dependency-free billing tests cover a full-minute 31-day/two-app
month against a simulated accounting service; that volume test is not a
production metering benchmark. Native VM qualification is tracked in
`docs/reference-platform-qualification.md` in the Gregale repository.
