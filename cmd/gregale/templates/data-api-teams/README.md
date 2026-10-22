# Team orders dashboard

A typed application starter with database-backed team membership, owner/admin
writes, read-only members, tenant-filtered pagination and reliable order saves.
The application API never uses migration or management credentials.

## Start locally

With Node.js 22+, npm, a local Docker daemon and an approved matching bundle:

```sh
gregale data-api onboard --template data-api-teams --path team-orders --bundle /path/to/data-api-bundle.json
cd team-orders
gregale data-api dev --port 0 --check
```

Alternatively, initialize with `gregale init --template data-api-teams --path
team-orders`, then run `gregale data-api install --bundle MANIFEST
--client-directory client` inside it. `data-api dev --port 0 --once --check-types
--check` verifies the committed types, client and required SQL request scenarios.

In a second terminal in the project directory:

```sh
npm run build:browser --prefix client
npm run serve --prefix client
```

Open `http://127.0.0.1:3030`. Enter the dev session's API URL, `shop-a` and a
matching test JWT from the private session file printed by dev. Tokens and the
inspector link are private; do not commit or share them. Paste a current token
for every action. The page clears the token field immediately and keeps the
value in memory for that action only.

| Identity | Database membership | Behavior |
| --- | --- | --- |
| owner / alice | shop-a owner | Read, create and update shop-a orders |
| admin / amy | shop-a admin | Same order permissions as owner |
| member / ann | shop-a member | Read shared shop-a orders; saves denied |
| other / bob | shop-b owner | Access shop-b only |
| outsider / eve | None | No team access |

The owner and other test JWTs intentionally claim the wrong tenant. Access comes
from `api.team_members` and the verified JWT subject, never from the tenant
claim or a caller-selected role. Ordinary REST membership queries reveal only
the current subject's memberships; the owner-only directory RPC lists members
of the requested team. Direct REST membership writes are denied; owner-authorized RPCs provide
revision-checked changes.
Owners also manage membership and review access history through the
administration controls described below. Admins manage orders only.

Choose **Connect and load** after changing identity or team. Owners/admins can
create an order, or choose **Edit** to load its version into the form. Amounts
are integer cents; statuses are draft, submitted or fulfilled. Filter by status
and choose **Reload**, then **Next page**. Pages use tenant-scoped UUID order and
an exclusive cursor. They are not a snapshot: inserts or status changes between
pages can change which rows appear. Filters reset the page cursor.

## Order summaries through the Data API

Migration `0012_team_order_summary.sql` adds the read-only
`summarize_team_orders` RPC. Apply the migration, approve its EXECUTE grant
through the normal owner workflow, refresh the runtime and regenerate types.

```ts
import { teamsClient } from './orders.js'
import { parseOrderSummary } from './order-summary.js'

const team = teamsClient({ url, accessToken }, 'shop-a')
const filters = { status: 'submitted' as const, minimum: 100 }
const controller = new AbortController()
const result = await team.summary(filters).abortSignal(controller.signal)
if (result.error) throw result.error
const summary = parseOrderSummary(result.data[0], team.tenant, filters)
// summary.order_count and summary.total_cents are exact decimal strings.
// summary.by_status contains draft, submitted and fulfilled buckets.
```

Summaries include every matching accessible order, independently of list-page
limits. Status and minimum/maximum whole-cent filters are optional; ranges are
inclusive, with the existing integer amount range of 0–2,147,483,647 per order.
Use `{}` for all orders. Pass the same filters to the response parser. Counts
and summed cents remain strings; use `BigInt` for exact arithmetic. Empty teams
and empty matching sets return zeros for the overall result and all statuses.

Current database membership controls access, including for unchanged JWTs after
revocation. Members may read summaries. The order table's current RLS policy
also applies. Missing membership returns HTTP 403. Empty/null team IDs and
invalid ranges return HTTP 400. Requests preserve native errors, cancellation
and explicit retry control. Results use one statement snapshot. Requests already
in flight can finish with their earlier view after a concurrent membership change.

The existing 15-second statement/request budget applies. A small response still
can require scanning many rows, so broad queries on large teams may time out.
Failed calls return no partial or cached totals. Status enum changes require
reviewing the paired parser alongside regenerated types.

## Team administration

Choose **Load members** as an owner. The directory shows this team's user IDs,
roles and **Manage** buttons, with cursor pagination. Use **Add existing user**
for a verified identity-provider ID, or **Manage** an existing row to change its
role, remove access or transfer ownership. Confirm the intended change before
submitting. Only owners can perform these actions; admins and members cannot
promote themselves, join another team or list its members.

The append-only `0002_team_administration.sql` migration introduces an
owner-provisioned `api.team_users` registry. It registers the IDs already present
in legacy memberships. The **Add existing user** workflow requires IDs to be
registered by trusted owner migrations, identity-provider provisioning, or a
previous verified invitation acceptance. New invitees can self-register their
verified subject through the invitation workflow below.
The registry is not an authentication service and is not a global user directory
for application users; HTTP clients cannot register arbitrary IDs. The local
`seed.sql` registers test IDs, including Eve for the add/remove walkthrough.
Do not deploy those fixtures to production.

