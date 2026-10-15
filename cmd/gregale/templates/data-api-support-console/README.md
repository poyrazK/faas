# Gregale support console starter

A customer-facing read-only support workspace: organization sign-in, approved
cases and replies, tenant-scoped search, relationship browsing and bounded CSV
export. This is separate from the privileged development inspector.

## Try the working example

~~~sh
gregale init --template data-api-support-console --path support-console
cd support-console
npm ci
npm test
npm run demo
~~~

Open http://127.0.0.1:3030, choose **Sign in with your organization**, then select
synthetic agent A or B. Agent A has 120 demo cases and related replies; agent B
belongs to another tenant. The demo implements OIDC code/PKCE and signed JWTs,
but uses a synthetic issuer and in-memory data. It uses no customer credentials,
does not model production authentication, and is excluded from the deploy image.
Set DEMO_PORT if 3030 is occupied.

## Connect a real identity provider and Data API

Create a separate Data API with reviewed support views. The Data API starter's
migrations/examples/support/support.sql demonstrates private base tables,
column-level SELECT grants, tenant SELECT RLS, and security-invoker exposed views.
Adapt it into a new numbered migration using your actual binding login. Keep
private schemas out of the API exposure. Do not grant broad base-table access.

Register this console as an OIDC authorization-code application with PKCE S256.
Register exactly https://YOUR_CONSOLE_HOST/auth/callback as its redirect URI.
The configured provider must support JWT ID and access tokens signed with RS256
or ES256 and publish OIDC discovery/JWKS. Configure the Data API for the same
access-token issuer, JWKS, and API audience.

The issuer must grant staff access deliberately. API access tokens require:

- sub and exp, with the correct issuer and API audience.
- gregale_read_only: true as a boolean, assigned by the trusted issuer.
- A nonempty tenant claim consistent with your database RLS.
- The same sub as the OIDC ID token for this application.

Configure these on your provider's authorized staff application/group. Requesting
a scope or changing a browser parameter cannot confer staff access. Test an
ordinary writable user, a foreign tenant, and a revoked/expired staff identity
against your real issuer and database before exposing customer data. This starter
does not provision accounts, group membership, tenant assignments, or SQL grants.

Edit support-console.json on the server to approve resource names, exposed
tables, columns, stable key columns, searchable fields, filter values and complete
relationship mappings. It starts with api.support_cases and api.support_replies.
Requests cannot select a different schema, arbitrary column, RPC, filter or
target URL. The console strips unapproved response properties as an additional
boundary; database grants and RLS must still enforce access independently for
clients calling the Data API directly.

Generate the current contract with the matching Gregale CLI and record its
fingerprint, for example:

~~~sh
gregale data-api types YOUR_SUPPORT_DATA_APP --snapshot --output support.schema.json
~~~

Use that snapshot's fingerprint for SUPPORT_SCHEMA_FINGERPRINT. Schema mismatch
blocks reads until the console configuration and approved fingerprint are updated.

## Configure and deploy

The console needs Node 22+ (the Docker image uses Node 24) and these application
environment settings. Store secrets using Gregale's secret facilities, not source
files. Do not provide owner database credentials or account-management tokens.

| Setting | Purpose |
| --- | --- |
| SUPPORT_ORIGIN | Exact public console origin, HTTPS in production. |
| SUPPORT_OIDC_ISSUER | Exact discovery/token issuer, preserving any trailing slash. |
| SUPPORT_OIDC_CLIENT_ID | Registered staff OIDC client ID. |
| SUPPORT_OIDC_CLIENT_SECRET | Optional confidential-client secret; uses client_secret_basic. |
| SUPPORT_OIDC_SCOPES | Provider-approved scopes, default openid profile; include your API read scope as needed. |
| SUPPORT_OIDC_AUDIENCE_PARAMETER | Optional audience or resource, according to your provider. |
| SUPPORT_DATA_AUDIENCE | Audience expected in the API access token. |
| SUPPORT_DATA_API_URL | Data API HTTPS origin, without a path, query or credentials. |
| SUPPORT_SCHEMA_FINGERPRINT | Reviewed 64-character lowercase contract fingerprint. |
| SUPPORT_SESSION_KEY | Private 32-byte base64url AES key, identical across replicas. |
| PORT | Application listen port, default 8080. |