Membership rows have a generated UUID revision. Edits and removal require the
revision from the displayed row; stale revisions fail instead of overwriting
another owner's work. A role-away/role-back change or removal/re-add creates a
fresh revision, so stale requests cannot become valid again. Approved mutations
serialize by tenant and recheck the actor's ownership after acquiring the lock.
A database trigger guards owner loss and immutable membership keys, including
ordinary owner SQL; it refuses demotion or removal of the last owner.
Membership mutations require READ COMMITTED snapshots. The RPC declares this
isolation for PostgREST; direct SQL callers must use it too. The trigger rejects
higher isolation levels instead of checking ownership against a stale snapshot. Bootstrap
new teams with an owner in an owner-controlled provisioning transaction. Closing
an entire team is a separate owner maintenance workflow; this API does not
provide team deletion or a bypass of the last-owner rule.

**Transfer ownership** promotes the selected existing member to owner before
changing the current owner to admin, in one transaction. It always transfers to
another user. Both changes commit together; the dashboard refreshes the actor's
permissions and disables owner controls afterward. Multiple co-owners are
supported; owners may remove or demote one another only while an owner remains.

Each membership operation uses an immutable request key and payload. Its
append-only audit event is also its committed response receipt. **Resolve pending
change** first reads the exact receipt under the original authenticated subject;
if present, it confirms completion without repeating a mutation. This still
works after the operation demotes or removes its actor. A former actor can read
only their exact-key receipt, not the team directory or general audit history.
If no receipt is found, the controller may explicitly retry with the same key
and arguments only after verifying the original subject is still an owner.
The server rechecks authority; a changed user cannot silently replay another
owner's operation. A missing receipt alone does not prove that a timed-out
operation rolled back. Do not discard uncertain operations and issue new keys.

Pending order saves and access changes lock the other mutation controls, API,
and team. Pending state stays in memory, so keep the page open until resolved.
After a confirmed access change, a failed permission refresh does not undo the
change or retry it; reconnect to verify access. An old receipt describes the
original operation, not necessarily the current role after later changes.

**Load history** and **Older changes** show actor, target user, old/new roles and
server time to current owners only. Transfers also record the actor's demotion
and both membership revisions. History pagination preserves the server's full
timestamp and uses the event UUID as a tiebreaker. It is not a snapshot of future
changes or a statement of commit order. Audit rows cannot be edited or deleted
through HTTP. They contain user IDs and retain the original change metadata;
owner retention/privacy maintenance must account for those records and the
pending-operation retry window. There is no automatic expiry or purge tool.
Only changes through the approved RPCs are recorded; privileged SQL maintenance
is not captured by this history. This is application audit history, not a
tamper-proof compliance ledger against the database owner. No invitation email, identity creation or provider lookup is
performed by the dashboard.

Run `--scenario team-administration --replay data-api.requests.json --once` for
the saved ownership/role/audit requests. They restore the original local roles
and use captured fixture revisions as unique keys to support repeated replay;
production clients generate a fresh UUID per logical operation. SQL and Chromium
acceptance also exercise concurrent changes and committed-response loss.

## Save and recover

`save_team_order_once` creates when `expected_version` is zero and updates only
the supplied positive version. A stale, deleted or RLS-hidden row returns the
same version-conflict response. Concurrent edits cannot overwrite each other;
reload and review the current order before submitting another operation.

Each logical operation has an application-generated UUID key and order UUID,
plus immutable tenant, expected version, amount and status arguments. Receipt
keys are scoped to tenant and authenticated subject. The receipt stores the
original response separately from mutable order data, so replay after a later
edit returns the original snapshot without reverting that edit. Order primary
keys also include the tenant; a UUID used in another team reveals no global
identity collision. Failed transactions retain neither writes nor receipts.

The client validates inputs before transport, validates the whole RPC response,
disables automatic retries and bounds each request to five seconds. A timeout,
lost response or malformed successful response can happen after commit. The
form retains its original actor and frozen arguments and blocks new saves. Keep
this page open and choose **Resolve pending save**. Supply a fresh JWT for the
same authenticated subject and API/team. Membership is verified by the server
before replay; switching to another authorized admin is refused by the client.
A revoked or downgraded membership prevents replay on the server too.

Recovery may complete an uncommitted operation or return its committed receipt;
it is not a read-only status check. The confirmed result can be historical;
choose **Reload** to read the current order. A failed refresh does not replay a
confirmed save. Tokens and pending operations are not persisted; closing the
page loses the pending key. Production session management and pending-operation
storage must enforce identity, privacy and receipt-retention rules.

Receipts have no automatic expiry. Direct HTTP reads, changes and deletion of
receipts are denied, as are direct order mutations that bypass the approved RPC.
The RPC uses an owner-reviewed transaction-local scope under SECURITY INVOKER;
that scope protects the HTTP surface, not someone holding the raw serving SQL
login who can set PostgreSQL parameters. Review other allowed functions before
adding them. Never disclose binding credentials to application users.

Owner maintenance may remove receipts only after the supported retry/offline
window has elapsed. Purging a receipt ends its duplicate-prevention window;
an old update replay may then conflict, and an old create has no receipt proof.
This starter provides no purge tool and no delete endpoint. Use reviewed owner
maintenance for deletion/privacy workflows, including receipt snapshots. External
payments, notifications and other side effects need an outbox or their own
idempotency; this RPC protects only its database transaction.

## Deploy and evolve

The scaffold includes a separate migration app and the existing owner migration,
release, RPC permission and artifact tools. Local `seed.sql` is excluded from
migration-app uploads and is never run by production release. Do not deploy
local Alice/Bob membership rows. Provision actual tenant memberships through a
reviewed owner migration using your identity provider's verified subjects.
Existing projects should copy the migration into their next unused append-only
migration number and review names/policies first; do not overwrite existing SQL.

Follow Gregale's Data API guide to reserve the migration app, attach its managed
migration binding, create the serving Data API with issuer/JWKS/audience and
HTTPS application origin, apply explicit RPC grants and refresh the runtime.
Owners must not give a serving binding owner privileges or RLS bypass, or add
unguarded membership, identity-registry or receipt mutation policies. Upgrade
existing starters by applying the new append-only administration migration,
regranting all approved RPCs, refreshing the runtime, regenerating types and
updating the client/collection. Preserve existing migrations and test identities. Changes to membership rows take
effect without regenerating types; active statements retain PostgreSQL snapshot
semantics. A JWT does not cache role membership in this design.

Export hosted types into `client/src/database.types.ts`, compile and run two-team
hosted authorization checks with real application subjects. Use `data-api sync`
with your migration-app name and client check command, and the existing
coordinated refresh workflow when required. Configure secrets through bindings
and JWT environment variables. No hosted qualification is claimed by local tests.

Commit generated types alongside migrations. `--check-types` rejects drift
without replacing declarations; `data-api check` combines structural compatibility
and freshness checks. Nullable additive fields can preserve old explicit
projections, while dropped columns or enum changes require compatibility review.
Fingerprints do not encode RLS bodies, index definitions or RPC behavior; keep
policy and behavioral tests alongside type checks. Review historical receipt
snapshots when changing the response shape. An approval never waives stale types.

For application CI, pin immutable HTTPS CLI/SDK artifacts with
`node tools/artifacts.mjs pin MANIFEST --base-url HTTPS_ARTIFACT_DIRECTORY`, then
commit `data-api-artifacts.json` and the client lockfile. The generated GitHub
workflow restores the verified pair, checks declarations, runs client tests and
team isolation/saving scenarios, then builds the browser. A local-only pin needs
`--from` when restoring and cannot satisfy that workflow's hosted download step.

The dashboard displays curated errors and request IDs from SDK response
metadata. Use `gregale data-api status APP` and `gregale data-api diagnostics APP
--request-id UUID` to investigate hosted failures. Do not log tokens, membership
subjects, order payloads or raw database error details.

## Self-service invitations

Owners choose **Create invite link** for member or admin access. The link expires
in 24 hours; the API accepts explicit expiries up to seven days. Share it privately.
It is a bearer credential: anyone holding it may accept with a verified application
session. This is not an email-bound invitation. Owner access is granted through
membership administration and the separate ownership transfer workflow.

An invitee opens the link, chooses their trusted API URL and supplies their current
application token, then selects **Accept for selected team**. Connect is not
required before acceptance. The verified JWT subject is registered and joined in
one transaction; no owner provisioning of that subject is required. Gregale does
not create identity-provider accounts or send email. Initial team owners still
require trusted bootstrap provisioning.

The secret uses 256 random bits, appears only in the URL fragment and action
memory, and is stored only as a SHA-256 hash in PostgreSQL. The page removes the
fragment from browser history immediately, sends no referrer, and never selects
an API endpoint from the link. Do not log authenticated RPC bodies or SQL parameters. Do not paste links into logs, analytics, support
reports or public chat; extensions, screenshots and scripts on the page can see
them. Use HTTPS for hosted browser and API origins. **Clear shared link** discards
the displayed secret; owner lists and audit history cannot reconstruct it.

Owners can page through invitations, revoke pending links and view the latest 20
invitation events. Acceptance requires the issuer to be a current owner. Revocation,
expiry and acceptance serialize with membership changes; one acceptance wins.
Existing members are refused without changing their role or consuming the link.
The original accepting subject may replay its historical receipt, including after
expiry or member removal; replay never restores access. No other subject can reuse
a consumed invitation. Removing a member remains a separate access change.