Generate a private session key on your machine:

~~~sh
node --input-type=module -e "import {randomBytes} from 'node:crypto'; console.log(randomBytes(32).toString('base64url'))"
~~~

Save it directly through your approved secret workflow. Rotating the key signs
out existing console sessions. Configure the listed environment values, register
the public callback origin, then deploy normally:

~~~sh
npm ci
npm test
gregale deploy
~~~

The included gregale.yaml, Dockerfile and Procfile start the service on port 8080
and expose /healthz. npm start refuses missing/invalid configuration. Deployment
and real-provider verification are separate from running the synthetic demo.
No Data API CORS changes are needed: browser requests stay on the console origin
and the console server reads the API.

## Sessions and operations

Tokens never enter browser JavaScript, localStorage, rendered HTML or exports.
OIDC state, nonce and PKCE protect sign-in. API tokens live in an authenticated
encrypted HttpOnly cookie, with Secure in production and SameSite=Lax. The server
revalidates issuer, audience, expiry, staff flag and tenant before each API read.
Sessions expire at the earlier of token expiry or 30 minutes; users then sign in
again. There is no refresh-token/offline session flow.

Sign-out clears the browser cookie. It does not revoke the provider's JWT or end
a global provider session; copied cookies remain bounded by token/session expiry.
Use short-lived staff tokens and your provider's revocation policies. For
immediate revocation, extend this starter with your provider's supported token
introspection or a server-side session revocation store.

All data forwarding uses GET, the original staff token, approved projections and
a pinned schema. Related-row scopes intersect the token's database RLS. Reads
are capped at 100 rows and 1 MiB per response with timeouts; pagination uses live
ordered pages and can shift under concurrent writes. CSV export caps at 10,000
rows and 10 MiB, reports progress, supports cancellation, and produces no partial
download on failure. Spreadsheet mode protects formula-like text; JSON-cell mode
preserves NULL, structured values and exact numeric representations. Navigation
requires scalar safe keys; unrepresentable numeric keys are not used for navigation.

Keep the app and Data API under HTTPS, review the approved resource configuration,
and run your actual staff/tenant authorization checks alongside npm test.

### Saved views and links

Administrators can define up to 20 shared views in the optional `views` array in
`support-console.json`. Each entry has `name`, `resource`, `search`, `filter`,
`sort`, and `direction`; the starter includes Open cases. Restart the service
after configuration changes. Staff cannot edit these administrator definitions.

Save on this browser stores up to 20 personal views per OIDC subject and tenant.
These preferences contain settings only, persist after sign-out, and do not sync
across devices. Delete a personal view to remove it; clearing browser storage
removes all preferences. Avoid saving sensitive customer information in searches.

Create link produces a URL fragment containing settings, including search terms.
Review it before sharing. No records, relationship keys, tokens or credentials
are included. Recipients sign in and fetch fresh results under their own tenant
and permissions. Related-row scopes cannot be saved or shared. Invalid or stale
links fail validation against approved resources and columns.

### Customer timelines

The demo includes a case timeline combining cases and replies. Production timelines
are opt-in: expose timestamp and customer-key columns through approved API views
and column grants, then add a `timelines` array to `support-console.json`:

~~~json
{
  "id": "customer_history",
  "label": "Customer timeline",
  "resource": "customers",
  "from": ["tenant", "id"],
  "sources": [
    {"resource": "cases", "to": ["tenant", "customer_id"], "timestamp": "created_at", "title": "title"},
    {"resource": "orders", "to": ["tenant", "customer_id"], "timestamp": "created_at", "title": "status"}
  ]
}
~~~

All resources and columns must already be approved in the resource configuration.
Use complete tenant/customer mappings; database RLS remains responsible for access.
Each event needs a non-null ISO timestamp with at most millisecond precision and
unique scalar keys (numeric keys must be safe JavaScript integers). Selecting a
customer row opens merged newest-first events; equal timestamps use resource/key
tie breakers. Open approved record expands its approved columns.