**Resolve pending invitation** explicitly reuses the original key and payload,
after verifying the original signed-in user. Pending invitations lock other
mutations and session fields. Keep the page open until resolved; no browser
storage, automatic retries or secret recovery service is used. If a creation
response is lost and the issuer loses owner access, an owner can inspect/revoke
the invitation; the client cannot reconstruct a discarded secret.

`0003_team_invitations.sql` adds invitation records, immutable create/revoke/accept
history and reviewed SECURITY INVOKER RPCs. HTTP cannot directly change or delete
invitations, register arbitrary subjects, or alter history. Privileged SQL owners
remain responsible for retention and can change the database, just as for the
membership audit. Accepted user IDs and invite metadata are personal data.

Run the required `team-invitations` scenario for creation, role limits, isolation,
acceptance, replay and audit checks. All secrets in seed/scenario fixtures are
public test values and must never be used for real invitations. Local acceptance
also tests expiry, concurrent acceptance, revocation and browser response loss.

## CSV import and export

Owners and admins can load a CSV file or paste CSV text, read the headers and map
columns to total cents, status and optional order ID. Commas, quotes, CRLF, UTF-8
BOMs and multiline quoted fields are supported. The local preview validates all
records, reports errors by logical record number (including the header), and
shows up to 20 valid records. It sends no requests or database writes. Fix every
error and approve the create-only policy before **Register reviewed import**.
Missing IDs receive UUIDs retained in the reviewed plan. Map stable order IDs
when you need duplicate detection across separate imports; reimporting a file
without IDs creates new orders. Changing CSV, mapping,
API or team invalidates the preview. Amounts are integer cents, never decimals.

Registration stores the immutable reviewed manifest without writing any orders.
**Continue next batch** processes up to 20 rows in one transaction, atomically
with its inserted/skipped counts and cursor. Existing IDs are skipped without
changing their values, versions or creator. Duplicate IDs inside a file are
validation errors. This first workflow creates orders; it does not update them.
A concurrent insertion may change a row's disposition from insert to skipped.
Application bounds are 1 MiB CSV/manifest and 1,000 records per import; larger
imports should be split into reviewed files. These are starter workflow bounds,
not platform plan quotas.

If a response is lost, **Resolve pending import** explicitly repeats the original
operation as the original signed-in owner/admin. An old batch cursor returns
current progress without processing another batch. A future cursor is refused.
Pending requests lock other mutations and session fields. Keep the page open to
resolve an uncertain registration; its generated key and manifest live in memory.
After registration is confirmed, **Load import history** and **Resume / inspect**
restore persisted progress even after a browser reload. Only the original actor
can continue a job. Owners see the team's history; admins see their own; members
cannot import or inspect manifests. Losing write permission stops further batches.
There are no automatic retries or background workers.

History records the user, source label, timestamps, total, processed, inserted and
skipped counts. Manifests remain in PostgreSQL for recovery; they contain customer
data, so owner retention/privacy maintenance must include them. Direct HTTP edits
and deletion of history are denied. Privileged database-owner maintenance is not
captured or prevented by this application workflow. No automatic purge or rollback
of previously committed batches is supplied; separate reviewed edits handle
correction after an import.

**Export filtered CSV** scans orders visible to the current user using the selected
status filter and UUID cursor, up to 10,000 orders. It refuses an oversized export
instead of silently truncating it. This is a live scan, not a transaction snapshot;
concurrent changes can affect its contents. It exports IDs, total cents, status,
version and creator, escapes CSV quotes and spreadsheet formula prefixes, and
creates a browser download without uploading the file elsewhere. Members may
export their readable team data. To reimport, map ID, cents and status; exported
version and creator columns are ignored and existing IDs are skipped.

The append-only `0004_team_order_imports.sql` migration and required
`team-order-imports` scenario cover reviewed plans, actor/tenant isolation,
protected history and duplicate-safe progress. PostgreSQL and Chromium acceptance
also cover concurrent batches, committed-response loss, account switches and
reload-to-resume behavior. This starter is the first supported import shape;
other application schemas need their own reviewed parser and database RPCs.

## Reviewed order workflows

New orders created with ordinary saves start as drafts. Only draft totals may be
edited; the status control is read-only. Choose an order, confirm its status change
and use **Submit order** (draft → submitted) or **Fulfil order** (submitted →
fulfilled). Submission freezes the total; fulfilment is final. There is no reopen,
cancellation or refund workflow. Imported initial statuses remain supported for
historical data; their later updates follow the same rules. Application owners
should review imported states as part of CSV approval.

Each action requires the displayed version and a fresh UUID request key. The
server verifies current owner/admin membership after acquiring the team lock,
then commits the transition, new version, event and immutable response receipt in
one transaction. Concurrent actions cannot both consume the same version. Direct
REST updates remain denied, and ordinary saves cannot change status or edit a
submitted/fulfilled order. The database trigger also checks later SQL updates;
privileged database owners still control the schema and session settings.