Pages contain 25 positions in a fixed membership/order captured on the first read.
Each page re-reads sources using the current staff JWT and RLS. Newly added events
appear after reopening; removed or inaccessible events leave gaps. These are
sequential live reads, not an atomic database snapshot. Already displayed records
remain visible until closing or refreshing, as with ordinary console reads.

This first version rejects more than 100 matching events per source, invalid
timestamps, duplicate keys, or more than 1 MiB of combined event data. It never
silently truncates history. At most 20 session-bound cursors are retained in server
memory for five minutes. Restart, eviction, expiry or another service instance
requires reopening the timeline. Cursor state contains event identifiers, not
record bodies or tokens; use a single service instance for continuity.

### Customer lookup and profiles

Production customer pages are opt-in. Approve a customer API resource and its
relationships, then add the following top-level configuration:

~~~json
"customer": {
  "resource": "customers",
  "title": "name",
  "lookup": ["email", "account_number"],
  "relationships": ["cases", "orders"],
  "timeline": "customer_history"
}
~~~

Lookup uses exact equality on the selected approved column. Use appropriately
indexed fields and normalized identifiers in your API views; for order-reference
lookup, expose that identifier through an approved customer view. The optional
timeline must use the customer resource as its anchor. All relationships must
already be approved and map complete customer/tenant keys. Database RLS enforces
access on every resource. No new private tables or columns are exposed by default.

Results show 25 matches per page and require an explicit selection, including
when names or identifiers match multiple customers. Profiles re-read the customer
first, then show up to five records per configured relationship; Browse opens the
existing scoped browser for all records. Reads are live and sequential. Related
records and profiles remain visible until refreshed, closed or signed out.

Customer links contain the configured resource keys in the URL fragment. Review
identifiers before sharing. Links survive sign-in, re-fetch under the recipient's
identity, and show unavailable when the customer is deleted or inaccessible. They
contain no record bodies or credentials and are not stored in personal saved views.
Profile responses are bounded to 1 MiB; narrow columns or relationships if exceeded.
The synthetic demo includes duplicate lookup matches and isolated tenants.

### Support access audit

Authenticated customer lookups, profile reads, timeline pages, ordinary data reads,
and export page reads emit structured JSON events to server stdout. Events contain
only ID, UTC time, verified OIDC subject, tenant, action, outcome, HTTP status and
the approved resource name where available. Search values, customer/relationship
keys, URLs, record bodies, display names and credentials are excluded. Rejected
queries after authentication are recorded; anonymous attempts, login callbacks and
requests refused before staff verification are outside this access audit.

Set SUPPORT_AUDIT_ADMINS to a JSON array of explicit subject/tenant pairs, for
example [{"subject":"staff-subject","tenant":"account-a"}]. No administrator is
enabled by default. The signed-in administrator sees Access audit and can read
only the latest 200 retained events for their configured tenant. An external JWT
role claim cannot grant this access. The synthetic demo authorizes agent A only.

SUPPORT_AUDIT_RETENTION_DAYS defaults to 7 (1–30). SUPPORT_AUDIT_MAX_EVENTS defaults
to 1000 (1–10000), across tenants on this instance. Change deployment environment
and restart to change controls. The console stores a bounded in-memory recent
history; restart or another instance has a separate history. Configure your log
collector for durable storage, restricted access and its own retention. This
starter does not guarantee durable or tamper-proof audit delivery.

Export events describe server page reads, including failures. They do not attest
that a CSV completed or was downloaded; the browser may cancel or fail afterwards.
Audit inspection itself is recorded. Staff subjects and tenants are identifiers:
treat the audit log as restricted operational data.

### Durable PostgreSQL audit history

Apply migrations/audit.sql once, as the owner of a separate audit database. Create
a dedicated non-owner login without superuser or BYPASSRLS and grant only schema
USAGE and table SELECT, INSERT, DELETE as shown in the migration. Do not expose
this schema through the Data API. Configure:

| Environment | Purpose |
| --- | --- |
| SUPPORT_AUDIT_DATABASE_URL | Dedicated audit PostgreSQL connection; use your database's verified TLS configuration |
| SUPPORT_AUDIT_OUTBOX_DIR | Absolute path to a writable persistent volume, restricted to the console runtime user |
| SUPPORT_AUDIT_RETENTION_DAYS | Database history retention, 1–30 days; default 7 |
| SUPPORT_AUDIT_ADMINS | Explicit verified OIDC subject/tenant pairs, as above |

Without the database URL, the recent in-memory audit mode remains available.
With PostgreSQL enabled, startup requires the migration, a reachable database,
enabled FORCE RLS, and a non-owner login. The service supplies the verified tenant
inside each database transaction; RLS and an explicit tenant predicate protect
reads. The audit login is a trusted server credential, never a browser credential.

Events enter a flushed, atomically renamed file outbox before the data response.
If that cannot be queued, the console returns 503 rather than releasing records.
A single worker retries queued events every two seconds. Event IDs make inserts
idempotent, including replay after an ambiguous commit. Pending files are replayed
after restart. Mount a persistent volume and use one console process per outbox
directory; concurrent processes sharing an outbox are unsupported. Each queue is
bounded to 10,000 events. Separate instances may use separate outboxes and the
same PostgreSQL history. Files contain only the redacted audit event.

Administrators can filter by staff subject, action, outcome, and date range.
The view shows the latest 200 matching events. CSV exports contain at most 1,000
matching events and reject larger results until filters are narrowed. CSV cells
are spreadsheet-safe. Audit reads and exports are themselves audited. Delivery
status shows local queue depth and the last failure; database outages return a
visible administrator error while outbox retries continue. Export/queue status
does not attest that a browser completed a download.

Retention cleanup runs in each tenant's read/write transaction. Schedule the
owner-side DELETE shown in the migration to clean inactive tenants too; align its
interval with SUPPORT_AUDIT_RETENTION_DAYS across instances. The in-memory event
count limit does not limit PostgreSQL history. Backups, logs and outbox volumes
need their own retention policy. Events delivered after their retention deadline
are immediately removed. Durable storage depends on the configured database and
persistent volume; losing both is outside this starter's delivery guarantee.

### Approved support actions

Production actions are disabled by default. Start with add reply and close case:
apply migrations/support-actions.sql after the private support example in the
same database as the cases. Adapt the view definition to preserve any additional
approved columns. Grant SELECT(version) on the private cases table to the existing
Data API read binding login, expose version in the approved cases resource, and
refresh the pinned schema fingerprint. Add:

~~~json
"actions": [
  {"id":"add_reply","label":"Add reply","resource":"cases","idColumn":"id","version":"version"},
  {"id":"close_case","label":"Close case","resource":"cases","idColumn":"id","version":"version"}
]
~~~

Set SUPPORT_ACTION_DATABASE_URL to a dedicated PostgreSQL login. Grant only USAGE
on support_actions and EXECUTE on the six-argument function, as shown in the
migration. Never grant this caller private table access, receipt access, function
ownership, superuser or BYPASSRLS. The SECURITY DEFINER function's owner is trusted:
use a restricted migration/function-owner role with the required private-table
and receipt privileges, rather than a general application login. Keep the action
schema out of the Data API. The caller is a trusted server credential; do not
share it with browsers or other clients.

The OIDC access token must retain gregale_read_only:true and contain a signed
gregale_support_actions array with the permitted IDs. The server intersects this
claim with configured actions and checks it again on confirmation. The ordinary
Data API gateway and browser remain read-only; controlled actions use the separate
function connection with verified subject/tenant claims. The function independently
checks action permission and tenant and commits a redacted receipt with the change.

Staff select an action from an approved case row or related case in a customer
profile, enter a reply (maximum 1,000 characters), review the preview and confirm.
Preview binds complete resource keys, current integer case version, staff session,
tenant and a random idempotency receipt. Large encrypted previews are rejected.
Replies and closures increment the case version; concurrent changes require a new
preview. Reply bodies and customer keys are excluded from the access audit.

New mutations require confirmation within five minutes. Already committed
confirmations can replay their result for the remaining session, up to 30 minutes.
Expired, uncommitted confirmations cannot mutate. Duplicate submissions serialize
on the receipt ID and return the original safe result. Receipt ownership and
request hashes are checked. Keep database receipts while they may be retried;
do not prune them inside the session/retry window.