**Resolve pending action** explicitly replays the original actor, key, order and
version. Pending actions lock other writes and session fields; keep the page open
until resolved. Recovery requires the original subject to retain owner/admin
access. If it has lost access, an owner can inspect order/history while the actor's
write permissions remain revoked. There are no automatic write retries. An old
receipt confirms its original transition, not the current status after a later
fulfilment; reload before another action. A failed refresh does not repeat a
confirmed operation.

**History** beside an order shows draft edits, submit/fulfil events, actor, server
time, version and total. All current team members may read it, including read-only
members; other teams cannot. Pagination follows versions rather than timestamps.
History starts with updates after `0005_team_order_workflows.sql`; no old events
are invented and creation/import is covered by its own saved receipts/import
history. Replays add no events. HTTP cannot forge, edit or delete history. Records
contain customer user IDs and historical totals and need owner retention/privacy
maintenance; privileged SQL maintenance without a reviewed application scope is
not audited. This is application history, not a database-owner-proof ledger.

The required `team-order-workflows` scenario verifies submission, fulfilment,
version conflicts, immutable replay, history and denial of an ordinary-save
bypass. The complete starter collection stays within the existing 100-request
bound; repeated denial variants are covered by the PostgreSQL acceptance suite.
The stricter ordinary-save behavior is intentional: callers that previously
changed status with `save_team_order_once` must use the reviewed action RPC.

## Transactional webhook delivery

`0006_team_order_outbox.sql` enqueues an immutable snapshot when a reviewed submit
or fulfil event is recorded, in the same transaction as its order, history and
receipt. Draft edits, imported initial states and old transition replays do not
produce webhooks. The queue starts with new transitions after this migration;
there is no backfill. Each event has a stable random UUID, order/version and
`order.submitted` or `order.fulfilled` type, actor, total, team and timestamp.

The owner dashboard pages deliveries and attempt history and lets owners replay
exhausted failures. Members/admins cannot inspect the queue or replay it. Replay
preserves event ID and snapshot, advances a checked generation, resets the
five-attempt cycle and retains lifetime attempts. Completed deliveries and live
leases cannot be replayed. **Resolve pending replay** reads its generation without
sending another mutation; a changed generation may be another owner's successful
replay. It confirms the requested generation advance, not who performed it.
If no advance can be confirmed, inspect current state before any further replay.

The runnable worker lives in `workers/order-webhooks.mjs`. Restore the approved
SDK bundle into `client` first. Configure the following through your worker's
secret/environment manager, never through browser inputs or committed files:

| Variable | Purpose |
| --- | --- |
| `DATA_API_URL` | Trusted HTTPS Data API origin |
| `OUTBOX_WORKER_TOKEN` | Issuer-signed dedicated worker JWT |
| `OUTBOX_TEAM` | Exactly one team to dispatch |
| `OUTBOX_WEBHOOK_URL` | Trusted HTTPS receiver endpoint, without credentials, query or fragment |
| `OUTBOX_SIGNING_SECRET` | 64 hexadecimal characters from 32 random bytes |
| `OUTBOX_NAMESPACE` | Stable deployment name, 1–128 letters, digits, hyphens or underscores |

Worker JWTs require boolean `outbox_worker: true` and `outbox_team` equal to the
configured team. These are trusted issuer/server claims, never user-editable
profile metadata. A worker needs no team membership and cannot read/write orders
or manage members. Its approved claim/finish RPCs operate only on that queue;
direct REST updates are denied. Keep worker tokens and signing keys private.
The local private dev identity file includes a test-only `delivery_worker` token.
`OUTBOX_ALLOW_LOOPBACK=1` permits local HTTP localhost/loopback testing; it never
permits arbitrary HTTP destinations. Hosted destinations stay HTTPS. The worker
uses the runtime's Fetch transport; preserve your runtime's proxy and TLS trust
configuration and platform egress policy.

Run `npm run outbox:once` from the scaffold root to dispatch one available event.
Schedule repeated invocations with your own protected worker/cron environment.
This is application-owned delivery code, not a new managed Gregale worker fleet;
no scheduler, hosted resources or credentials are created by onboarding. Worker
files are excluded from the owner migration app's upload, as are client artifacts.
Run the worker separately with its verified SDK and dedicated JWT, never the
migration-owner DSN. All workers for a team must use the same destination,
namespace and signing configuration; this queue delivers to one destination per
team, not to multiple subscriptions. Changes to destinations affect pending
retries too, so coordinate them as an operator change.

Claims use SKIP LOCKED, caller-generated UUID tokens and 30-second leases. HTTP
calls time out after 10 seconds; redirects are not followed and only 2xx is
success. Failure schedules exponential backoff (2, 4, 8, 16, then 32 seconds) up
to five claims. A crash consumes an attempt. Expired leases become claimable;
an expired final lease becomes dead on the next claim sweep, or can be replayed
by an owner. Stale acknowledgements cannot replace a new lease or completed
state. A claim-token replay returns its live lease. Transport/database failure
may leave acknowledgement uncertain: the worker fails with a curated message
and leaves lease-based recovery to subsequent invocations. It never logs tokens,
URLs, signing secrets, provider error bodies or request payloads. History stores
only attempt times, leased/delivered/failed/expired outcome and optional HTTP code.