When the outcome is uncertain, the UI preserves the same confirmation and disables
editing or starting another action until a retry resolves it. Do not reload,
sign out or create a new request while resolving an uncertain outcome. Preview
state is in memory and is not synchronized between browsers. A successful action
asks staff to refresh the case/profile; no stale record is assumed current.

Support previews, attempts and outcomes emit redacted audit events. If durable
audit is configured, an attempt must enter its persistent outbox before mutation.
A database receipt commits atomically with the mutation, even if the final HTTP
response or access-audit delivery fails. Enable durable audit for production
oversight. The synthetic demo grants agent A both actions and agent B neither.

### Recovering action outcomes

Apply migrations/action-recovery.sql after support-actions.sql, as the trusted
function owner. It adds a private request registry, wraps execution, and adds
registration, submission tracking and read-only recovery functions. Existing
completed receipts are backfilled into recovery history. Revoke the
runtime login's execute_mutation grant after the rename and grant only the new
functions listed in the migration. Existing action installations require this
migration before restarting the updated console.

Preview registers the durable action ID before returning a confirmation. The
registry contains subject, tenant, action, request hash, deadline, state and safe
result; it never stores reply text or customer keys. Confirm records an unresolved
submission in a separate committed transaction before attempting the mutation.
Completed state and result commit with the mutation; known version/expiry rejection
is committed as rejected. Connection or unexpected database failures remain
unresolved rather than being reported as successful or safely rejected.

Recent actions shows the latest 50 requests for the currently verified staff
subject and tenant, including after reload or signing in again. Enter an action ID
to recover an older request. Recovery only reads; it does not call the mutation
function or resubmit an uncertain action. Signed action permissions may be revoked
without hiding the same staff member's historical outcomes. Other subjects and
tenants cannot recover those requests.

Previewed means confirmation was not submitted. Unresolved is not a failure:
the service may have recorded submission before losing its connection. Refresh
recovery or ask an administrator to investigate; do not create a replacement
action merely because a result is unavailable. Completed returns the original safe
result. Rejected means the guarded mutation did not commit. Database unavailability
is shown as unavailable, never an empty success.

The registry and receipt table are durable and must be retained together. This
starter performs no automatic deletion of action recovery history. Define a
customer retention policy, retain receipts beyond all retry windows, and prune both
tables deliberately. The demo registry is synthetic in-memory data and resets when
the demo process stops; production recovery uses PostgreSQL.

### Case assignment and queues

Apply migrations/case-assignment.sql after action-recovery.sql. It adds an optional
assignee to cases, an indexed tenant/assignee/status queue, and private assignment
history with a security-invoker API view. Adapt the cases view definition to retain
any custom columns. Grant the Data API read binding SELECT(assignee) on private
cases, SELECT on approved assignment-history columns, USAGE on the private schema
and SELECT on api.support_assignment_events. The separate action caller still
gets function execution only. Refresh the pinned contract fingerprint.

Add assignee to the approved cases resource and configure:

~~~json
"queues": {"resource":"cases","assignee":"assignee","status":"status"}
~~~

Add assign_case (label Assign to me) and unassign_case (label Unassign case) to the
actions array, using the same case ID/version columns as existing actions. The
OIDC token must explicitly include each permitted ID in gregale_support_actions.
Assignment is self-service in this version: the verified subject is the assignee;
staff cannot supply another person's identity. Unassign permission can clear any
visible case in the tenant. Both actions require an open case and the previewed
version and increment that version. Changing assignment between two previews
makes the later confirmation conflict.

My open cases derives the assignee from the current verified subject. Unassigned
open cases selects NULL assignees. Both intersect ordinary filters, relationship
scopes and database RLS; paging and CSV export preserve the queue. Saved/shared
queue views keep identity-relative meaning, so My cases means the recipient's
cases when someone else opens a link.

To show assignment history in a case timeline, approve an assignment-events
resource with tenant/id keys, occurred_at timestamp and title, then add a source
mapping from the case's tenant/id to the event's tenant/case_id. Approve only the
history fields your staff need. Actor and assignee are staff identifiers, not
credentials. Previous/current assignment and history commit atomically with the
version change and idempotent receipt. Redacted access audit and Recent actions
include the new action IDs. Repeated confirmation creates one history event.