This is **at-least-once delivery**, with no global ordering promise. A crash after
a receiver commits but before acknowledgement can send the same event again.
Replay of a failed delivery can also repeat a side effect. The receiver must
verify `gregale-signature` against raw request bytes before JSON parsing, using
`verifyWebhook` from the worker module. The header is `t=<Unix seconds>,v1=<HMAC
SHA-256 hex>` over `<timestamp>.<raw body>`; verification uses constant-time
comparison and a five-minute past/future clock window. Keep receiver clocks in
sync, bound request size and reject a bad signature before doing any work.

Derive deduplication identity from the **signed body's** `namespace` and `id`;
`gregale-event-id` is an unsigned convenience header and must match that identity
if used. Persist the identity in a receiver inbox with a unique key, atomically
with its business side effects, and return 2xx after commit. Already-processed
events should return 2xx without repeating side effects. A valid HMAC alone does
not prevent replay within the clock window. Keep namespace stable across retries
and replays; coordinate secret rotation with receiver verification. Event UUIDs
are random, but a restored/reset environment still needs its own namespace.

Queue snapshots and attempt records contain customer data and need owner
retention/privacy maintenance. HTTP cannot delete or alter history; privileged
SQL owners control their own database. There is no automatic purge or receiver
rollback. Local HMAC/HTTP/PostgreSQL/Chromium tests cover the delivered behavior;
hosted qualification and real destination delivery require operator setup.

## Tenant delivery operations

Owners can **Load queue health**, **Pause new claims** and **Resume new claims**.
Pausing stops allocation of new leases after the pause transaction commits. It
never cancels an active HTTP request: an existing lease can be recovered with
its original token and finished while paused. New transitions continue to enqueue
snapshots, and owner replay may reset failures while paused; none dispatch until
claims resume. Pause/resume uses a UUID settings revision to reject stale screens,
even if another owner toggles the setting away and back. Teams start unpaused
without a settings row. Worker scheduling and signing configuration are unchanged.

Health reports mutually exclusive pending, leased, delivered and dead counts,
plus oldest pending age in whole seconds. Pending includes delayed retries and
expired non-final leases; expired final leases count as dead before a worker
sweep persists that state. Age measures enqueue time, not the most recent retry
schedule. `0007_team_delivery_controls.sql` recovers existing enqueue timestamps
from matching order history; records without that history retain migration time.
Health is a read snapshot, not a forecast or a guarantee that a worker is running.
Only current owners can inspect it.

**Preview failed deliveries** reads at most 20 eligible dead or expired-final
records without changing them. Review the listed event IDs and generations and
approve **Replay reviewed batch**. The server resets exactly that set atomically;
a missing, already-replayed, delivered, active or otherwise changed record rejects
the whole batch. New failures appearing after preview are not silently included.
Event IDs, payloads and lifetime attempts are preserved. A reviewed batch is a
starter application bound, not a platform quota. Repeat preview for another batch
only after the previous operation is confirmed. Receiver deduplication remains
required for every replay.

Pause/resume and batch replay use one immutable request key and payload. **Resolve
pending control operation** first reads the original actor's exact receipt, so a
lost response can be confirmed even after that actor loses ownership. If no receipt
is found, only the original subject still holding owner access may explicitly
retry the original request. A missing receipt alone does not prove rollback.
Keep the page open until an uncertain operation is resolved; pending state is
not stored in browser storage. The controller never issues a new key or broadens
a reviewed batch during recovery. A receipt confirms its historical operation;
refresh health to see current settings.

**Load operations** and **Next operations** show the verified actor, action,
server time and request ID for pause, resume, batch and individual replay.
Pagination uses operation UUIDs, not chronological ordering. Only current owners
can browse this history; former actors can read only their own exact-key receipt.
Direct HTTP settings changes or operation edits/deletion are denied. History and
receipts need owner retention/privacy maintenance and are not protected from the
privileged database owner. There is no automatic cleanup or retention window.

Local SQL/browser acceptance covers active-lease completion during pause,
revision races, stale-batch rollback, original-actor recovery after demotion and
confirmed browser operations. The required saved collection stays within the
existing 100-request bound; repeated denial variants remain in the SQL suite.

## Named webhook subscriptions

Owners can create named integrations in the dashboard, select submitted events,
fulfilled events, or both, and enable or pause each subscription independently.
Migration `0008_named_webhook_subscriptions.sql` fans out each matching transition
atomically with the order write. Each destination has its own queue, attempt budget,
leases, dead letters, health RPC and owner replay control. Metadata saves use revision
checks and immutable operation receipts; resolve a lost response before another edit.
The dashboard paginates selected deliveries, attempt history and subscription
changes in pages of 20. Named replay is individual; the existing batch
preview and batch replay controls apply to the legacy team queue.

With no named subscriptions, the original team queue continues to work. Once a team
has named subscriptions, only enabled matching subscriptions receive future events;
there is no legacy fallback. Disabled subscriptions stop future enqueueing, while
paused subscriptions accumulate deliveries and stop new claims. Team pause overrides
all subscription queues. Existing leases may finish while paused. Editing filters,
enabled state or configuration never rewrites queued deliveries.

Configure one worker invocation per subscription. Its trusted JWT must include
`outbox_worker: true`, `outbox_team` and `outbox_subscription` (the lowercase subscription
UUID); these must come from the verified issuer. Set `DATA_API_URL`,
`OUTBOX_WORKER_TOKEN`, `OUTBOX_TEAM`, `OUTBOX_NAMESPACE`, `OUTBOX_SUBSCRIPTION_ID`, and
`OUTBOX_CONFIGS_JSON`. For example, the latter maps an operator-approved reference to
private destination configuration:

```json
{"accounting-v1":{"url":"https://receiver.example/events","secret":"<64 hexadecimal characters>"}}
```

URLs and signing keys stay in worker configuration, never in database metadata or
browser inputs. Each queue row pins its subscription version and configuration
reference. Treat each reference's URL and key binding as immutable: rotate by adding
a new reference and updating the subscription, retaining old bindings until their
queued deliveries drain. Missing bindings fail closed and consume the normal retry
budget; the worker never falls back to a newer destination. Keep the namespace stable
for receiver deduplication. Named signed payloads include `subscription_id` and
`subscription_version`; signatures cover the raw body, and receivers deduplicate
namespace plus delivery ID. Fan-out deliveries have distinct IDs per destination.

Run `npm run outbox:once` under your scheduler with these environment settings.
Production destinations require HTTPS; explicit loopback HTTP remains available for
local checks. This starter supplies customer-owned SQL and a worker, not a hosted
scheduler or a managed webhook service.


## Delivery diagnostics and sample tests

Migration `0009_webhook_diagnostics.sql` adds owner-approved sample tests and safe
failure categories for named deliveries. Owners select a saved subscription, approve
contacting its receiver, and choose **Queue test delivery**. A test emits a signed
`webhook.test` event with `test: true` and sample data:

```json
{"message":"Gregale webhook test","requested_by":"alice"}
```

No real order or order event is created or modified. The queue uses synthetic order
correlation fields internally; these are omitted from sample payloads. Receivers must
verify the raw-body signature, recognize the signed type and test flag, acknowledge
the sample, and avoid applying order actions. The same namespace/delivery-ID
idempotency rules apply, since tests may retry.

An explicit test can be queued while new order deliveries are disabled. Subscription
and team pause still block new claims. Queueing is not a synchronous HTTP probe:
your scheduler must run the worker. Each request pins the reviewed subscription
revision, version and configuration reference. Later edits cannot redirect a queued
test. Concurrent retries with the same request key produce one immutable receipt;
only one pending or leased sample per subscription is accepted. A different request
is refused until the existing sample is delivered or settled as dead. Resolve a lost
response before another test. The original actor can read that exact receipt after
losing ownership, but cannot queue a new test without current owner access.

**Attempt history** shows start/finish times, HTTP status, outcome and a curated
failure explanation. Selected deliveries show the next eligible retry time; scheduling
and pause may delay the actual attempt. `configuration_missing` means the worker lacks
the pinned reference, `network_error` means no HTTP response was obtained, and
`http_error` means the receiver returned an unsuccessful response. Provider bodies,
raw exceptions, URLs, keys and worker tokens are not persisted. Older attempts can
have no failure category.

A successful attempt confirms HTTP acceptance by that pinned destination at that
time; it does not establish the readiness of a later configuration version or verify
receiver-side business processing. No attempt means no evidence yet: check scheduling
and pause before assuming the endpoint is ready. Owners can also inspect immutable
subscription change history, including the actor, filters, enabled/pause state,
configuration reference and version. Members cannot inspect these operational views.

The named acknowledgement RPC adds an optional `failure_reason`; existing callers
that omit it continue to work. Migration 0009 replaces its SQL signature, so operators
with manually maintained grants must grant the new seven-argument function to the
restricted Data API login. The local runtime applies annotated-RPC grants automatically.
Local tests cover sample signing, unchanged orders, tenant/subscription authorization,
missing bindings, pinned edits, pause, concurrent idempotency and browser recovery.

## Personal saved views and bulk fulfilment

Migration `0010_saved_views_bulk_fulfilment.sql` adds reusable order views and reviewed
bulk fulfilment. Every member, including read-only members, can save personal views
within a team. Views retain a name, optional status and whole-cent total range, and
sorting by order ID or total in either direction. Other team members cannot read or
change your views. Updates require the current revision; lost saves resolve through
an immutable receipt with the original actor and request key.