### Priority and first-response deadlines

Apply migrations/case-priority.sql after case-assignment.sql as the private-table
owner. This migration uses a transaction to backfill timestamps while preserving
FORCE RLS afterwards; adapt its transaction wrapper if your migration runner
already owns the transaction. Preserve custom cases-view columns, grant SELECT
on approved priority/clock fields to the read binding, and refresh the pinned
contract fingerprint. No extra private-table grants go to the action caller.

Add priority, created_at, first_response_at and response_due_at to the approved
cases projection. Add set_priority to the actions configuration and explicitly
grant that ID in the signed OIDC gregale_support_actions claim. Staff select Low,
Normal, High or Urgent, review the preview, then confirm. Priority changes use
case-version guards, idempotent receipts, audit events and Recent actions.

Extend queues with deadline:"response_due_at", firstResponse:"first_response_at"
and dueSoonMinutes:60 (integer 1–1440). Due soon means unanswered open cases with
a deadline from the server's current time through that window. Overdue means an
unanswered open case with a deadline before now. Closed and answered cases are
excluded. These queue predicates intersect existing filters, RLS and related-row
scopes; paging, exports and saved links preserve their relative meaning.

The administrator-only support_private.response_policy table maps each priority
to a response budget in minutes: default low 2880, normal 1440, high 240, urgent 60.
Administrators can update budgets from 1 to 43200 minutes. New cases use creation
time plus that budget. A priority change recalculates an unanswered case from its
original creation time, rather than resetting the clock. Policy updates affect
new cases and later priority changes; existing deadlines stay fixed until an
explicit recalculation. Once a staff reply is committed through add_reply, the
first-response timestamp is recorded and priority changes retain the old deadline.

This version uses elapsed time, not business hours or holidays. Existing cases
without historical timestamps start their clock at migration time; cases with
existing replies are marked answered at migration time, since their original
response timestamps are unknown. Other reply-writing paths must set the
first-response timestamp too if used alongside this starter.

Priority history commits with the mutation in support_private.priority_events.
Approve its safe fields through api.support_priority_events and add a timeline
source mapping case tenant/id to event tenant/case_id, with occurred_at and title.
The synthetic demo includes an overdue case and a case due soon.

### Internal notes and case activity

Apply migrations/internal-notes.sql after case-priority.sql. It creates a separate
private notes table and security-invoker API view, and appends a timestamp to the
reply view for activity ordering. Preserve custom view columns, grant only approved
notes/reply timestamp SELECT columns to the read binding role, and refresh the
pinned contract. Existing replies receive migration-time timestamps because
historical timestamps are unavailable. The action caller still gets function
execution only and no direct note-table access.

Add add_note (label Add internal note) to the approved case actions. A staff token
needs both the signed boolean gregale_internal_notes:true and add_note in
gregale_support_actions to write notes. Reading requires gregale_internal_notes:true,
the existing read-only claim, and matching tenant RLS. Note permission is distinct
from permission to send a customer reply. Never grant it to customer identities.

Approve a notes resource with staffOnly:true, a clear label such as Internal notes
(staff only), tenant/id keys, and only the columns needed by your staff. The server
hides staff-only resources and timeline sources from unauthorized sessions, and
rejects direct reads and exports; the database independently enforces the claim.
Do not expose notes through a customer-facing view, reply relationship or email
integration.

Configure a Case activity timeline anchored on case tenant/id with sources for
replies, notes, assignment events and priority events, each mapping to tenant/case_id.
Use occurred_at for event timestamps, body for reply/note titles and title for
assignment/priority history. Keep resource labels clear so internal notes and
customer replies remain visibly different. Existing bounded, newest-first timeline
paging and current-permission checks apply.

Notes use the same preview, confirmation, case-version guard, idempotency and
durable recovery as replies. They create one separate note record, increment the
case version, and never set first_response_at or create a customer reply. Only
open cases accept a note in this first version. Note content is absent from access
audit events and recovery results, which contain only safe note IDs/outcomes.
Staff should follow their organization's retention and sensitive-data policy for
note bodies; this starter does not automatically delete them.