Load **My saved views** and choose **Apply / edit view** to apply its criteria. Save
current filters to update that view, or choose **New saved view** to create another.
Views are paginated; sorted order pages use a UUID tie breaker, so equal totals do not
skip rows between pages. Filters describe a live query rather than a frozen dataset.
CSV export applies the same status/range/sort criteria, retaining the existing
10,000-row bound and live-scan semantics. Changes to data between reads may move
orders across pages; reload before selecting
an operational batch. Changing filters or pages clears the current bulk selection.

Owners and admins can select submitted orders on the current page and choose
**Preview selected orders**. The preview shows current versions, totals, eligibility
and conflicts. Approve the reviewed selection to fulfil it. Each batch contains
1–20 distinct order IDs and reviewed versions; this is a customer-owned workflow
bound, not a platform plan quota. Bulk submission and transitions other than
fulfilment are not part of this milestone.

The fulfilment RPC checks writer membership after the team lock and applies the
selection atomically under read-committed isolation. Every order must still have the
reviewed version and submitted state. A stale, missing, inaccessible or no-longer-
submitted order rejects the whole batch. Nothing from the attempted batch remains:
order updates, workflow events, webhook fan-out and the receipt all roll back. Totals
and creators remain unchanged. The normal subscription filters, immutable destination
references and delivery pause controls continue to apply.

Each actor/team operation key identifies one canonical selection. Retrying that key
returns the original immutable result rather than advancing orders again; changing
its payload is refused. Keep the page open while an operation is uncertain and choose
**Resolve pending batch**. The original actor can read the exact committed receipt
after losing writer access, but an absent receipt can only be retried by that actor
with current writer membership. A fresh preview is required after a known conflict;
the UI never silently substitutes newer versions or applies only eligible rows.

Team members can inspect paginated batch history, including actor, operation ID,
reviewed versions and fulfilled order snapshots. Direct REST writes to views,
receipts and batch-operation tables are denied. The typed helpers live in
`client/src/order-operations.ts`; the generated schema exposes `list_view_orders`,
`save_order_view_once`, `preview_team_order_fulfilment`, `fulfil_team_orders_once` and
exact receipt-recovery RPCs. All SQL remains owned by the customer starter.

Local acceptance covers personal-view isolation, filter/sort pagination with tied
totals, stale-view revision races, preview conflicts, all-or-nothing rollback,
concurrent idempotency, atomic webhook fan-out, authorization after queued revocation,
recovery after demotion/removal and Chromium dashboard flows.

## Shared team views

Migration `0011_shared_team_views.sql` adds published views alongside personal views.
Owners and admins can publish current status/total filters and sorting, then update a
selected team view using its current revision. Every current team member can list,
apply and inspect published views. Applying a shared view uses the same order query,
CSV export and reviewed bulk selection controls; it does not grant writer access.

Choose **Load team views**, then **Apply / select team view** to use its saved criteria.
The selected name/version identifies the publication being used. Working-filter
changes remain local until a writer chooses **Update selected team view**; choose
**New team view** to publish a separate definition. Selecting a shared view clears
the personal-view editor and current bulk selection so a later personal save creates
a separate view. Metadata lists and publication history are paginated in pages of 20.
The history shows the actor, version, status/range and sorting for each publication.

Every member may name and create an independent personal copy of the selected shared
revision. Copying checks that revision under the membership lock and creates the
personal view and immutable copy receipt atomically. A stale source is refused;
reload and select the current publication before copying. A duplicate personal name
or existing destination ID is refused without overwriting preferences. Subsequent
shared edits do not change personal copies. Copying uses the saved shared criteria,
not unsaved working-filter edits.

Publishing and copying use actor/team request keys. Retry with the same original key
returns the immutable result, even if the source has since changed. Keep the page
open while an operation is uncertain and choose **Resolve pending team-view operation**.
The original actor can read that exact committed receipt after losing writer access
or team membership. An absent receipt can only be retried by that actor with current
membership; publishing additionally requires current owner/admin access. A serialized
revision refusal releases a pending operation that did not commit.

Publication history is visible to current members, but copy names, personal snapshots
and copy receipts are private to the copying actor, including from team publishers.
Direct writes to shared definitions and operation history are denied by RLS; role
changes are rechecked after acquiring the same team lock used for administration.
The typed helpers are in `client/src/shared-views.ts`, with generated RPCs
`save_shared_order_view_once`, `copy_shared_order_view_once` and
`get_shared_order_view_receipt`. Copying reuses the personal-view save RPC, so
manually managed restricted-login grants must also permit `save_order_view_once`.
The local runtime grants annotated functions automatically.

Shared views are live-query presets, not order snapshots or access policies. Reload
shared metadata to pick up a newer publication. Bulk fulfilment still reviews exact
order versions and commits through its existing atomic workflow. Local SQL/client/
Chromium acceptance covers publish/apply/copy, stale revision races, unchanged orders,
private copy visibility, copy independence, authorization after queued revocation,
and lost-response recovery after access loss.
