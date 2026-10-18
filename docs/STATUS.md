# Status

This file is the long-form engineering record: it preserves milestone history
and tracks the acceptance evidence that is still open. The README intentionally
does not mirror this list; customer-facing maturity belongs in the generated
[capability matrix](capabilities.md). Update the current snapshot below when
release or acceptance state changes, and keep older milestone notes clearly
historical.

## Zero-downtime daemon restarts — 2026-10-17 (branch, not released)

gatewayd-internal (unix socket and compute data listener), realtimed and the
control plane's schedd can restart behind systemd sockets that keep their
listeners open, as apid and gatewayd-public already did (ADR-068). schedd's
socket follows its configured address (`faas_schedd_listen_addr`, from the
manifest's schedd bind); the compute sockets stay off
(`faas_compute_durable_listeners`) until node_join manages them. Shutdown ends
SSE and WebSocket sessions at once instead of holding the restart for the
25 s grace, gRPC servers end watch streams so GracefulStop finishes, the
public-to-internal H2C hop resends requests that crossed a graceful GOAWAY,
wake RPCs wait out a schedd restart, outboundd and bridged drain before
exiting, and HTTP daemons answer requests on connections accepted just before
shutdown instead of closing them
([ADR-1736](adr/1736-zero-downtime-daemon-restarts.md)). Portable
restart-under-load tests show no failed requests. On fsn-4 (systemd 255),
gatewayd-internal's synth server behind a socket unit and a TCP schedd
listener behind a socket unit each restarted five times under load with 0 of
138,179 and 0 of 127,987 requests failing; without the socket unit the gateway
returned 103,323 502s. Open: the first socket conversion through `node_join`
and deployctl, and the listeners the ADR leaves without socket activation.

## Product Control customer work history — 2026-10-17

[ADR-1727](adr/1727-product-control-customer-work-history.md) adds a private
customer API and explicit portal search over independently retained receipts.
Exact run reference, original feature, outcome and inclusive UTC start dates
combine with AND. Removed features and old plans stay discoverable. Pages use
immutable admission time and UUID descending at microsecond precision; scope,
Auth realm, role, effective permissions and all filters bind continuation.
Missing and foreign exact references return the same empty scoped page.

Both stores recheck current customer authority and View work. Billing-only and
plan-manager roles cannot read history; pausing starts preserves authorized
reads. PostgreSQL shares a read-only repeatable-read authorization/source
snapshot; memory returns detached entries with bounded collection space. Limits
are 25 entries, 2048 cursor bytes, 32 KiB JSON and a 20-second service read budget.
Source scan cost still depends on retained history. There is no total count,
new retention window, audit journal or snapshot across pages; outcome changes
can enter/leave a filtered search and require a fresh first page.

The shared Run details projection preserves settled outcomes after result expiry,
keeps active Operations retention, and labels unavailable unresolved work without
inventing failure. Entries contain no input/result bodies, prices or charges,
allowance counts, execution diagnostics or owner billing evidence. View run
details links to the independently permitted result/files, billing month and help.

The portal adds next/first page and clear-filter actions, with no polling or
search persistence. Editing filters, switching workspaces, permission changes,
sign-out and access denial clear history and selected results/details. Request
and workspace generations fence late responses and stale buttons. Timestamp
parsing accepts the memory store's nanoseconds and PostgreSQL's microseconds.

All 313 selected Go checks, 106 portal checks and 10 Chromium checks passed,
including race-enabled domain/customer service and memory/PostgreSQL contracts.
The full Product Control HTTP race suite passed after the final asset change.
Chromium used the production router/customer middleware and SQLC/PostgreSQL with
fictional Auth and state-only work, covering 28 tied-timestamp receipts, removed
features, original plans, result expiry, filters, actions, access changes,
workspace/sign-out clearing, default-off routes and desktop/mobile layout.
Scoped changed-file lint, SQLC consistency, private spec/SDK coverage, repository
policy, documentation paths and formatting passed. The disposable browser fixture
stopped and its database was removed. Prior verification artifacts are preserved.

The Product Control and independent email switches remain default off. No schema
migration, worker, CLI/SDK implementation, email transport, real provider/Auth
qualification, workload execution or deployment is introduced. See the
[work-history runbook](ops/product-control.md#customer-work-history).

## Product Control customer run details — 2026-10-17

[ADR-1726](adr/1726-product-control-customer-run-details.md) adds a private,
read-only customer API and Run details panel to Recent work and work activity
links. The original receipt supplies the plan, admission month, one consumed
start and optional historical allowance before/after admission. Current plan
changes do not rewrite those terms. Older missing allowance is explicit, settled
outcomes survive result expiry, and expired unresolved work establishes neither
failure nor a zero charge. Active work follows existing Operations retention.

Live Application Auth membership and current account/app/tenant/realm mapping
remain mandatory. Both stores recheck current role permissions at the receipt
boundary. View work is required; billing-only and plan-manager roles cannot read
runs. The complete charge object is omitted without View billing. Billing members
see provisional/finalized success charges, waivers, minimal statement references
and signed credit aggregates for the exact run. Partial/full credits reduce the
net amount. No statement or credit claims payment or a cash refund. Source
selection excludes payloads, execution diagnostics, owner reasons and handoffs.

PostgreSQL shares read-only repeatable-read authorization and source queries;
memory returns detached values. JSON is limited to 16 KiB and the service read
budget is 20 seconds. No migration, worker, journal or extra polling is added.
The portal provides explicit result/files, original billing-month and contextual
help actions. Selection, closing details, workspace/role changes, sign-out and
access denials fence stale responses and old buttons and clear prior results.
The panel is not persisted and shows its observation time with explicit refresh.

All 297 selected Go checks passed, including race-enabled domain/customer service
and memory/PostgreSQL contracts. The full Product Control HTTP race suite passed.
All 96 portal checks and 10 Chromium checks passed. Chromium exercised the
production router, customer middleware and SQLC/PostgreSQL with fictional Auth
and state-only work fixtures. It covered original Starter terms under a current
Pro plan, signed partial credits, result expiry, source links, role changes,
foreign workspace reads, sign-out, default-off routes and desktop/mobile layouts.
The local fixture stopped and its disposable database was removed. SQLC
consistency, private-route spec/SDK coverage, schema-source binding, documentation
paths, formatting and repository policy checks passed. Scoped lint, including tests and router wiring, passed with zero introduced
issues. This check covers every line in every changed Go file; it does not claim
a clean repository-wide lint.

The default-off Product Control and independent email switches remain unchanged.
Migrations, schema, Auth protocols/templates, CLI/SDKs, email transport and
VM/scheduler source and earlier verification artifacts are preserved. No real
credentials, email, workload execution, deployment, flag enablement or live
provider/Auth/native qualification is claimed. See the
[run-details runbook](ops/product-control.md#customer-run-details).

## Product Control customer workspace activity — 2026-10-17

[ADR-1725](adr/1725-product-control-workspace-activity.md) adds customer workspace
activity to the existing Updates area. The read-only private API and portal join
retained work outcomes, confirmed self-service plans, finalized statements,
signed credit notes and the latest help updates. Source fields are selected
explicitly; owner notes, reviews, assignment evidence, handoffs, work prices and
billing snapshots are absent. No migration, journal or worker is added.

Live Application Auth membership and current account/product/tenant/realm mapping
remain mandatory. Both stores recheck current permissions before pagination.
Work, plan, billing and help each retain their existing access rules; work-only
members receive no billing amounts or plan confirmations. Plan/help history also
checks its recorded organization. Cursors bind workspace, Auth app, organization,
role, effective permissions, filter and the full descending time/kind/UUID key.
Pages have 25 entries, 64 KiB responses and a 20-second service deadline. The
SQLC query shares a read-only repeatable-read snapshot with authorization; memory
collects at most a page plus lookahead. Source scan cost still depends on retained
history. Signed credits remain negative, matching the original documents.

Links open existing independently authorized readers: a run/result, exact plan
confirmation, original billing month or help request. Looking at a previous plan
confirmation preserves an unconfirmed new review. Browser ordering retains all
six microsecond digits. Filters survive ordinary workspace refreshes; pending
responses and old buttons are fenced across selection, sign-out and workspace
or permission changes. The activity list is not persisted and adds no polling
loop. The unread inbox and personal email consent remain separate. Available
history is not a complete audit log; results may expire and help updates coalesce.

All 304 selected Go checks passed, including race-enabled domain/customer service
and memory/PostgreSQL contracts. The full Product Control HTTP race suite passed.
All 82 portal checks and 10 Chromium checks passed. Chromium uses the production
router, customer middleware and SQLC/PostgreSQL projection with fictional Auth
and state-only workload fixtures. Its isolated Go overlay supplies equivalents
of existing test accessors without changing repository source. It covers source
links, pending reviews, signed credits, role visibility, cursor scope, workspace
switching, sign-out, default-off routes and desktop/mobile layouts. Browser checks
sent no external requests or email and executed no real workload. The fixture
stopped cleanly and its disposable database was removed.

SQLC consistency, private-route spec/SDK coverage, schema-source binding,
formatting, documentation paths and repository policy checks passed. Scoped changed-file lint, including tests and router wiring, passed with zero
introduced issues. The initial broad run exceeded its budget and reported twelve
context warnings in unchanged legacy test helpers; the verification report records
that baseline rather than claiming a repository-wide clean lint. The default-off Product Control flag and independent email
switches remain unchanged. Existing migrations, schema, Auth templates/protocols,
SDKs, outbox and VM/scheduler source, and earlier verification artifacts are
preserved. No deployment, real credentials or live provider/Auth/native workload
qualification is claimed. See the
[activity runbook](ops/product-control.md#customer-workspace-activity).

## Product Control notification unsubscribe — 2026-10-17

[ADR-1724](adr/1724-product-control-notification-unsubscribe.md) adds unsubscribe
without sign-in to new personal work/help notification emails. Each retained
delivery has a separate random capability, persisted only as a lookup hash and a
delivery-bound age envelope. Retries reuse the original capability and message
body. Text/HTML links use a URL fragment; GET and HEAD show confirmation without
reading or changing preferences. An explicit browser POST or standard mailbox
one-click POST performs the same scoped action. Both supported mailbox form
encodings are bounded and validated; cookies and Auth availability are unnecessary.

Unsubscribe records one immutable confirmation per original consent version,
turns both categories off, clears recipient authority and cancels queued mail.
Leased work stops at the final readiness check. Requests already in flight may
finish and retain their provider receipts. Bounce/complaint holds and recipient
generations remain intact. Even the preference-version ceiling cannot prevent
stopping mail. Repeats recover the same acknowledgement; old confirmed links
report their historical result after later consent, while stale unconfirmed links
cannot change newer choices. Re-enabling requires authenticated explicit consent
and enables only future updates.

The portal, owner console/private API and `gregale product email opt-outs` expose
scoped historical receipts separately from current preferences. Public
acknowledgements reveal only confirmation ID/time and whether that confirmation
still matches current choices. Authenticated history exposes safe references,
versions and generations without addresses, Auth actors, capabilities or message
content. Pages have 25 entries and scoped time/UUID cursors. Receipts cascade with
the original delivery's 30-day window and existing delivery cap; there is no new
receipt quota that could refuse an opt-out. The append-only migration adds checked
capability fields and receipt storage. Earlier migrations, Auth templates, SDKs
and VM/scheduler code remain unchanged.

All 3,008 unique selected Go checks passed, including 147 domain/outbox race
checks, 184 CLI/completion checks, the full API client suite, private-route and
schema checks. The two optional Fish/PowerShell syntax checks were skipped
because those shells are unavailable. The full Product Control router race suite
passed, including owner scope/session/MFA and all-method default-off routes.
All 73 portal checks, nine confirmation-script checks and 11 Chromium checks
passed. Browser checks use production customer/owner views and sender code with
fictional Auth, a loopback provider and signed callbacks; they cover lost
acknowledgements, same-tab fragment links, newer consent, queue cancellation,
mailbox POSTs, complaint holds, sign-out and desktop/mobile layouts. A delayed
response for an older link cannot replace a newer confirmation screen.

Fresh schema generation/source binding, SQLC consistency, private-route SDK
coverage, scoped lint with tests (zero issues), formatting and repository policy
checks passed. The verification packet retains the source manifest, review files,
logs and browser evidence. The disposable schema database and browser fixture
were cleaned up. Product Control remains default off; email defaults disabled
and real sending keeps its independent switch. No deployment, real credentials
or external email was used. Real mailbox behavior, DKIM header coverage and proxy
query redaction remain deployment qualification. See the
[unsubscribe runbook](ops/product-control.md#unsubscribing-from-notification-email).

## Product Control notification recovery — 2026-10-17

[ADR-1723](adr/1723-product-control-notification-recovery.md) adds an explicit
customer action to recover a personal hard-bounce pause after verifying a
different sign-in address and renewing consent. Sender evidence is an encrypted,
subscription-bound address digest; plaintext addresses and digests are omitted
from platform records and public projections. Existing Auth protocols and
migrations are unchanged. Historical pauses without evidence, inaccessible
identity keys and complaint holds stay under review.

An immutable scoped receipt records the confirmation, selected categories,
preference versions and recipient generations. Concurrent unchanged requests
converge on one confirmation. Recovery resets eligibility to future updates;
cancelled messages, original deadlines and attempt budgets remain intact. Old
recipient bounces cannot pause the replacement or cancel its queue. Current
recipient bounces pause normally, and complaints from any retained generation
still establish a hold. Replaying an old receipt cannot clear a new hold.

Preference saves and recovery serialize per subscription before Auth grant
rotation. PostgreSQL consent waiters release their connection between attempts;
the holder uses a bounded request budget. Callbacks retain a separate short lock
and can still prevent recovery during external verification. Owners inspect
scoped confirmations in the console/API and `gregale product email recoveries`;
there is no owner resume action. Customer history is personal. Pages are bounded
to 25 records with scoped cursors; retention is 30 days and recovery capacity is
50 per subscription in that window. The append-only platform migration adds
checked generations, encrypted evidence and confirmation receipts.

All 2,974 unique selected Go checks passed, including 127 domain/outbox race
checks, 183 CLI/completion checks, the full API client suite, private-route
compliance and schema-source binding. The two optional Fish/PowerShell syntax
checks were skipped because those shells are unavailable. The full Product
Control router race suite passed, including owner scope/session/MFA checks and
all-method disabled recovery routes. All 71 portal checks and eight Chromium
checks passed with same-address rejection, unchanged-request response recovery,
consent serialization, old/new recipient feedback and complaint holds. Chromium
used production customer/owner views, fictional Auth and a signed loopback
provider on desktop and mobile. Fresh schema generation, SQLC consistency, SDK
coverage, scoped lint with tests (zero issues) and repository policy checks
passed. The source-bound verification packet retains the logs, review source
and desktop/mobile browser evidence for this increment.

Product Control remains default off; email defaults disabled and real sending
keeps its independent switch. No deployment, real credentials or external email
was used. Live Auth/address verification and provider qualification remain
separate. See the [notification recovery runbook](ops/product-control.md#recovering-notifications-after-a-hard-bounce).

## Product Control provider delivery feedback — 2026-10-17

[ADR-1722](adr/1722-product-control-email-provider-feedback.md) adds signed Resend
delivery, bounce and complaint reports above durable provider receipts. The
portal, owner console and `gregale product email` show provider-reported states
and report times. Receiving-server delivery does not establish inbox placement
or reading. Semantic duplicates and out-of-order reports preserve one message,
its attempt budget and complaint/bounce precedence. A bounded, expiring metadata
buffer handles reports that arrive before the send receipt commits; matching
checks expiry even when cleanup has a backlog. Previously accepted messages
without provider IDs cannot acquire feedback retroactively.

Permanent bounces and complaints pause the affected personal product/workspace
subscription. Queued messages cancel and leased messages stop at the next
readiness check; in-flight provider calls can complete. Other subscriptions
remain independent. Preference saves, grant renewals, address changes and owner
retries cannot clear the pause. This increment has no resume or force-send
action. Temporary and unclassified bounces are visible without an automatic
pause. The append-only migration adds checked pause/report fields, unique
provider receipts and the bounded pending table; prior migrations are preserved.

All 3,095 unique selected Go checks passed, including 249 domain/outbox/mail
race checks, memory and real PostgreSQL feedback contracts, 182 CLI/completion
checks, the API client suite, private-route compliance and schema-source binding.
Two optional Fish/PowerShell syntax checks were skipped because those shells
are unavailable. The full Product Control and existing platform-mail HTTP race
suites passed. All 68 portal checks and eight Chromium checks passed across
production customer/owner views on desktop and mobile, using fictional Auth and
a signed loopback provider. Fresh migration/schema generation, SQLC consistency,
SDK coverage, scoped lint with tests (zero issues), documentation links and
repository policy checks passed.

An initial API contract compile was killed when overlapping builds reached the
workspace memory limit. Lint was stopped to release memory, and the contract
suite and lint passed after rerunning with bounded memory and build concurrency.
The initial resource failure and final source-bound verification evidence are
retained with this increment.

Product Control remains default off, email defaults disabled and real sending
still needs its separate switch. The optional feedback secret neither enables
sending nor shares the global platform-mail suppression path. No deployment,
real credential setup, external email or live provider qualification was
performed. See the [feedback runbook](ops/product-control.md#provider-delivery-feedback-and-paused-subscriptions).

## Product Control owner notification controls — 2026-10-17

[ADR-1721](adr/1721-product-control-owner-email-controls.md) adds owner console
and `gregale product email` controls for whole-window queue totals, oldest
pending age, customer-scoped delivery history and clear failure guidance.
Counts cover the selected rolling 30-day product/customer window across all
statuses; filtered 25-message pages use scoped keyset cursors. Safe projections
omit addresses, Auth identities, recipient grants and private message bodies.

Owners can requeue eligible provider-rejected failures against their observed
failed attempt. Recovery preserves the delivery/provider key, original deadline,
preference/source revisions and six-attempt budget. It rechecks current Auth
authority, mapping, role/product permissions and source before queueing; the
worker repeats those checks before sending. Transient outages retain automatic
backoff and Retry-After. Atomic retry receipts make concurrent clicks and lost
responses safe, including after a later attempt completes. The append-only
platform migration adds bounded retry receipts and an owner scope index.

All 2,953 unique selected Go checks passed, including 108 domain/outbox race
checks, PostgreSQL and memory recovery contracts, 181 CLI/completion checks,
the complete API client suite, shared Product Control validation, private route
compliance and schema-source binding. Two optional Fish/PowerShell completion
syntax checks were skipped because those shells are not installed. The full
Product Control HTTP race suite passed with owner scope, session, MFA, verified
email, CSRF and all-method disabled-route checks. Chromium passed eight
desktop/mobile checks using production owner service/templates, fictional Auth
and a private local sink. Fresh migration/schema generation, SQLC consistency,
SDK coverage, scoped lint with tests and repository policy checks passed.

Everything remains behind the default-off Product Control flag; email still
defaults disabled and real sending requires its separate switch. No deployment,
real credential setup or external email was performed. Existing Auth protocols,
issued migrations, pilot evidence, work execution and commercial behavior are
preserved. Real provider acceptance/inbox delivery qualification remains later
internal release work and does not block implementation.

See the [owner email runbook](ops/product-control.md#owner-notification-email-controls).

## Product Control personal notification email — 2026-10-17

[ADR-1720](adr/1720-product-control-personal-notification-email.md) adds personal
Work outcomes and Help replies opt-ins above the workspace inbox, with a durable
outbox, bounded retries, delivery status and a private local mail sink. Preferences
start off, use version checks and apply only to new updates. Auth issues a narrow
30-day recipient grant for the current verified address; the platform encrypts
it and saves no customer login token or plaintext recipient address. Each send
rechecks live membership/role, owned realm, mapping, current product permissions,
source revision, preferences and lease. Messages omit help text and run payloads.

All 2,921 unique selected Go checks passed, including 87 domain/outbox race
checks, the full API client suite, shared Product Control validation, CLI/Auth
runtime contracts, private route compliance and schema-source binding. The full
Product Control HTTP race suite also passed. PostgreSQL verified outbox CAS,
leases and send-time mapping checks; 79 Auth runtime tests passed, including live
SQL grant checks for changed/unverified addresses, bans, membership removal and
expiry. Chromium passed nine desktop/mobile checks through production portal and
service code with fictional Auth responses and local capture. Scoped lint with
tests, fresh migration/schema generation, SQLC consistency and repository policy
checks passed. Earlier pilot and recovery evidence remains frozen.

The Product Control flag stays default off. Email separately defaults disabled;
local mode records captured messages, and real Resend mode requires an explicit
send switch. Provider acceptance never claims inbox delivery. No deployment,
real provider call or external email was performed. Deployed Auth rollout and
real provider qualification remain later internal release work; credentials are
not a prerequisite for continuing implementation.

See the [personal email runbook](ops/product-control.md#personal-notification-email).

## Product Control customer account recovery — 2026-10-17

[ADR-1719](adr/1719-product-control-customer-account-recovery.md) adds **Forgot
password?** to the customer sign-in page. It opens the latest product's owned
Application Auth recovery UI in a separate tab, without passing credentials or
requesting email on navigation. The existing Auth flow owns recovery mail,
one-time links, password policy and session behavior. Customers return to the
product afterward and sign in normally. Caller targets/queries are rejected;
an explicit empty redirect fragment prevents fragment inheritance.

All 841 selected API/domain/CLI/private-contract checks passed, along with the
full Product Control HTTP race suite, scoped lint with tests and repository
policy checks. Local Chromium exercised nine checks using the production
recovery handler and portal/Auth UI assets with fictional Auth API responses:
separate-tab privacy, explicit recovery/reset actions, missing tokens, fragment
discarding, realm changes, mobile layout and disabled methods.

Implementation continues while deployed pilot access is unavailable. The new
entry point remains behind the default-off Product Control flag. Actual recovery
delivery and deployed reset/session behavior remain separate live checks; no
external messages, deployments or flag changes were performed. No schema,
commercial receipt, Operations lifecycle or Auth implementation changed.

See the [account recovery runbook](ops/product-control.md#customer-account-recovery).

## Product Control complete internal pilot preparation — 2026-10-17

[ADR-1718](adr/1718-product-control-complete-internal-pilot.md) adds an opt-in
version-two manifest requiring three baseline downloads, three journeys linked
to help about those same runs, and observed customer inbox pages. Both the
selected successful work and latest help source must match retained evidence.
Missing stages or older sources leave the journey incomplete; malformed or
foreign pages are rejected. Version-one ledger workflows remain compatible.
Reports retain only artifact paths/hashes/byte counts and keep live browser,
read recovery and native qualification unverified.

All 249 selected Product Control domain/CLI and help-dispatch checks passed,
including the read-only HTTP verifier. The
[pilot guide](../examples/customer-operation-export/product-control/README.md#customer-updates-inbox-pilot)
includes inbox sharing, newer-source unread, stale updates, lost acknowledgements
and current authority. The verified inbox runtime retains its earlier build/source
evidence; updated pilot tooling is captured separately. No runtime API, schema,
portal or default-off gate changes are part of this preparation.

The live three-customer trial remains pending a selected internal deployment,
access profile and qualified native Operations host. No live scenarios or actual
downloads have been recorded during preparation.

## Product Control customer notifications — 2026-10-17

[ADR-1717](adr/1717-product-control-customer-notifications.md) adds an Updates
inbox for retained work outcomes, runs needing attention and the latest owner
help response/status. It uses a rolling thirty-day view with current workspace
and kind-specific permissions applied before pages and unread counts. Read
status is explicitly shared within the workspace, scoped to its Auth identity;
newer source versions become unread and stale acknowledgements conflict.

Local qualification passed all 103 Product Control memory/PostgreSQL cases,
134 API/domain/service checks, 68 portal/recovery contracts, the HTTP race suite
and nine migration regressions including the full apply/ledger walk. Chromium
checked current source kinds, explicit read choices, lost-response recovery,
workspace clearing, escaped text and mobile layout. Both binaries built; scoped
Go lint, SQLC and private API contract checks passed. A local disk-exhaustion
failure during the final storage attempt was resolved by reclaiming completed
derived caches; the full rerun passed without prerequisite skips.

The customer portal offers read/unread controls, filtering, pagination and links
to existing help/run readers. Lost acknowledgements refresh through GET without
POST replay. Read metadata has bounded capacity and expiry; inbox actions cannot
execute work or change access, plans or billing. The existing default-off flag
covers the routes and asset. Email delivery and the deployed three-customer
Auth/browser/native-export pilot remain separate release work.

## Product Control internal release candidate — 2026-10-17

[ADR-1716](adr/1716-product-control-internal-pilot-evidence.md) extends
`gregale product pilot verify` with optional scoped help expectations and
three-customer journey records. Help snapshots must match reviewed hashes and,
for run requests, the original admission terms. `product pilot help` inspects
hashes without printing messages/responses. Journey records report observed time
to first success, blocked steps and elapsed time to the latest help resolution.
Local artifacts are bounded and hashed; contents are excluded from reports.
Incomplete journeys and evidence mismatches have distinct nonzero exits.

All 221 Product Control/API/CLI and selected help checks passed. The earlier
migration-scoping failure is corrected without changing issued SQL: an already
unqualified migration must retain its bytes, and PostgreSQL integration checks
its actual function placement. The full migration apply/ledger walk passed.
Broader database tests completed with existing prerequisite skips, recorded in
the verification report rather than treated as executed acceptance.

The service remains internal behind the default-off flag. Operator-recorded
journeys retain unverified qualification; the deployed three-customer browser,
Auth and native-export trial still needs a selected internal target and access.
The [pilot guide](../examples/customer-operation-export/product-control/README.md#customer-help-and-complete-customer-journeys)
now includes help creation/responses, interrupted acknowledgements, permissions,
workspace changes and friction recording. No deployment or flag change occurs
during preparation.

## Product Control Customer Help — 2026-10-17

[ADR-1715](adr/1715-product-control-customer-help.md) adds customer requests with
server-generated diagnostic snapshots, an owner review queue and customer-visible
status/response. Requests follow current workspace, role and Auth realm authority.
Feature/run requests retain only permitted context; messages and responses are
shared with authorized members of that workspace. Exact request retries preserve
the initial snapshot; version checks prevent competing owner edits from losing
updates. Help does not execute work or change permissions, plans or billing.

All 87 Product Control memory/PostgreSQL cases passed without skips, alongside
the HTTP race suite and 58 portal/recovery contracts. Chromium checked explicit
submission, customer-visible responses, lost-acknowledgement recovery, workspace
clearing, escaped text and mobile layouts. Both binaries built; SQLC, private API
contract checks and scoped Go lint passed. Database package tests and the full
migration apply/ledger walk passed against a clean disposable local database.

The portal and owner APIs/pages remain internal behind the existing default-off
flag. At this milestone, the historical migration scoping assertion failed for the unchanged
`20261003160458628_application_standard_runtime_default_base.sql`; its source
and scoping helper/test matched the preceding milestone. The release candidate
recorded above corrects that assertion. The deployed pilot remains
separate. See the [Customer Help runbook](ops/product-control.md#customer-help).

## Local Product Control customer usage guidance — 2026-10-16

[ADR-1714](adr/1714-product-control-customer-usage-guidance.md) adds customer
usage progress, warnings from 80%, exact UTC resets and separate explanations
for exhausted/zero allowances, paused access, permissions and unavailable
features. The portal and private customer read API share existing accepted-start
counts across plans/revisions. Authorized customers can open explicitly offered
plan choices through the existing review and confirmation flow. No new storage
schema, counters, automatic plan changes or work submissions are introduced.

API/domain/service checks and the complete Product Control HTTP suite with race
detection passed. All 49 portal/recovery contract cases passed. Local Chromium
exercised six guidance states, authorized read-only plan review, escaped long
feature labels, logout clearing and desktop/mobile widths without page overflow.
SQLC generation, internal-route spec/SDK checks and repository policy gates
passed. This milestone changes no storage implementation; the preceding
milestone's memory/PostgreSQL storage evidence remains historical.

The service remains internal behind its existing default-off flag. The live
Auth/email/browser/native-export pilot still needs a selected deployment and
access. See the [usage guidance runbook](ops/product-control.md#usage-and-next-steps).

## Local Product Control customer activity timeline — 2026-10-16

[ADR-1713](adr/1713-product-control-customer-activity.md) adds an owner-only
activity timeline in the console, private read API, and `gregale product customers
activity`. Ten event kinds combine retained setup/activation, reviewed assignment
and self-service plan changes, original run terms and verified outcomes,
waivers, billing documents and invoice handoffs. Scoped cursors preserve
simultaneous events; each page is a fresh observation, bounded at fifty events,
3 MiB and twenty seconds. No new journal, observation writes or Auth authority
is introduced. Legacy direct assignments and Auth team events are not inferred.

All 71 Product Control memory/PostgreSQL storage cases passed without skips,
along with API/domain/service and CLI help/completion checks. The complete Product
Control HTTP suite passed with the race detector, including disabled-method
fallback protection. Both binaries built. Chromium checked all ten rendered event
kinds, expandable details, source-text escaping and desktop/mobile widths using
fictional data. These are local checks with simulated Auth/execution boundaries.

The service stays internal and off by default. Local verification is separate
from the live internal Auth/email/browser/native-export pilot, which still needs
an approved target and access. The [runbook](ops/product-control.md#customer-activity-timeline)
documents history coverage, source links and continuation semantics.

## Local Product Control customer readiness overview — 2026-10-16

[ADR-1712](adr/1712-product-control-customer-readiness-overview.md) adds an
owner-only **Customer readiness and activation** page, private read API, and
`gregale product customers overview`. Assigned customers combine pinned plans,
existing feature/binding/allowance diagnostics, guided activation evidence, and
work needing review across all retained months. Pages share a twenty-second
deadline, contain at most ten customers and 3 MiB of JSON, and link to existing
review flows. No new activation, membership, execution or billing writes occur.

The 69 Product Control memory/PostgreSQL storage test cases passed without skips;
Product Control API/domain/CLI checks and the complete HTTP boundary suite with
the race detector passed. Chromium checked the rendered overview's five customer
states, expandable checks, and desktop/mobile widths. These are local checks,
with simulated Auth/execution at the HTTP boundary, not a deployed pilot.

Product Control remains internal behind its existing default-off flag. The live
internal Auth/email/browser/native-export pilot still needs a deployment target
and access; this overview grants no native qualification and enables no flag.
The [runbook](ops/product-control.md#customer-readiness-and-activation-overview)
describes the observation and continuation contracts.

## Durable migration source retirement — 2026-10-17

[ADR-1530](adr/1530-durable-migration-source-retirement.md) binds production
sources to their original native generation before capture. Ownership movement
atomically records pending source retirement. Source RAM/CPU/vCPU remains charged
until physical cleanup is proven, and source records/leases block maintenance,
retirement and deletion. Retained receipts fence delayed capture, late resume,
and instance-name reuse across daemon replacement.

Shared memory/PostgreSQL contracts, ordinary and protected capacity, atomic
ownership/retirement, source drain races, direct-writer guards and deferred/
suppressed-write failures pass. Full scheduler/RPC regressions, portable native
fixtures, targeted races, SQL generation, schema restore/down/up parity and
scoped lint are verified. Failed preparation cannot erase uncertain binding
commits or pin an unstarted lease; parked rollback never resumes an unaccounted VM.

Release preparation also verifies replay of all nine recovery migrations against
existing DDL. Replay preserves current operator claims and the newer source
guards when an earlier incoming migration is replayed alone. The canonical schema
and its migration-source checksum are regenerated from fresh PostgreSQL 16.

Dedicated x86_64 KVM migration/process-death and leak qualification remains
pending: this environment has no `/dev/kvm`, and leakcheck cannot inspect its
kernel loop devices. The broader portable VM suite excludes the same 21 existing
filesystem/source-mode/readiness failure families documented under ADR-1529.
Nothing deployed. See the [source retirement guide](ops/migration-source-retirement.md).

## Durable incoming migration reservations — 2026-10-17

[ADR-1529](adr/1529-durable-inbound-migration-reservations.md) persists destination
generation, resource bounds, both wakes and source-lease authority before adoption.
Pending reservations consume capacity and block drain completion/retirement.
One dispatch grant, retained response/cleanup receipts and an fsynced native
generation binding fence delayed creation and late ownership commit. Source resume,
lease deletion/replacement and the migration watchdog retain unresolved attempts.
Ownership conversion preserves admitted resident resource floors atomically.

Shared memory/PostgreSQL phases, replay, RAM/CPU/vCPU contention, source fencing,
drain races, commit/cleanup races (including direct writers), suppressed writes and
deferred commit failures pass. Full scheduler and vmmd RPC regressions, manager
tests, targeted memory/scheduler/manager/RPC races, SQLC/protobuf regeneration,
restored-schema parity, scoped production/test lint and schedd/vmmd link checks pass.

The broader VM suite has 21 existing failure families involving filesystem,
source-mode and readiness expectations; all reproduce with the incoming journal
hooks removed. The portable VM suite excluding those failures passes. Dedicated
x86_64 KVM migration/process-death and leak qualification remains pending; this
environment has no `/dev/kvm`. Nothing deployed. Upgrade dispatchers/receivers
together, preserve journals and inspect historical handoffs; see the
[migration recovery guide](ops/inbound-migration-reservations.md), including
conservative process-ledger recovery after a failed handoff.

## Node admission generation fencing — 2026-10-17

[ADR-1528](adr/1528-node-admission-generation-fencing.md) carries placement's
selected lifecycle generation into instance and migration ownership transactions.
New residency requires an admitting node at that generation. A database guard
also protects direct writers, while existing residents can finish publication and
cleanup. Definite stale placements trigger bounded fresh selection; failed
attempts release restore pressure and job leases/ledger reservations. Migration
uses its existing commit resolver and adopted-VM cleanup after a refusal.

Memory/PostgreSQL admission, lifecycle-cycle, job/migration rollback, lock-order
and both admission/drain race orderings pass. The full scheduler suite, targeted
memory/scheduler/event/statetest race checks, scoped production/test lint, SQLC
diff and restored-schema parity pass; schedd links. Nothing deployed. Coordinate
the dispatcher upgrade and inspect historical closed-node inventory;
see the [admission rollout guide](ops/node-admission-fencing.md). Native migration
and evacuation qualification remains a separate fleet acceptance gate.

## Generation-fenced node completion — 2026-10-17

[ADR-1527](adr/1527-generation-fenced-node-completion.md) binds recovery-controller
completion to the lifecycle generation captured before its sweep. The final work
check, lifecycle transition, immutable cycle receipt and audit handoff share one
transaction. Delayed workers cannot complete a later cycle with the same lifecycle;
retries preserve the original result after later cycles, node deletion and outbox
retention. Warm and frozen resident rows keep drains pending.

Targeted memory/PostgreSQL generation, precondition, replay, concurrency and fault
injection suites pass, including suppressed writes, deferred commit failure and
capacity lock ordering. Scheduler/heartbeat/operator recovery and event/statetest
regressions, memory-store and runner race detection, scoped production/test lint,
SQLC compile/diff and restored-schema parity pass. Nothing deployed. Stop old
recovery controllers before migration; see the
[node completion guide](ops/node-lifecycle-completion.md). Native migration and
evacuation qualification remains a separate fleet acceptance gate.

## Atomic operator node maintenance — 2026-10-17

[ADR-1526](adr/1526-atomic-operator-node-maintenance.md) makes node lifecycle
transition, execution receipt, intent result and outcome audit handoff atomic.
Claims fence obsolete workers and terminal replay preserves original outcomes
after later maintenance cycles and outbox pruning. RecoveryRunner retains
migration/evacuation ownership. Coordinated upgrade quarantines ambiguous legacy
node actions; receipts and unresolved evidence block downgrade.

Memory/PostgreSQL recovery, concurrency and fault injection, scheduler integration
and recovery regressions, memory race detection, scoped production/test lint,
SQLC and restored-schema parity pass. Nothing deployed. Stop old schedd writers
before migration and reconcile quarantined legacy requests before another action;
see the [node maintenance guide](ops/operator-node-maintenance.md).

## Operator recovery submission receipts — 2026-10-17

[ADR-1524](adr/1524-durable-operator-recovery-submissions.md) atomically accepts
restart, park and cold-boot intents with their original 202 response and request
audit handoff. Actor-scoped keys have no receipt TTL; replays precede mutable
target checks, changed parameters return 409, and every replay runs current
operator authentication. gregalectl exposes reusable --request-key and an
instances wait --intent-id command that only polls.

Targeted memory/PostgreSQL concurrency and fault-injection, HTTP authentication
and replay, CLI and memory race tests pass. SQLC/schema/spec validation, scoped
lint, SDK generation/request smoke checks, Node SDK build and API/CLI link
builds pass. No deployment has been performed. Apply the migration and drain
old API writers before rollout; uncertain pre-upgrade requests need
reconciliation. See the [submission upgrade guide](ops/operator-recovery-submissions.md).
This change adds no VM lifecycle behavior; native qualification of the earlier
restart/cold-boot execution changes remains pending below.

## Operator cold-boot recovery — 2026-10-17

[ADR-1523](adr/1523-durable-operator-cold-boot-recovery.md) makes cold-boot
invalidation, the deployment recovery generation, intent completion and audit
handoff one transaction. Boot lineage fences delayed captures, and warm/frozen
promotion cannot bypass recovery. Original results survive worker replacement,
lost acknowledgements and outbox pruning. Memory/PostgreSQL recovery and failure
injection, scheduler/publisher/RPC and HTTP regressions, memory race checks,
SQLC/OpenAPI/schema validation, scoped lint and daemon/CLI builds pass.
Native KVM lifecycle and leak qualification remains pending; no deployment has
been performed. Migration and coordinated schedd/imaged rollout are required:
[cold-boot recovery guide](ops/operator-cold-boot-recovery.md).

## Operator restart execution recovery — 2026-10-17

[ADR-1522](adr/1522-recoverable-operator-restart-execution.md) adds an immutable
restart plan, per-claim ownership, a generation-bound vmmd teardown protocol and
an independent durable acknowledgement. Confirmed teardown, STOPPED, operator
completion and its audit handoff commit together; interrupted execution without
proof is exposed as `outcome_unknown`. Late acknowledgements allow database-only
reconciliation against the original target. Legacy unary restart propagates
terminal-state write errors. See the [recovery and rollout guide](ops/operator-restart-recovery.md).

Targeted memory/PostgreSQL, scheduler/manager/RPC, API/CLI and memory race suites
pass. SQL/protobuf regeneration, restored-schema parity and changed-code lint
pass; schedd, vmmd and gregalectl link successfully. State tests and their lint
run as file suites to avoid unrelated state-test compiler memory pressure.
The new metal test compiles and skips without staged VM images. Native
`test-metal` and `leakcheck` qualification remain pending: this environment has
neither `/dev/kvm` nor the acceptance images. Nothing deployed.

## Read-only all-binding verification preview — 2026-10-16

`gregale bindings verify APP --all --dry-run` shares preparation and selection
with batch execution. Its separate preview report describes planned operations,
consent flags, exclusions, prospective coverage, and structured blockers.
`plan_ready` and `complete_coverage_possible` express metadata readiness and
potential coverage, never a passed verification gate. Both queue and storage
consent flags remain inert during preview. Strict mode evaluates whether the
plan could cover every eligible binding; default partial-coverage behavior is
preserved. See [preview before execution](bindings-verify-all-queues.md#preview-before-execution).

## Writable storage in all-binding verification — 2026-10-16

`gregale bindings verify APP --all --deployment ID|vN --object-storage-write`
uses existing disposable-object canaries for writable bindings and read-only
probes for read-only bindings. Upload, download where granted, and cleanup
evidence must match the requested prefix and mode. The combined report and
strict gate reject failed operations, failed cleanup, incompatible guest
reports, and mismatched deployment/scope receipts. Storage write consent remains
separate from queue publication; omitted consent preserves existing behavior.
See [writable storage](bindings-verify-all-queues.md#verify-writable-object-storage).

## Strict all-binding verification coverage — 2026-10-16

`gregale bindings verify APP --all --require-complete` adds a CI gate to the
existing batch verifier. Its JSON `gate` and human output explain failed probes,
unsupported or unchecked eligible bindings, incomplete inventory, and empty
eligible selections. Disabled, out-of-scope, and release-only bindings remain
explicit exclusions. Default verification semantics, explicit write/publication
consent, and interruption exit 130 are preserved. See
[strict coverage](bindings-verify-all-queues.md#require-complete-coverage-in-ci).

## Queue canaries in all-binding verification — 2026-10-16

`gregale bindings verify APP --all --deployment ID|vN --queue-publish`
includes eligible push queues in the existing combined report using the existing
completion-canary runner. Publication is opt-in and deployment-pinned; receipts
must match the selected scope, binding, and deployment and confirm handler
completion. Missing consent or capability remains explicit partial coverage;
disabled and out-of-scope bindings are skipped. Interrupted and unsuccessful
canaries use existing cancellation cleanup. See
[all-binding queue verification](bindings-verify-all-queues.md).

## Service authorization explanation — 2026-10-16

`gregale bindings service explain CALLER TARGET --method GET --path /path`
reads app-level policies and reports allowed, denied, or indeterminate with
structured rules, reasons, and settings to review. It uses the shared scope
normalizer and gateway matcher, preserves caller-policy intersections, and
makes no application requests. Previews, unavailable apps, and unsupported
policy contracts remain indeterminate; runtime and environment resolution are
outside this snapshot explanation. See
[service binding configuration](service-binding-configuration.md).

## Service policy export and drift checks — 2026-10-16

`gregale bindings service export` produces reusable six-field policy JSON in a
new private file or on stdout. `service check` compares normalized live values
against supplied fields without mutation, retaining null versus deny-all caller
semantics. CI exits distinguish match (0), drift (1), and errors (2). Checks are
snapshots; reviewed plans remain the guarded write path. See
[service binding configuration](service-binding-configuration.md).

## Reviewed service binding plans — 2026-10-16

[ADR-1386](adr/1386-reviewed-service-binding-plans.md) adds private saved plans
with app/account/endpoint identity and service-settings fingerprints. Apply
checks its precondition under the app lock in both ordinary and activity-aware
store transactions. Stale plans conflict before mutation, and unrelated manifest
edits survive guarded updates. Direct configuration retains its existing
unguarded behavior. See the [workflow](service-binding-configuration.md#save-and-apply-a-reviewed-plan).

## Service binding reliability and transport CLI — 2026-10-16

[ADR-1384](adr/1384-service-binding-reliability-cli.md) extends the service policy
file with dependency timeouts/retries and canonical URL transport. Validation
uses the resulting target list before mutation; previews and saved receipts
expose implicit reliability pruning when a target is removed. Omitted settings
remain partial updates, non-idempotent retries require explicit per-target
opt-in, and URL selection does not certify running-guest adoption.

## Service binding CLI configuration — 2026-10-16

[ADR-1383](adr/1383-service-binding-cli-configuration.md) adds policy-file
configuration for standalone service targets, declared-only authorization,
caller restrictions and method/path scopes. Read-only previews preserve the
API's omitted/null/empty distinctions; apply uses one partial app update and
reports saved values. See the [configuration guide](service-binding-configuration.md).
Runtime adoption and connectivity still require inspection and verification.

## Queue canary CLI setup — 2026-10-16

[ADR-1382](adr/1382-queue-canary-cli-deployment-intent.md) adds bounded revision
pin lifetime updates and repeatable per-deployment queue canary declarations.
Source archive validation precedes deployment mutations, previews expose the
selected bindings, and redeployments require fresh opt-in. The queue worker
quickstart now uses the CLI throughout; message publication retains separate
explicit consent. Native acceptance qualification remains pending below.

## Queue worker completion acceptance — 2026-10-15

[ADR-1381](adr/1381-queue-worker-completion-acceptance.md) gives the shipped
queue worker a safe reserved canary path with exact deployment opt-in and
publication consent. The [native queue journey](ops/queue-bindings-native-acceptance.md)
requires nine phases covering real parked delivery, exact handler completion,
scheduler restart, durable receipt/message recovery, cancellation, expiry,
completed-work replay rejection and a zero-traffic candidate's release fence.
Fresh completion on the old serving revision cannot qualify the candidate.
Independent invocation counts, source/build/runtime provenance, published Node
base hashes, registered cleanup and host leak checks constrain the report.
Portable starter/source/artifact and PostgreSQL-backed authority race checks,
CLI quickstart materialization, API consent/receipt-recovery regressions,
13 queue runner/verdict regressions, the existing
49 PostgreSQL/storage runner/verdict regressions, real fragmented Go JSON parsing,
Python lint and combined native-tag compilation passed. Tagged Go lint has no
new findings; the same 18 existing issues remain. This workspace has no `/dev/kvm`;
supported native execution and host leak qualification remain pending. No
provisioning or rollout gate is enabled.

## Python PostgreSQL native acceptance — 2026-10-15

[ADR-1380](adr/1380-python-postgres-native-acceptance.md) extends the
[PostgreSQL native journey](ops/managed-postgres-native-acceptance.md) to the
unchanged Python starter. Nineteen phases now require the probe and both shipped
starters, private-CA trust, release-only migration credentials, disconnected-pool
restore, failed-hook cold fallback, running/parked rotation and scheduler recovery.
Starter reads require current credential/version/process adoption ACKs, and
independent SQL checks preserve the shared Node/Python notes dataset. Version-3
reports/manifests require separate Python source and deployed app-layer hashes;
old or partial Node-only evidence must be regenerated. Portable Go race checks,
the exact materialized Python lifecycle/SQL recovery suite, real pinned-driver
TLS success and negative trust cases, 23 PostgreSQL verdict regressions, 26 storage
runner/verdict regressions, rollout/CLI checks, real fragmented Go JSON parsing,
canonical starter drift, Python lint and combined native-tag compilation passed.
Tagged Go lint has no new findings; the same 18 existing issues remain. This
workspace has no `/dev/kvm`; supported native execution and host leak qualification
remain pending. No provisioning or rollout gate is enabled.

## PostgreSQL native acceptance repair — 2026-10-15

[ADR-1379](adr/1379-postgres-native-acceptance-repair.md) repairs the existing
thirteen-phase [PostgreSQL native harness](ops/managed-postgres-native-acceptance.md):
the fixture CA is packaged under the shipped runtime COPY, provenance identifies
actual OCI app layers/source archives and the minimal base, and the verdict
handles bounded fragmented Go cleanup logs. Version-2 guest reports/manifests
replace the inapplicable function-runtime base identities; old guest evidence
cannot satisfy combined readiness. Portable source/provenance and cross-language
Go race tests, 21 PostgreSQL verdict regressions, the existing 26 storage
runner/verdict tests, rollout/CLI checks, real Go JSON cleanup-log parsing,
Python lint and combined native-tag compilation passed. Tagged Go lint has no
new findings; 18 existing issues remain. This workspace has no `/dev/kvm`; supported native execution and
host leak qualification remain pending. No provisioning or rollout gate is enabled.

## Native storage starter runtime matrix — 2026-10-15

[ADR-1378](adr/1378-native-storage-starter-runtime-matrix.md) extends the
[deployed storage acceptance journey](ops/object-storage-native-acceptance.md) to
both unchanged Node and Python starters. Each runs eight phases with isolated
fixtures, strict TLS trust and its own source/build/app-layer evidence. The
Dockerfile starters are full OCI apps; this replaces the earlier harness's
inapplicable function-runtime release lookup with actual deployed artifacts. Version-2
reports require both cleanup identities, the same revision and shared artifact
hashes; Node-only or reused Node evidence cannot qualify Python. Portable Go race
checks, 26 storage runner/verdict tests, real Go JSON cleanup-log parsing,
Python lint and combined native-tag compilation passed. Changed-file Go lint is
clean; full tagged lint retains 18 unrelated existing issues. This workspace has no `/dev/kvm`;
supported native execution and host leak qualification remain pending for both
starters. No feature or rollout gate is enabled.

## Python storage explicit TLS trust — 2026-10-15

[ADR-1377](adr/1377-python-storage-explicit-tls-trust.md) makes the Python storage
starter honor its explicit `AWS_CA_BUNDLE` while preserving certificate-chain and
hostname verification. Default public CA trust remains enabled; invalid bundles
and certificates fail adoption/readiness. New transports reread the configured
file on reload and restore. The exact materialized starter passed all 35 portable
tests, including signed HTTPS operations, negative trust cases, the real handler
process and corrected-bundle recovery. Fresh dependency isolation, canonical drift,
Python/Go lint and packaging checks passed. Live-provider and native VM execution
remain separate qualification work.

## Object-storage starter native acceptance harness — 2026-10-15

[ADR-1376](adr/1376-native-object-storage-starter-acceptance.md) adds a strict
[deployed storage acceptance journey](ops/object-storage-native-acceptance.md)
for the unchanged Node starter, public credential rotation, real snapshot/restore,
failed-hook cold fallback, independent byte/replay checks and credential cleanup.
Portable race, runner/verdict checks and tagged compilation passed; changed-file
lint is clean. These checks are separate from native execution. This
implementation workspace has no `/dev/kvm`; supported native
execution and host leak qualification remain pending. No feature or rollout gate
is enabled. Python guest and live-provider qualification remain separate.

## Containers: image runs, process identity and Compose build context — 2026-10-15

- **Image runs.** [ADR-1161](adr/1161-image-runs.md) `gregale run --image` (#4663),
  [ADR-1163](adr/1163-live-job-task-output.md) live output (#4694, edge routing
  fixed in #4725) and [ADR-1330](adr/1330-interactive-image-runs.md)
  `gregale run -it --image` (#4734) are merged.
  - They stay operator-gated and off by default: `FAAS_IMAGE_RUNS`
    (Ansible `faas_image_runs_enabled`) and `FAAS_INTERACTIVE_IMAGE_RUNS`.
  - Unit, conformance and gateway tests pass. The metal tests
    `TestImageRunsMetal`, `TestJobTaskLiveOutputMetal` and
    `TestInteractiveImageRunMetal` are in the native `jobs` phase but have not
    run. The `e2e-native` dispatch on 2026-10-14 failed because the acceptance
    VM `faas-acceptance-1` no longer exists.
- **Process identity.** [ADR-1239](adr/1239-docker-compatible-image-process-identity.md)
  (#4704) keeps root-owned image files owned by root, runs images without
  `USER` as root, and gives an unknown numeric UID GID 0, as Docker does.
  - Unit tests pass.
  - Native evidence must run imaged as its production service user. That
    evidence is in open #4722, which also lets workloads serve port 80.
- **Compose build context.** [ADR-1288](adr/1288-compose-declared-build-context.md)
  (#4719): project apply stages a Compose-declared build service with its
  declared context as the archive root. The release-gate e2e test pins both
  that shape and the whole-repository shape.
- **No native run recorded.** The `containers` native lane has no recorded
  passing run; [container qualification](container-qualification.md) remains
  the coverage plan.
- No deployment setting changed. `faas_image_runs_enabled` defaults to
  `false`.

## Billing integrity: delivery finality, hard spend cap, plan history — 2026-10-13

[ADR-1162](adr/1162-billing-delivery-finality-spend-cap-and-plan-history.md) closes four
billing gaps. The provider pusher delivers an hour only once every minute up to its end
is recorded compute-complete or older than the catch-up bound, so late catch-up rows
can no longer land in a delivered hour. The spend cap bills exactly up to the cap,
settles capped hours without later back-billing, and schedd parks capped accounts:
explicit-deployment admissions check the cap, the reaper parks via `appMustPark`, warm
pools are reclaimed and queued job tasks deferred. App purge keeps `usage_minutes` and
`usage_daily`. The month's allowance follows recorded plan activations (largest grant
so far) in the pusher, `CurrentMonthOverageCents` and `OverageMBSecondsForRange`.
`pkg/meter`, `pkg/billing`, `pkg/billing/reconciler` and `pkg/api` tests pass locally
(one known timing-flaky loop health test aside), as do the new `pkg/state` plan-history
tests, the MemStore conformance suite and the full `pkg/sched` suite including the new
spend-cap tests. PG-backed tests, `make lint` and a live Polar sandbox delivery remain
unrun.
No deployment setting or provider configuration changed.

## Combined PostgreSQL rollout readiness — 2026-10-12

[ADR-992](adr/992-managed-postgres-combined-rollout-readiness.md) combines provider
and durable lifecycle evidence with a separately approved native guest bundle.
The offline verifier requires the same clean source revision, SQL major, current
artifact hashes and unexpired evidence. Staging canaries can opt into enforcement;
partial configuration cannot fall back to legacy approval. Portable race, verdict,
lint and tagged compilation checks passed; [evidence and limits](ops/evidence/20261012-managed-postgres-rollout-readiness/REPORT.md)
record the qualification boundary. Supported native execution remains pending; this change
does not enable production provisioning or alter Neon resources or billing.

## PostgreSQL starter checkpoint drain — 2026-10-17

The Node and Python PostgreSQL starters' first capture happened right after
readiness, so every wake restored the startup check's open SQL session and
orphaned its server backend. Both adapters now drain every session in a
`before_checkpoint` hook (idempotent, retried after a failed drain, rotation
refused until restore) next to the existing `after_restore` replacement.
`secret-reload-node` bundled the restore code but never called it; it now
serves both hooks. Declaring the checkpoint hook turns off warm captures for
those apps (ADR-351). Unit tests and the TLS/SCRAM suites (Node and Python
starters, concurrent functions) check that no server session survives the
drain. Native VM capture evidence is pending.

## Concurrent backend cancellation — 2026-10-16

The follow-up to PR #4768 holds shared-worker slots through cancellation
cleanup. Node receives an abort signal, Python acknowledges completed cleanup,
and function hook handlers receive socket-derived lifecycle context (ADR-1540).
Portable interpreter/race tests and real TLS/SCRAM PostgreSQL integration check
bounded admission, blocked-write rollback, healthy neighbours, pool replacement
and worker-death recovery. [Portable evidence](ops/evidence/20261016-function-backend-concurrency/REPORT.md)
and the [acceptance procedure](ops/function-backend-concurrency-acceptance.md)
separate these process checks from native VM qualification. Native execution is
pending: this workspace lacks KVM. Function hooks execute in one worker; this
does not qualify VM-wide pool draining or durable post-response `waitUntil`.

## Deployed PostgreSQL starter qualification — 2026-10-12

The [native acceptance gate](ops/managed-postgres-native-acceptance.md) now requires
thirteen phases, including the shipped Node starter, real public API rotation,
restored pools and failed-hook cold fallback. The portable public API and restricted
TLS/SCRAM SQL tests passed locally; [evidence and limits](ops/evidence/20261012-managed-postgres-deployed-starter/REPORT.md)
record the narrowed API build and outstanding guest execution. The internal GCE
node failed preflight with existing jail/mount leaks and provides nested diagnostics
only. Supported native KVM qualification remains pending; provisioning stays disabled.

## Local built validators and project publication — 2026-10-10

[ADR-948](adr/948-built-validator-artifacts-and-project-publication.md) adds explicit
source-bundle packaging and automatic publication from verified source builds before
cache/VM work. Gated environment promotion/clone paths carry immutable validator
bindings to copied deployments; graph publication and rollback recheck members.
Deployment detail reports bounded validator availability/digest observations, with
updated SDK models. Shared storage remains default off. Direct image deployments
retain the explicit publisher; this is not a universal SQL publication fence.
Source cases are written; tests, builds, unit/env checks and native/GCS acceptance
remain unrun. No PR, live artifacts, unit installation or deployment settings changed.

## Local shared durable entity validator artifacts — 2026-10-09

[ADR-947](adr/947-shared-durable-entity-validator-artifacts.md) adds private immutable
object-storage content and deployment bindings, a release publisher hook, restart-free
API lookup, and shared readiness checks before schedd prime, imaged live publication
and covered API traffic/rollback paths. Existing serving wakes remain independent of
artifact reads. Shared storage is default off; source cases are written. Tests, builds,
daemon contract checks and native/GCS acceptance remain unrun. Publication coverage is
per path, not a universal SQL fence; separate project/environment publication paths
remain to qualify. No PR, live artifact upload or deployment settings changed.

## Local durable entity validator release tooling — 2026-10-09

[ADR-946](adr/946-durable-entity-validator-release-tooling.md) adds a shared registry
contract, offline packaging/merge command with automatic digests and an optional
APID validator-availability gate for explicit promotion, traffic redistribution and
promote/advance recovery. Registry staging and API restarts remain operator release
steps; initial builder publication and autonomous scheduler/recovery paths are outside
this gate. Source cases are written; tests, builds and native/live qualification are
unrun. No PR, deployed registry or deployment changes.

## Local durable entity operation visibility — 2026-10-09

[ADR-945](adr/945-durable-entity-operation-observability.md) adds bounded storage and
engine latency metrics, restore/validation/export outcomes, an operator dashboard
and a [qualification handoff](runbooks/FaasDurableEntityQualification.md). Existing
restore audit records now include the validator bundle digest. This does not add
an entity history API or qualify durability. Source cases are written; tests,
builds, Grafana/PromQL checks and native/private-bucket acceptance remain unrun.
[ADR-944](adr/944-durable-entity-isolated-restore-validation.md)'s separate validator
bundle path remains local, unqualified and default off. No PR or deployment.

## Local application-validated durable entity restore — 2026-10-09

[ADR-943](adr/943-durable-entity-application-validated-restore.md) adds default-off
application restore validation, a separate verdict-only guest route and owner
validation endpoint. Enabled restores require an explicit deployment pin and
revalidate under the private claim after receipt/version checks. Rejection prevents
publication; replay skips validation; takeover still fences commit. Go/Node guest
helpers, Go/Node/Python owner clients and source cases are written. Purity is an
application contract; deployment selection and bucket commit are not atomic.
Tests/builds/provider qualification remain delegated and unrun. No PR or deployment.

## Local durable entity scheduled backups and preview — 2026-10-09

[ADR-942](adr/942-durable-entity-scheduled-backups-and-restore-preview.md) adds a
default-off hourly application backup worker, bounded eventual seven-day pruning,
owner backup list/read endpoints and read-only restore preview. Go/Node/Python
clients are updated. Schema compatibility remains unverified; preview grants no
restore authority. Orphaned backups require operator lifecycle/account-erasure
cleanup. Cases and [operator guidance](runbooks/FaasDurableEntityBackups.md) are
written; tests/builds/provider checks remain delegated and unrun. No PR or
production enablement.

## Local durable entity owner export/restore API — 2026-10-09

[ADR-941](adr/941-durable-entity-owner-state-recovery-api.md) wires owner-scoped
export and fenced restore endpoints, sensitive no-store responses, strict bounded
requests and best-effort metadata audit events. Go/Node/Python clients are updated.
Restore acquires only existing committed entities. Source qualification cases are
written; tests, builds and provider checks remain delegated and unrun. No PR or
production enablement.

## Local durable entity state export/restore engine — 2026-10-09

[ADR-940](adr/940-durable-entity-state-export-restore.md) adds read-only data
exports and private-claim fenced restores with an expected business version and
stable request receipt. Restore preserves current alarms, outgoing work and retry
metadata. Internal engine methods only; customer API/SDK exposure remains to be
implemented. Source cases are written; tests and builds are delegated and unrun.
No PR, deployment or production enablement.

## Local durable entity application schema upgrades — 2026-10-09

[ADR-939](adr/939-durable-entity-application-schema-migrations.md) adds opt-in
Go/Node schema-aware guest calls. Pure sequential upgrades produce local state
and persist only with the normal fenced transition. Future schemas, missing
steps and invalid outputs fail closed; legacy adoption is explicit. Versioned
readers/writers must be deployed before newer envelopes are written because
unaware legacy application code is not fenced by these SDK helpers. Examples
and cases are written; tests, builds and native/provider qualification remain
pending. Work is local with no PR or feature-gate change.

## Local typed durable entity SDK — 2026-10-09

[ADR-938](adr/938-typed-durable-entity-sdk.md) adds Go/Node scope-bound entity
handles and typed pure guest calls/transition builders. Handles preserve
selectors and require explicit replay identity; result decode errors retain
acknowledgement metadata. Guest builders preserve existing alarms by default,
allow explicit schedule/clear, and reject partial publication after invalid
webhook intents. A typed counter/reminder example and SDK cases are written.
No server or feature-gate change is introduced. Work remains local with no PR;
tests, builds and native/provider qualification remain pending.

## Local durable entity operational health — 2026-10-09

[ADR-937](adr/937-durable-entity-operational-health.md) adds a bounded read-only
health worker, sampled backlog/exhaustion observations, recovery outcomes and
freshness-aware alert rules. New outgoing messages retain an engine-generated
timestamp; legacy message age remains explicitly unknown. Only fully successful
rotations refresh gauges. The worker defaults off and requires explicit preview
and app enablement. Metrics contain no entity identifiers and are not exact fleet
inventory. Test cases and the [operator runbook](runbooks/FaasDurableEntityHealth.md)
are written; tests, rules and native/provider qualification remain unverified.
Work remains local with no PR.

## Local durable entity recovery — 2026-10-09

[ADR-936](adr/936-durable-entity-exhausted-work-recovery.md) adds an owner-only
retry API and Go/Node/Python SDK methods for exhausted alarms and outbox heads.
Fresh inspection version/revision and exact target checks fence stale requests
and later retry cycles at the same business version. Recovery changes only
retry metadata on an existing unowned entity; guest execution and transport
acceptance remain with existing workers. Successful acknowledged recovery emits
an audit event through the existing best-effort path. Tests and native/provider
qualification remain pending with the testing agent. Work is local with no PR;
feature defaults remain disabled.

## Local durable entity inspection — 2026-10-09

[ADR-935](adr/935-durable-entity-owner-inspection.md) adds account-scoped,
metadata-only inspection of committed version, alarms and pending outbox work,
plus a separate scoped head-delivery observation. Go, Node and Python clients
include the read API. Inspection performs no ownership acquisition or guest
execution. Missing delivery history means unknown; an empty outbox does not
prove receiver completion. The reservation example now matches initial engine
state `{}`. Work is local with no PR; tests and native/provider acceptance are
pending with the testing agent. Existing feature gates remain disabled.

## Local runtime update work — 2026-10-06

[ADR-693](adr/693-private-runtime-routing-and-health-verification.md) adds private
gateway cache-installation receipts, bounded lost-notification/restart repair,
and account-scoped post-cutover verification using fresh candidate-only health
and request evidence. Activation history stays immutable. Gateway confirmation
is disabled by default; customer Apply remains unavailable. The reviewed gateway
session set is explicit, and verification is a current observation rather than
a durable completion or drain receipt. Authoritative gateway membership,
durable verification orchestration, predecessor drain/cleanup and dedicated
native Linux amd64 KVM acceptance remain open. Local fixtures provide no native
runtime qualification; the dedicated acceptance project is suspended.

## Current snapshot — 2026-09-16

- **Product surface:** the `gregale` CLI, API, GitHub integration, dashboard,
  framework apps, functions, async workloads, edge protocols, and managed data
  paths are implemented in the current tree. Entitlement and launch status are
  generated in [`docs/capabilities.md`](capabilities.md), not inferred from
  milestone labels below.
- **Deployment:** supported acceptance runs on native x86_64 Linux KVM hosts.
  The split-box manifest and role-aware Ansible bootstrap are the supported
  production path; nested virtualization and the retired local harness are not
  acceptance environments.
- **Current engineering focus:** M9 multi-box recovery and snapshot fan-out,
  durable release/rollback safety, debugger and execution observability, and
  the remaining operator recovery drills. The open evidence list is maintained
  in [What's next](#whats-next).
- **Recently merged platform work:** release-safe rollback and snapshot
  transitions, safe deployment rollout preflight details and CLI diff output,
  debugger regression/root-cause synthesis with target-mirror replay,
  durable project-environment promotions, streamed disposable executions,
  durable wake lifecycle and suspension handling, and customer cache and
  error-budget metrics. Managed PostgreSQL dependency binding is available as
  a gated operator preview. These landed after the older milestone prose below
  and should not be inferred from its historical PR list.

Durable entity guest outbox work (2026-10-09, local verification pending): a
separate default-off handler gate selects protocol v2 behind the relay gate.
V2 handlers can return bounded registered app-webhook intents with state,
result and alarm; apid checks current scope/destination admission before engine
publication. V1 remains unchanged. Go/Node SDK helpers validate pure transitions,
and a reservation/confirmation example bundles the Node helpers. Added cases
have not been run; the testing agent must qualify relay and guest behavior before
enablement. No PR or rollout was performed
([ADR-934](adr/934-durable-entity-guest-outbox-protocol.md)).

Durable entity outbox relay work (2026-10-09, local verification pending): an
opt-in apid worker reserves FIFO heads in object storage, deduplicates acceptance
into the existing webhook ledger with receipts that outlive delivery history,
and acknowledges through a fenced snapshot commit. Bounded retry/backoff,
exhausted-head inspection/operator replay and indexed discovery with independent
repair scans are implemented. Manifest writers now use schema 6; stop older
writers and apply the transport-acceptance migration before enabling the relay.
The flag remains disabled and guest protocol v1 still rejects outgoing intents;
the separately gated v2 draft above adds handler exposure.
Automated checks and native/live-GCS qualification remain with the testing agent
([ADR-933](adr/933-durable-entity-outbox-relay.md)).

Durable entity outbox commit update (2026-10-09): the private state engine can commit
bounded outgoing webhook intents with state and request receipts through one
manifest CAS. Pending work survives replay, restart, takeover and cleanup and
counts toward snapshot/storage caps. Its schema-5 upgrade is superseded by the
schema-6 relay writer above. Guest protocol v1 continues to reject outgoing work.
Native-host and live-bucket
qualification remain pending ([ADR-903](adr/903-object-storage-entity-outbox-contract.md)).

Durable entity alarm update (2026-10-08): the opt-in object-storage preview adds
an advisory time-ordered alarm index with bounded repair scans, fenced retry
reservations with backoff and exhaustion, and exact-scope private operator
inspection. Its schema-4 upgrade is superseded by the schema-6 writer above.
Native-host and live-provider qualification remain pending; this does
not promote the preview to production availability
([ADR-712](adr/712-object-storage-durable-entities.md)).

Image readiness update (2026-10-07): newly assembled primary image deployments
require a fresh successful effective OCI/Compose command healthcheck before
serving readiness, in addition to existing readiness and route verification.
Cold boot, restore, warm-pool resume and migration preserve that requirement;
older daemon/guest responses cannot acknowledge it. Existing releases need
reassembly or redeployment to gain the gate. Native boot/restore and leak
qualification remain pending ([ADR-683](adr/683-image-healthcheck-readiness.md)).

Image runtime recovery update (2026-10-07): required primary image command
checks now continue throughout serving VM lifetimes. Repeated fresh failures
use scheduler-owned cold recovery and existing restart limits; missing proof
uses infrastructure recovery without charging app exhaustion. Old daemons and
guests cannot silently omit monitoring. Native lifecycle and leak qualification
remain pending ([ADR-684](adr/684-image-healthcheck-runtime-recovery.md)).

Compose dependency rollout update (2026-10-07): `service_healthy` now holds an
initial project release until its captured dependency deployment is live with
traffic in the same project/environment. Durable waits survive restart; a failed
dependency or expired deadline fails the candidate while retaining the previous
serving release. CLI and dashboard stage summaries show the blocker
([ADR-685](adr/685-compose-dependency-release-gates.md)).

Managed PostgreSQL cutover safety update (2026-10-02): vmmd retains live and
failed-boot ownership through confirmed teardown and serializes concurrent stops
([ADR-469](adr/469-managed-postgres-confirmed-teardown.md)). Scheduler fault,
operator restart and pressure-recycling paths now retain their admission and
resident state through failed Destroy, including schedd restart reconstruction
([ADR-470](adr/470-managed-postgres-scheduler-teardown-accounting.md)). Liveness
and workload OOM reports now persist on vmmd and retry until schedd confirms
application; replay with unknown guest ownership stays pending
([ADR-471](adr/471-managed-postgres-failure-report-redelivery.md)). Its nested-node
[diagnostics](ops/evidence/20261002-managed-postgres-failure-reports/README.md) passed
34 top-level checks and three leak checks. Restart admission
now quarantines observed guest slots and instance IDs before
prepared-network allocation or RPC service; unowned boots/stops are rejected
([ADR-472](adr/472-managed-postgres-restart-resource-quarantine.md)). This protects
surviving identities but does not reattach guests or resume recovered reports.
Its nested-node [diagnostics](ops/evidence/20261002-managed-postgres-restart-quarantine/README.md)
passed 13 selected top-level metal tests and three leak checks, including real
guests with UUID, builder-prefixed and compact IDs. Admission fencing and local
teardown do not yet provide durable all-node drain proof. Linux vmmd now commits
lease intent before resource creation and records each guest's kernel boot ID,
PID and process start time in private persistent storage
([ADR-473](adr/473-managed-postgres-resource-intent-journal.md)). Restart inventory
quarantines guest and ambiguous journal records, including intent with no
observable resources.
Process provenance does not reconstruct lifecycle ownership; verified restart
cleanup and serving recovery remain pending.
Its nested-node [diagnostics](ops/evidence/20261002-managed-postgres-resource-journal/README.md)
passed 21 selected top-level tests and three leak checks, including real guest
checkpoint/reopen and confirmed journal retirement. Image staging now persists
temporary-file and bind intent, inode/namespace/mount provenance and original
source modes; live-owner cleanup retains failures and refuses observed replacements
([ADR-474](adr/474-managed-postgres-resource-assets.md)). This inventory still
excludes several resource classes and complete jail/network namespace incarnations.
Its nested-node [diagnostics](ops/evidence/20261002-managed-postgres-resource-assets/README.md)
passed 31 selected top-level tests and three leak checks, including real mount
failure/replacement guards and reopened guest bind provenance.
Version-3 journaling adds jail-directory and named nsfs binding observations;
live owners check replacements before cleanup and preserve prepared alias identity
([ADR-475](adr/475-managed-postgres-resource-placement.md)). Complete resource
incarnations and verified restart cleanup remain pending.
Its nested-node [diagnostics](ops/evidence/20261002-managed-postgres-resource-placement/README.md)
passed 41 selected top-level tests and three leak checks, including real nsfs
replacement, prepared alias transfer and foreign jail-mount guards.
Version-4 veth assets add atomic creation addresses, interface indices and vmmd
network namespace context ([ADR-476](adr/476-managed-postgres-resource-links.md)).
Live owners fence ordinary/prepared teardown and private-link reconciliation;
indexed host-link deletion precedes namespace deletion. Prepared claims persist
link provenance before policy or guest start. ADR-477 extends this with durable
spares and handoff intent; verified restart cleanup remains pending.
Its nested-node [diagnostics](ops/evidence/20261003-managed-postgres-resource-links/README.md)
passed 49 selected top-level tests, full macOS race suites and three leak
checks, including real foreign veth/dummy links, renames and prepared reuse.
Version-5 records preserve one stable spare filename through transfer intent and
validated guest adoption ([ADR-477](adr/477-managed-postgres-prepared-network-journal.md)).
Startup quarantines source, target and slot; journal-enabled startup skips
name-based spare deletion. Live cleanup retires the durable record before slot
release. Same-boot restart reclamation and filesystem power-loss qualification
remain pending.
The [handoff diagnostics](ops/evidence/20261003-managed-postgres-prepared-handoff/README.md)
passed 71 selected top-level tests, five real process-crash checkpoints,
full Linux/macOS race suites and three lifecycle leak checks.
Customer cutover activation remains disabled, and supported native lifecycle
acceptance remains pending.

Managed PostgreSQL drain plans (2026-10-13, [ADR-1153](adr/1153-managed-postgres-durable-drain-plans.md))
capture the app instance catalog and native member reservations atomically under
an existing admission fence and ownership epoch. Restart recovery reuses the
original receipts. Unsupported members, empty/oversized inventories and stale
authority block the catalog verdict; terminal rows alone are never receipts.
The `catalog_retired` observation remains separate from external SQL-session
isolation, route convergence and customer activation. Production provisioning
and customer activation remain disabled; native KVM acceptance is outstanding.

Startup can now retire absent, fully checkpointed, unclaimed prepared-network
reservations from a different kernel boot
([ADR-478](adr/478-managed-postgres-prior-boot-spare-retirement.md)). Current UID
holders and resource-name collisions retain the reservation; durable journal
removal precedes allocation. Same-boot crashes, incomplete version-5 records, transfers and
guests remain quarantined. This removes no physical resources and supplies no
customer drain receipt. Native host-reboot and filesystem power-loss acceptance
remain pending; customer activation stays disabled.
The [reclamation diagnostics](ops/evidence/20261003-managed-postgres-restart-reclaim/README.md)
passed 79 selected top-level tests (255 including subtests), full Linux/macOS
race suites and three leak checks. Prior-boot provenance was injected; the
diagnostics preserve real foreign resources but do not perform a host reboot.

New prepared-network records commit the creator boot before physical setup
([ADR-479](adr/479-managed-postgres-prepared-boot-intent.md)). Absent unclaimed
version-6 reservations can therefore retire after a different kernel boot even
when setup checkpoints are incomplete or already retired. All current name/UID
collision checks and fsync-before-admission rules apply. Contradictory boot
provenance fails startup; same-boot crashes, transfers and guests remain
quarantined. Version 5 keeps its complete-checkpoint rule. Older binaries reject
version 6, so rollback requires a drained journal or a compatible binary.
Native reboot/power-loss acceptance remains pending; customer activation stays
disabled.

The [boot-intent diagnostics](ops/evidence/20261003-managed-postgres-prepared-boot/README.md)
passed 83 selected top-level tests (347 including subtests), 11 real process-crash
stages, 35 injected prior-boot collision cases, full Linux/macOS race suites and
three lifecycle leak checks. Lint, egress and deployment checks passed. The node
uses nested virtualization; native reboot/power-loss qualification is pending.

## M0 — repo scaffold. ✅

Repo tree, build/test/lint tooling, CI, `pkg/api` limits table,
8-role ansible bootstrap, hello-boot acceptance test. `make bootstrap`
gates it on a fresh bare-metal x86_64 control-plane node (reference deploy:
the original Hetzner EX44).

## M1 — vmmd core. ✅

Invariant-critical VM lifecycle: slot allocator (`pkg/fcvm`),
per-instance netns/TAP (`pkg/netns`, ADR-009), cold-boot config +
jailer argv (Appendix B / ADR-019), `Manager` with no-leak unwind,
metal layer (`manager_metal_test.go`), and the 5-RPC gRPC surface
at `/run/faas/vmmd.sock` (ADR-013/014/016, `pkg/vmmdgrpc`). KVM +
root required for the metal gate.

## M2 — imaged + guest-init. ✅

OCI→app-layer pipeline, two-drive scheme (`pkg/oci` diff + `pkg/rootfs`
applier), base→ext4 auto-stage (`pkg/imaged::EnsureBaseExt4`), real-mkfs
build in Linux CI, `guest/init` overlay + crash supervisor, two-drive
boot verified metal-side (`cmd/e2e/deploy_wake_metal_test.go`).

**Fixture follow-up:** the body/trim mismatch originally flagged in PR #55
was resolved by PRs #151, #159, #135; `deploy_wake_metal_test.go` is now
exercised by the M8 netns + egress test path. Native x86_64 acceptance-node
sign-off on the §14 metal gate is tracked under [What's next](#whats-next).

## M3 — snapshots + wake. ✅

Park/wake with the ADR-005 restore-or-cold-boot fallback, FC version
pinning (`snapshots.fc_version`), and the vsock post-restore resume
hook (ADR-022) that re-seeds entropy + steps clock — V6 acceptance
green in `pkg/fcvm/v6_resume_ext4_metal_test.go`.

**Remaining:** §14 V2 platform latency loop driver (100 cycles per app class,
p95 < 350 ms, p99 ≤ 500 ms, p999 ≤ 800 ms)
— see [What's next](#whats-next).

## M4 — gatewayd-public + gatewayd-internal + schedd. ✅

Routing, wake gate, admission ledger (47,600 MB headroom / 160 vCPU),
G7 flow-aware reaper (`pkg/sched/flowcount`), `PGBackend` PG routing,
schedd-over-gRPC (ADR-018), last-seen flush, 1k rps CI-asserted
hot-path load test (PR #44), per-VM `memory.max` + per-plan `tc`
egress (PR #37, closes #31 + #33).

## M5 — apid + deploy pipeline + CLI. 🚧

Production wiring is in via the pgx-backed `state.PgStore`, real
`rootfs.Builder` in `pkg/imaged::handleDeployment` (PR #26),
plan-quota table-tests (`cmd/e2e/quota_e2e_test.go`), the
snapshot-prime handshake that flips a deployment to `live` after
one cold-boot priming cycle, and the G2 sealed-secrets path
(PR #42); `gregale` CLI renders RFC 7807 problems (UX §3.3).

**Fixture follow-up:** the body/trim mismatch flagged in PR #55
was resolved by PRs #151, #159, #135 (same fixture exercised by
the M8 netns + egress path). Native x86_64 acceptance-node sign-off on the
§14 metal acceptance gate is tracked under [What's next](#whats-next).

**Beta ship-blockers landed** — PR #136 (`PR-A`, ship-blockers
for the beta cohort) and PR #154 (`PR-B`, atomic supersede +
durable build queue).

**Authentication surface rewritten** — PR #161 added Google
OAuth 2.0 (`cmd/apid/handlers_google.go`, routes mounted at
`cmd/apid/server.go:426-427`); PR #162 auto-creates the user
account and issues an active Bearer API key + session cookie
on signup; PRs #163 and #164 retired the stale magic-link login
email (the `/auth/verify` route remains in the codebase but is
no longer reachable through `/login`); PR #174 hardened
`POST /login` against pre-auth takeover
(`cmd/apid/handlers_auth.go:112`, closes #165).

**CLI SDK promoted** — PR #157 lifted the original CLI client to
`pkg/api/client.go` as the public SDK (38 exported methods
covering apps, deployments, plans, domains, crons, keys,
secrets, usage, and the OAuth device-code flow).

**CLI token now lives in the OS keychain (issue #293, closes gap G5)**
— the current `cmd/gregale/config.go` writes through
`github.com/zalando/go-keyring` (macOS Keychain / Linux libsecret via
D-Bus); the plaintext file at `~/.config/gregale/token` is retained only as a
fallback for headless hosts with no D-Bus session (CI runners,
SSH-only servers), and a WARN recommends installing `gnome-keyring`.
First successful keychain save one-shot-deletes the legacy
plaintext file so customers do not keep a redundant copy on disk
after upgrading.

**Mutable per-app env vars (issue #395, ADR-045)** — three new
plaintext routes mirror `/v1/apps/{slug}/secrets` minus the seal step:

```
GET    /v1/apps/{slug}/env
PUT    /v1/apps/{slug}/env/{KEY}
DELETE /v1/apps/{slug}/env/{KEY}
```

A new `app_envs` table (migration 00061) stores plaintext `value TEXT`
under the same `^[A-Z][A-Z0-9_]*$` SQL CHECK that gates sealed
secrets. Migration 00063 widens `api_keys_scopes_vocab_chk` to admit
`env:read` / `env:write` (both ship in the same PR so the DB CHECK
never rejects a freshly-minted API key during the rollout window).
Per-plan quotas (`EnvVarsMax` 16/32/64/256 Free–Scale,
`EnvValueMaxBytes` 4K/8K/16K/32K) live in `pkg/api/limits.go`
alongside the secrets quota (`SecretCountMax` 8/25/50/100) and the
Free one-day log-archive/read-back entitlement. Semantics: applies on next wake — no
snapshot invalidation, no new vmmd RPC. The wake path stages
`/etc/faas/env.json` next to the existing `/etc/faas/secrets.env`
sibling, and `guest/init::BuildEnvWithSecrets` gains a fourth
precedence layer with the order `OS environ < manifest env <
api env < secrets` (pinned by
`guest/init/app_test.go::TestBuildEnv_FourLayerPrecedence`). Audit
kinds `env.set` / `env.deleted` are distinct from `secret.set` /
`secret.deleted` so a sealed-secret audit row still means only
"credential change". The console Env Vars page becomes writable in a
separate `faas-frontend` PR — this PR ships the API surface only.

## M6 — builderd + real image pulls. ✅

Build-in-microVM is wired through (`cmd/builderd`, `pkg/builderd`
orchestration + executor, PRs #39/#40/#43); the metal lifecycle is
in `vm_metal.go` (`//go:build metal`) and calls vmmd over gRPC, with
`vm_stub.go` returning `ErrNotMetal` for non-metal builds. OCI
puller hardened (`pkg/oci/egress.go` — denied CIDRs cover RFC1918,
CGN, loopback, IMDS, ULA), streamed layer blobs. `cmd/imaged`
auto-stages the canonical `/srv/fc/base/runner-builder-<arch>.ext4` on startup.

Source-tarball staging + Dockerfile dispatch are in via PR #56
(closes #54): `pkg/builderd/drive.go::CreateBuildDrive1` copies
`VMRequest.SourcePath` into drive1 at `/build/src.tar` and re-stats
a sha256 against the host source to catch torn copies;
`pkg/builderd/dispatch.go::MapFramework` translates the host
`FrameworkDocker` enum into `api.FrameworkDockerfile` so guest-init
dispatches to `buildctl --frontend dockerfile` per ADR-004 instead
of falling through to Railpack-auto.

§14 orchestrator e2e closes M6 (PR #60, closes #57):
`cmd/e2e/build_metal_test.go` exercises the full chain
`apid → pg_notify('build_queued') → builderd → vmmd → firecracker
→ in-VM Railpack/buildctl → OCI image.tar → imaged →
deployments.Live` across three fixture paths (Node, Python,
Dockerfile). Reference-node sign-off remains the §14 source of truth per
CLAUDE.md.

## M7 — metering, billing, functions, cron. 🚧

The sampling/quota shapes are in `cmd/meterd` and
`pkg/billing/stripe`, the dunning state machine is
`pkg/state.MarkAccountDeletionPending` (ADR-021), GB-h = plan RAM
+ 8 MB per running second is in `pkg/meter`. Functions:
`guest/runners/{node22,node24,python312,python313,go124}` (handler
contract per spec §4.9; `go124` apps deploy with a static binary
emitted by Railpack's go plan; Go functions that speak the
persistent protocol — the `function-go` template and
`sdk/go/function.Serve` — are started before readiness and reused while
the instance runs, and restarted on every snapshot restore so restored
instances never share random state (ADR-687); others keep the
per-request subprocess model). Evidence, measured before
restart-on-restore, 2026-10-10 on `gregale-internal-test-1`
(`TestWakePlatformBenchMetal`,
real go124 runner in the guest, 4 interleaved runs × 15 wakes per
variant): boot-completed→first-byte p50 93 → 54 ms and gateway
proxy→first-byte p50 66 → 42 ms. Whole-wake latency on that node is
dominated by ext4 drive staging (p50 ~400 ms) and did not separate.
`binary` (ADR-987) runs one static executable built from the
customer's Dockerfile (Rust, Zig, C, C++, Go) on the `go124` base and
runner; it adds no base image, and the `function-rust` template is the
reference. Evidence, 2026-10-11 on `gregale-internal-test-1`
(`cmd/e2e` `TestBinaryFunctionMetal`, full daemon set, builder microVMs):
the unchanged `function-rust` template built from its Dockerfile, went
live with imaged staging the `go124` base, and served `GET /hello` through
the gateway. A static C handler then answered two snapshot-restore wakes
with `runtime=binary` and a different start-time random token each time,
so each restore ran a fresh handler process. The migration's Up, Down and
replay passed on Postgres 16.
Functions without an explicit `idle_timeout_s` park after 30 s on every
plan (ADR-974, `Limits.DefaultIdleTimeoutS`); apps keep the plan default.
Functions stream responses and send every header value (ADR-999). All five
runners share `guest/runners/internal.Serve`. Node Fetch bodies still being
produced, async-iterable and Python generator bodies, and Go handlers that
`Flush` stream; every other response is sent whole as before. Evidence,
2026-10-12 on `gregale-internal-test-1` (`cmd/e2e`
`TestFunctionStreamingMetal`, full daemon set): a streamed response reached
the client through the gateway with its first chunk 0.8 ms after the headers
and the second 1.97 s later, matching the handler's 2 s pause, and with both
`Set-Cookie` headers. `TestBinaryFunctionMetal` passed again on the shared
runner core.
Request timeouts have per-plan ceilings: Free 30 s, Hobby 1 min, Pro 5 min,
Scale 15 min (ADR-1150, `Limits.RequestBudgetMaxMs`). The gateway passes the
budget left to the guest in `X-Faas-Deadline-Ms`, and the runner bounds an
invocation by the longer of that and 30 s. Evidence, 2026-10-12 on
`gregale-internal-test-1` (`cmd/e2e` `TestFunctionLongRequestMetal`, full
daemon set): a Pro function with `request_timeout_s=120` and the default
30 s idle timeout streamed four ticks over 36.1 s through the gateway. Its
handler saw `X-Faas-Deadline-Ms=119999`, not the 86400000 the client sent.
The first attempt was cut at 29.5 s: on single-box installs schedd's stats
poller read only the empty capacity-stream cache, so the idle reaper never
saw the in-flight request. The poller now reads vmmd `Stats` directly when
schedd has no owner node.
Async, queue, cron and replay invocations run for the function's request
timeout too, at least 30 s (ADR-1180). Their leases cover the run, and long
ones are delivered off the drain tick and the cron loop. vmmd's stream
bridge waits for the guest's response headers as long as
`X-Faas-Deadline-Ms` allows; a fixed 30 s wait cut every response that
started after 30 s with a 502. Evidence, 2026-10-13 on
`gregale-internal-test-1` (`cmd/e2e` `TestFunctionLongRequestMetal`, full
daemon set, daemons at 74b9225c29 with the bridge, runner and test at
6d2e493e3a): the Pro function with `request_timeout_s=120` streamed for
36.0 s, answered a synchronous 40 s request once with HTTP 200 after 40.0 s,
and completed a 40 s async invocation in 40.7 s on its first attempt. Both
40 s runs saw `X-Faas-Deadline-Ms=119999`. Before the bridge fix every
attempt of that async invocation got a 502 about 32 s in and was retried.
A function may set `function_concurrency` (ADR-1342): one Node or Python
worker then serves that many requests at once, multiplexed by an id the
adapter echoes, and an abandoned request is cancelled without restarting
its worker. A `functions:` entry can set `ram_mb`, `request_timeout_s`,
`min_instances`, `concurrency`, `env` and `secrets_from_env`, applied before
the function's build, and `routes:` behind an `entry: true` function become
`kind=route` edge rules on the entry app's hosts. Evidence, 2026-10-14 on
`gregale-internal-test-1` (`cmd/e2e` `TestFunctionConcurrencyMetal`, full
daemon set at f4a1972f8c): a binary function whose handler speaks the shared
protocol, deployed with `function_concurrency=8`, answered eight concurrent
2 s requests in 2.17 s from one worker process, and after a client abandoned
a request the same worker reported one acknowledged cancel. The `gregale`
CLI then deployed a two-function manifest: the new `users` function was
reserved, configured and built with concurrency 4, its sealed
`USERS_TOKEN` reached the guest, `/users/42` on the entry host reached
`users` and `/about` reached the entry function, and a repeat deploy left
exactly one managed route rule.
`node24` and `python313` are the Tier 1 runtime
additions — additive on top of `node22` / `python312` with the
same envelope contract; handler paths `/app/node24.js` and
`/app/handler.py` respectively. See `docs/runtimes/{node24,
python313}.md` for the per-runtime detail and ADR-052 for the
canonical procedure to add a runtime. PR #373 (cron limits), PR
#423 (Move 4 — async-invoke) etc. Cron: `pkg/sched/cron.go`,
single-flight per scheduled fire, loop-tested in `cron_loop_test.go`. Cron caps (per-app
and per-account, Free gated to 402) live in `pkg/api/limits.go` and are
enforced by `apid`'s `createCron` under an apps `FOR UPDATE` row lock
(mirrors `CreateAppIfUnderQuota`); store-side check at
`pkg/state.PgStore::CreateCronIfUnderQuota`. Email:
`pkg/mail` interface with Resend + Postmark backends (gap G4).

**Billing-provider extraction (PR #155)** — the Stripe
implementation moved from `pkg/stripex` to `pkg/billing/stripe/`
and the `billing.Provider` interface is defined at
`pkg/billing/provider.go:39`. PR #173 later added the 5th method
`CreateUpgradeTransaction` to the interface.

**Paddle MoR provider (PR #158)** — `pkg/billing/paddle/` ships
with HMAC webhook verification (`pkg/billing/paddle/webhook.go`)
and an overage accumulator (`pkg/billing/paddle/usage.go`).

**apid + meterd dispatch (PR #173)** — both daemons now route
through `billing.Provider` via
`pkg/billing/loader/loader.go::LoadProviderForAPID` /
`LoadProviderForMeterd`. Operator selects via
`FAAS_BILLING_PROVIDER=polar`; empty selects the Polar public-release
provider. Paddle and Stripe remain explicit compatibility paths. apid also mounts
`/v1/webhooks/polar` with Standard Webhooks verification. ADR-032 records the
decision; the operator runbook is
`docs/ops/billing-provider-switch.md`.

**Webhook signature replay protection (PR for issue #294, ADR-042)**
— the three webhook ingresses (GitHub via `gatewayd-public`, Stripe + Paddle
via apid) verify HMAC but never checked the delivery UUID against a
dedupe table; a replayed webhook within the signature-validity
window succeeded twice. Closes the gap with a single shared
`webhook_deliveries` table (provider + delivery_id, 5-min TTL), a
`pkg/webhookdedupe` helper consumed by all three ingresses, one
sweep goroutine in apid, and a `webhook.replay_rejected` audit row
on each ingress. Replays return 200 (idempotent — provider
interprets as success and stops retrying). New `gatewayd-public` audit seam
mirrors the apid pattern.

**§14 M7 acceptance test (24h GB-h shadow, integer-arithmetic exact)**
— landed via PR #126. See
`pkg/meter/meter_test.go::TestInvoiceShadow24h` (local math),
`pkg/meter/pusher_shadow_test.go::TestPushHour_Shadow24h`
(push-side integer equality), and
`pkg/billing/stripe/sandbox_test.go::TestInvoiceShadow24h_Sandbox`
(live Stripe SDK — asserts `record.Quantity == 6187` exactly,
zero delta). Cadence switched from per-hour float (`qty =
int64(gbHours * 1000)`, 0.315 % short over 24h) to per-day
integer (`qty = mbSeconds * 1000 / 1024 / 3600`). The
`pkg/stripex/` directory no longer exists post-PR-#155 rename.

**Idempotent billing + observability surface (PR #75, not #71)**
— `usage_minutes` flipped to `ON CONFLICT DO NOTHING` and a parity
test was added for the shared `BillableRAMMB` helper. `cmd/meterd`
also got `/metrics` via `wire.NewOpsMetrics("meterd")`
(`cmd/meterd/main.go:256/278/285`) and an inline `/healthz`
(`cmd/meterd/main.go:293`). *(Earlier revisions of this file
attributed both to PR #71; that's the CLI-only `feat/m7-beta-hardening`
PR — corrected.)*

**M7 customer email coverage (PR #133)** — dunning entry /
recovery and quota-warning bodies in `pkg/mail/account.go`:
`PaymentFailedBody` (line 131), `AccountSuspendedBody` (96),
`AccountRestoredBody` (169), `QuotaWarningBody` (205). apid's
webhook handler fires `PaymentFailedBody` / `AccountRestoredBody`
on the success branch of `MarkDunningStep`; meterd's quota loop
fires `QuotaWarningBody` alongside `db.NotifyQuotaWarning` on the
first warning of each UTC day (dedupe gate at
`accounts.last_quota_warning_at`).

**Paddle e2e (PR #173)** — `cmd/e2e/paddle_e2e_test.go` exercises
three flows: signed `transaction.paid` on past_due → active within
5 s; signed `transaction.payment_failed` on active → past_due
within 5 s; bad HMAC → 400 with `validation_failed` problem.

## M7.5 — waitUntil post-response tail primitive (issue #667). ✅

`ctx.waitUntil(promise)` lands as a first-class guest-init
primitive, gated on ADR-078. PRs 1+2+3 of the issue ship as
PR #671 (commit `add1105f`); PRs 4+5+6 ship as one consolidated
PR covering the wire-up: schedd reaper gate, `AppendUsage
tailSeconds`, meter sampler/rollup, the three new `pkg/wire`
metrics, the permanent `TestPushHour_ExcludesTailSeconds` guard
test, the docs fixes below, and the metal acceptance test.

- **Wire:** 16-byte DGRAM on vsock port 1027 lead byte `0x04`
  (`[type][outcome][reserved 6B][elapsed_ms BE uint64]`); instance
  resolved from peer CID — the runner↔guest-init seam is the
  in-process WaitGroup + per-task `context.WithTimeout` draining a
  `tail_pipe_path` JSONL file the handler subprocess appends to.
  Reuses the existing 0x01/0x02/0x03 discriminator + port 1026
  host receiver. No new port, no new CID, no new gRPC RPC.
- **Reaper/park gates:** `ReapIdle` and `ReapAggressive` skip
  instances with `tail_count > 0` (mirrors the G7 OpenConns gate);
  `snapshotAndPark` installs a 5 s watchdog
  (`ParkTailDrainTimeoutSeconds = 5`) before snapshotting, and
  force-parks with a `wake.tail_failed{reason=forced_at_park}` audit
  row on watchdog fire.
- **Migration 00151:** `instances.tail_count integer NOT NULL
  DEFAULT 0` + `usage_minutes.tail_seconds bigint NOT NULL
  DEFAULT 0`. Replay-safe. Companion test
  `00151_wait_until_tail_test.go` pins the column types + the
  non-negative floor on `DecrementInstanceTailCount`.
- **Limits matrix (already in main from PR #671):**
  Free 5 / 16 / 4, Hobby 15 / 16 / 16, Pro 30 / 16 / 64,
  Scale 60 / 16 / 256 (TailTimeoutS / TailCapMax /
  ConcurrentTailsPerInstance). `TailCapMax = 16` is structural
  (the issue's hard ceiling); the per-plan
  `ConcurrentTailsPerInstance` controls how aggressive the cap is
  across concurrent requests.
- **Metrics (issue #667 / ADR-078):**
  `vmmd_guest_tail_seconds{plan, runtime, outcome}` (60 series),
  `vmmd_guest_tail_failed_total{plan, reason}` (16 series),
  `vmmd_tail_cap_reached_total{plan}` (4 series). All closed-set
  labels pre-instantiated at boot — the §12 tail-watchdog panel
  has zero rows from idle fleet and non-zero as soon as the first
  tail fires. Cardinality pinned by
  `pkg/wire/metrics_test.go::TestOpsMetrics_GuestTailSeconds_PreInstantiated`
  + siblings.
- **Informational only — load-bearing invariant:** `tail_seconds`
  MUST NOT enter `Math.GBHours`, `Provider.PushUsageRecord`, or
  any billing path. The permanent guard test
  `pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds`
  pins this — removing it requires removing ADR-078 §"Tail is
  informational" (a new ADR). The financial model and the
  customer-facing bill shape are unchanged.
- **Acceptance gate:** `pkg/fcvm/tail_metal_linux_test.go` (//go:build
  metal && linux) exercises the full path — handler returns, runner drains
  the tail, schedd park path observes the post-drain `tail_count
  == 0`, snapshot taken. Run with `make test-metal RUN_REGEX='TestMetal_TailEndToEnd'`
  on the native x86_64 acceptance host. `TestPushHour_ExcludesTailSeconds` and
  the cardinality tests run on every `go test` invocation and are
  the load-bearing pre-metal pre-merge checks.
- **Acceptance-checkboxes #6, #7, #8 closed (issue #667 follow-up, PR
  feat/issue-667-followup).** The original PR shipped the envelope
  shape + host-side receipt handler + schedd reaper gate, but
  deferred the runner-side tail host to this follow-up. Without
  it, customers could stamp `envelope.WaitUntilSec` and serialize
  it to JSON, but no runner acted on it. The runner-side tail host
  (`guest/runners/internal/tail_host.go`) plus the new
  `/run/guest-init/tail-events.sock` unix-domain proxy
  (`guest/init/tail_events_proxy_linux.go`) close the loop. **#6** =
  pathological tail killed at the ceiling
  (`guest/runners/internal/tail_host_test.go`, 6 unit tests pinning
  the TailHost surface). **#7** = cross-runtime parity
  (`guest/runners/internal/runnerparity/tail_host_pin_test.go`
  file-walk + 5 per-runtime `TestHandle_WaitUntilEnvelopeRoundTrip`
  tests). **#8** = metal end-to-end
  (`pkg/fcvm/tail_metal_linux_test.go::TestMetal_TailFullEndToEnd`
  fires a REAL 0x04 DGRAM via vsock to `VMADDR_CID_HOST:1027` and
  pins the wire encoding). Behavior change: `signal.SignalReady(...)`
  now fires AFTER the tail host's `Drain()` returns (not on the first
  non-5xx response as the original ADR described). The 5 s
  `snapshotAndPark` watchdog is the hard ceiling — if the tail host
  hangs, the park gate fires `tail_failed{reason=forced_at_park}`.
  The supported evidence is a native x86_64 KVM run. ADR-078 §"Amendment"
  documents the change.

## M7.6 — extracted Next.js dashboard + githubd. ✅

The frontend was extracted to the dedicated `faas-frontend`
repository by PR #160 (commit `42814d6` deleted `website/`). The
Go `html/template` dashboard described in earlier revisions of
this file is historical — see the external repo for the shipped
implementation. `pkg/dashboard` may remain in this repo for
backward compatibility but is not the production frontend.

`pkg/githubd` + `cmd/githubd` still live in this repo and provide
HMAC-verified webhook ingress, GitHub App OAuth + repo picker,
Checks-API status writer, and a per-install token cache with
proactive refresh. The production auth surface (Google OAuth +
dual Bearer / session cookies, `POST /login` takeover hardening,
retired magic-link machinery) is described under [M5](#m5--apid--deploy-pipeline--cli-)
via PRs #161–#164 and #174. SSE live updates on `/v1/events` and
`deployment_logs` persistence landed via PR #41, ADR-011,
ADR-012.

## M8 — hardening & ops. 🚧

All §11 ship-blockers and §12 ops surfaces from this milestone's
closeout are in via PRs #46 / #47 / #48 / #49 (G6 GDPR + 30-day
staged deletion per ADR-021; V6 vsock resume hook per ADR-022;
G7 flow-aware reaper in `pkg/sched/flowcount`; `AuthLimit` shared
per-IP bucket across `/v1/*` per §11 "10/min/IP"; per-VM cgroup
scope via jailer `--cgroup cpu.weight`; cold-wake UX surfaces
3+4+5 with `x-faas-wake: hot|restored|cold` and dashboard N+1
spinner) and PR #51 (the closeout batch):

- **§11 IPv6 egress** — `pkg/netns/policy.go` and
  `pkg/netns/config.go` now deny `fe80::/10, fc00::/7, ff00::/8,
  ::1/128, ::/128` via `ip6 daddr { … } drop` (ADR-023), in both
  the host firewall and the per-instance netns ruleset. Closes #32.
- **§11 cgroup fence verified** — #33 `memory.max = plan + 8 MB`
  after bringUp; unit tests in `pkg/fcvm/cgroup_test.go` green;
  metal test in `pkg/fcvm/manager_metal_test.go::TestMetalMemoryMaxFenceEnforced`
  runs on the dedicated native x86_64 acceptance node (`make test-metal`),
  not on a developer workstation.
- **§12 SLO dashboard pipeline** — `fcvm_snapshot_fleet_avg_bytes`,
  `fcvm_snapshot_fleet_p95_bytes`, `fcvm_resident_ram_pct`,
  `fcvm_lv_fc_used_pct` (schedd-owned), plus
  `vmmd_cold_boot_fallback_total` (vmmd-owned, ADR-016) and
  `gateway_wake_queue_wait_seconds` (`gatewayd-internal`-owned; legacy Prometheus job label `gatewayd` retained). Prometheus
  + node_exporter are ansible roles with SHA-256-pinned binaries,
  scrape config template at
  `deploy/ansible/roles/prometheus/templates/prometheus.yml.j2`.
  Grafana dashboard export at `deploy/grafana/faas-fleet.json`.
  **Build metrics (ADR-030):** `builderd_ops_total{op="build",code}`,
  `builderd_build_duration_seconds{outcome}`, `builderd_build_queue_wait_seconds`
  now emit from the build lifecycle, and `apid /status` computes the
  build-success SLO from real build data instead of the old vmmd
  cold-boot proxy (which measured wake, not build).
- **§12 public status page** — `apid` serves the unauthenticated
  `GET /v1/status` overview and `GET /v1/status/incidents/{public_id}`
  timeline. A five-minute evaluator maps labeled alerts into five public
  capabilities, overlays operator incidents/maintenance, and persists real
  UTC rollup buckets for 30-day uptime and coverage. Admin publishing is
  guarded by admin scope, allowlist, MFA, recent step-up, same-origin, and
  idempotency checks. `GET /status/slo.json` remains a compatible, always-JSON
  projection with its existing best-effort 30-day invocation rollup and recent
  operator incident history, while `GET /status` remains the minimal API-host fallback. The
  indexed React experience is served by `faas-web` at `/status`; see ADR-130.
- **§14 restore drill wired** —
  `deploy/scripts/faas-m8-restore-drill.sh` plus WAL-archiving
  knobs in the postgres ansible role. The drill now extracts the tar-format
  basebackup and `pg_wal`, verifies promotion, schema readiness, exact
  accounts/apps/healthy-instance row counts, and app wake recovery. It emits a
  dated record on pass or failure; an EX44 run is still required before the
  M8 sign-off claim.
- **`leakcheck.sh` glob fix** matches the v1.7 jailer `--id`
  constraint.
- **CPU-hour visibility shipped (issue #279 / PR #346 / ADR-039)** —
  per-instance CPU consumption is now exposed end-to-end:
  `schedd_instance_cpu_seconds_total{app,node}` (sum rollup,
  monotonic, regression-guarded), `usage_minutes.cpu_usec` (new
  column, additive `ON CONFLICT` merge; `mb_seconds` retains
  first-write-wins), `GET /v1/usage`, `/v1/usage/summary`, and
  `/v1/account/export` all expose `cpu_usec` / `used_cpu_hours`,
  and `gregale usage` shows a CPU panel. **Informational only — no
  billing change.** `pkg/billing/provider.go`, `pkg/api/limits.go`,
  and the financial model are explicitly untouched. The data
  path is the seam for the future billing PR (extends
  `Provider.PushUsageRecord` with `cpu_usec`).
- **Egress-byte visibility (issue #<TBD> / PR #<TBD> / ADR-046) —
  in progress** — per-instance customer egress is sampled from the
  kernel byte counter on root-side `vethHost.rx_bytes` (vmmd
  `pkg/fcvm/netstats.Cache` reads
  `/sys/class/net/<vethHost>/statistics/rx_bytes`, cumulative
  with regression-safe deltas) and from the gateway HTTP response
  writer (`pkg/gateway/handler.go:statusRecorder.Bytes`). Both
  accumulate additively in `usage_minutes.net_tx_bytes` (vmmd)
  and `usage_minutes.tx_bytes` (gateway). `usage_monthly` sums
  both columns; `GET /v1/usage`, `/v1/usage/summary`,
  `/v1/account/export`, and `gregale usage` expose the per-(account,
  app, month) totals; `pkg/appmetrics` rolls up
  `gateway_response_bytes_total{app,plan}` for the dashboard.
  **Informational only — no billing change.**
  `pkg/billing/{provider.go,stripe,paddle}`,
  `pkg/meter/pusher.go`, `pkg/api/limits.go`, plan limits, the
  Stripe/Paddle `gb_ram_hour` push shape, and the financial model
  remain untouched. The columns are the seam for the future
  egress-billing PR (extends `Provider.PushUsageRecord`).

**Networking & egress (PRs #128, #151, #159):**

- **PR #151 (tier-1 tenant egress)** — `pkg/netns/policy.go:77`
  adds `MasqueradeCIDR` (default `10.100.0.0/16`, line 121);
  `Render()` emits a `postrouting` nat chain at lines 198–201 with
  `ip saddr <CIDR> oifname "eth0" masquerade`. The persistent
  `br-tenants` bridge is brought up by
  `deploy/ansible/roles/br-tenants-up/` before
  `nftables.service` / `vmmd.service`. Metal regression in
  `pkg/netns/policy_metal_test.go`. Closes #134.
- **PR #128** — `pkg/netns/config.go:124` installs the netns
  default route inline in `Config.SetupCommands`; the forward
  chain uses the `BridgeName = "br-tenants"` constant
  (`pkg/netns/config.go:21`). Pinned by
  `TestSetupInstallsNetnsDefaultRouteViaBridge` /
  `TestSetupInstallsDefaultRouteAfterAddressing`
  (`pkg/netns/config_test.go:128,155`).
- **PR #159 (tier-2 per-app outbound IP allowlist, ADR-031)** —
  `migrations/00029_app_egress_allowlist.sql` adds an
  `egress_allowlist cidr[]` column to `apps` (v4-only BEFORE-row
  trigger). State path: `pkg/state/pgstore.go:336,455,493`,
  `pkg/state/memstore.go:585,602`. Metal regression in
  `pkg/netns/allowlist_metal_test.go::TestMetalAllowlistRuleInstalled`.
  Per spec §7 the feature is gated to **Pro / Scale** plans.
- **ADR-119 per-app static outbound IP (BYOIP, Scale-only, single-node v1)** —
  `migrations/00303_apps_static_egress_ip.sql` adds a nullable
  `static_egress_ip INET` + `static_egress_ip_set_at TIMESTAMPTZ` to
  `apps`, with a `family(INET)=4` CHECK (IPv6 deferred) and a partial
  unique index `apps_static_egress_ip_key` defending against cross-app
  IP collision. State path: `pkg/state/types.go::App.StaticEgressIP` +
  `pkg/state/pgstore_apps.go::UpdateApp` clear/set sentinels; apid
  handler in `cmd/apid/handlers_apps_static_egress_ip.go` (env gate
  `FAAS_STATIC_EGRESS_IP_ENABLED`, plan gate `StaticEgressIPAllowed`).
  Wire: `pkg/sched/vmmclient.go::VMMClient.UpdateStaticEgressIP` +
  `pkg/sched/egress_drift.go::onAppChanged` fans the pg_notify
  through the same drift subscriber the egress allowlist rides. vmmd
  applies the patch via `pkg/fcvm/manager.go::UpdateStaticEgressIP`;
  the per-VM netns renderer (`pkg/netns/config.go::NftCommands`)
  emits a sibling MASQUERADE-sibling rule in the per-netns
  `postrouting` chain — `oifname <VethPeer> ip saddr 10.0.0.2 snat to
  <CustomerIP>`. Operator TOML bundle at `/etc/faas/egress/static_egress_ips.toml`
  is loaded at vmmd startup + on SIGHUP via
  `cmd/vmmd/egress_static_ip_bundle.go::watchStaticEgressIPBundleReload`
  (same SIGHUP pattern as the egress allowlist bundle). CLI surface:
  `gregale app <slug> static-egress-ip {show|set <ip>|clear}`. Metal
  acceptance gates in `pkg/netns/static_egress_ip_metal_test.go`
  (nft syntax check + `ifconfig.me` round-trip from inside the netns).

**Observability & dashboards (PR #156):**

- Self-hosted Grafana role at `deploy/ansible/roles/grafana/`
  (templates: `grafana.ini.j2`, `provisioning-dashboards.yml.j2`,
  `provisioning-datasources.yml.j2`; files: `faas-fleet.json`,
  `grafana-server.service`). Includes the M1/M2 fleet panels and
  the D1–D5 dashboard fixes per ADR-031. The Prometheus pipeline
  + alertmanager + status-page `degraded` flag (PR #140) feed
  these dashboards end-to-end.

**Security & acceptance gates (PRs #130, #149, #150, #152, #153):**

- **PR #130 — per-wake stable `wake_id`** — minted in
  `Engine.Wake` (`pkg/sched/engine.go:282`) and on the Prime path
  (line 665). Schema in `migrations/00028_instances_wake_id.sql`;
  v4 fallback metric `faas_wake_id_v4_fallback_total`.
- **PR #149 — OpenAPI spec gate** — `make spec-check` invokes
  `vacuum lint` with `VACUUM_VER := v0.29.10` (`Makefile:225`);
  CI installs the vacuum binary from a SHA-256-pinned tarball
  (`.github/workflows/ci.yml`, `spec-check` job).
- **PR #150 — `changePlan` Stripe subscription gate** —
  `cmd/apid/handlers_ext.go:671-705` returns a 402 Problem with
  `CodePayment` when `acct.StripeSubscriptionItem == ""` and the
  requested plan requires a Stripe upgrade. Closes #142.
- **PR #152 — per-IP `AuthLimit` restored across loopback**
  (closes #89). `pkg/middleware/authlimit.go::defaultClientIP`
  trusts `X-Forwarded-For` only when `RemoteAddr` is loopback;
  the `gatewayd-internal` pin is at `cmd/gatewayd-internal/proxy.go:215-222`.
- **PR #153 — §11 security-hardening e2e sweep**.
  `cmd/e2e/sec11_sweep_test.go` (4 tests:
  `TestSec11_AuthLimitPerIP_CrossProcess`,
  `TestSec11_ApiKeyHashedAtRest`,
  `TestSec11_UnixSocketOnlyDSN`,
  `TestSec11_HostKey0400_Required`) plus
  `cmd/e2e/sec11_host_linux_test.go` (5 host-side checks:
  cgroups-v2 unified, kernel ≥ 6.8, unprivileged user-ns disabled,
  unattended-upgrades security-only, nftables policy file
  in-sync).

The §14 M8 gates still on the board are listed in [What's next](#whats-next).

- **Active-passive standby topology (ADR-083, accepted 2026-08-16):**
  lex-min leader election over `compute_nodes.name WHERE active=true`; `pkg/gateway/leader`
  package (`ElectLeader`, `Leader`, `LeaderStore`); `StandbyState` gauge
  (`<prefix>_gateway_standby_state`, enum warming/warm/draining) +
  `ActivePassiveFailoversTotal` counter (`<prefix>_gateway_active_passive_failovers_total{outcome}`,
  outcomes: `dns_flipped` | `dns_stale` | `peer_unreachable` | `manual_drain`); probe
  timeout bounded by `HAFailoverProbeTimeoutMS = 500` (in `pkg/api/limits.go`); drain
  deadline bounded by `HADNSRecordStaleSeconds = 30`. The failure drill is
  operator-driven on the native two-node acceptance pair; see
  `docs/ops/native-m9-acceptance.md` and the standalone runbook at
  `docs/runbooks/active-passive-ha.md`. Closes the Gate-A row "2nd box active-passive" in
  spec §14 M8.

## M9 — multi-box scale. 🚧

- **ADR-422** (bare-metal service recovery capacity, 2026-10-01): local implementation adds an opt-in durable fleet policy, atomic database admission guards, conservative RAM/CPU/vCPU recovery slots, capacity-aware service placement and an operator capacity certificate. Eleven shared MemStore/Postgres cases, including warm-transition admission and rollback, direct SQL/restart/migration replay, protected-limit and mixed-size host-loss scenarios pass. Full portable state/scheduler/API/CLI/API-contract race suites, pinned lint, SQL generation, repository policy gates and daemon/CLI builds pass. Native KVM acceptance, throughput measurement and leakcheck remain pending; see `docs/ops/service-capacity-protection.md` for local evidence and fleet qualification.

- **ADR-421** (continuous app ownership recovery, 2026-10-01): node-owned schedulers scan at startup and every five seconds, rotate bounded app-ID pages past capacity refusals, and transfer ownership only after locking and rechecking source/destination lifecycle. Routing invalidation and ADR-420's durable service recovery restore desired replicas without traffic. The sixty-service host-loss/restart/capacity-return scenario passes against MemStore and Postgres; lifecycle races, full scheduler/daemon race suites, pinned lint, SQL generation and build pass. Native x86_64 KVM acceptance and leakcheck remain pending; this is local implementation evidence, not fleet qualification. See `docs/ops/continuous-app-ownership-recovery.md`.
- **ADR-066** (Tier A5 cross-node live-instance migration, accepted 2026-08-07): four-phase handoff (Park → mint lease → `MigrateInstanceOwner` → ack), `schedd_live_migration_decisions_total{outcome}` counter, `apps.migrated_at` + `instances.migrated_at` stamped in the same transaction. Bundled with PR #509 (Tier A4 per-node schedd), PRs in the ADR-066 → 067 → 068 cluster.
- **ADR-062** (per-node schedd + async placement claim, accepted 2026-08-16): single-writer-per-host invariant survives multi-host deploys; `apid_control_plane_only` depguard in `.golangci.yml` prevents a control-plane path from calling a compute-only peer.
- **ADR-063** (snapshot de-localization, revised 2026-08-26; issue #1054): snapshots use the shared OCI backend as the authoritative transport, while each active node's vmmd asynchronously prepositions both restore blobs through a durable event-cursor plus `snapshot_replicas` queue. Origin metadata restricts new fan-out to the producer's region; wake placement prefers ready local replicas and retains on-demand restore/cold-boot fallback. vmmd now samples durable queue-to-ready latency (`snapshothipd_fanout_latency_seconds`) on a 100 ms cursor cadence, and per-claim lease fencing prevents late workers from overwriting reclaimed jobs; the two-node ≤200 ms measurement and 100-cycle leak drill remain M9 acceptance work.
- **ADR-067** (migrating-instance watchdog, accepted 2026-08-16): 1 s ticker self-heals stuck `state='migrating'` rows that never committed (peer died mid-handoff, gRPC dropped, operator killed the new owner). The watchdog is the only writer that can move a row out of `migrating` without a peer commit.
- **ADR-110** (declarative split-box manifest, accepted 2026-08-16): versioned YAML + typed schema at `deploy/manifest/splitbox.yaml` + `pkg/manifest/`; SemVer `schema_version (1.0.0)`; canonical validation through `gregalectl manifest validate` + the renderer + the release bundle installer + the doctor + the metal harness. PR-cluster shipped (PRs #912 #913 #914 #915 #917 #918 #919 #920 #921 #922 #923 #924).
- **ADR-141** (durable imaged→apid audit delivery, accepted 2026-09-03): migration 00590 adds a deduplicated `audit_event_outbox`; imaged keeps `pg_notify` as the fast wakeup, while apid transactionally writes the audit row and replays pending or expired-lease handoffs every two seconds. Failed deliveries back off, dead-letter after twelve attempts, and queue metadata is pruned after 90 days without deleting audit evidence. This closes the signature-audit loss window identified in ADR-058.

End-to-end smoke: `make native-m9-acceptance` exercises the native x86 per-node heartbeat/failure-safe path. It replaces the retired local virtualization harness; the target requires the acceptance marker, explicit `FAAS_M9_CONFIRM=native-x86`, and a schedd+vmmd pair on each node. The split-box deployment now installs a node-local schedd on every compute host, and peer schedds observe stale heartbeat timestamps so a frozen owner cannot hide its node. Live-migration workload fixtures and the measured snapshot fan-out gate remain follow-up work in the M9 runbook.

### M8 — alert pipeline. ✅ (this PR)

The §12 dashboard pipeline is wired end-to-end:

- **Alert rules** at `deploy/ansible/roles/prometheus/files/faas.rules.yml`
  encode the §12 thresholds verbatim — fifteen rules under a single
  `faas_slo` group (twelve §12-mandated + three TLS observability rules
  from ADR-024 H3: `FaasTLSCertExpiryPage`, `FaasTLSCertExpiryWarn`,
  `FaasTLSOnDemandDeniedHigh`), three severity tiers (`info` / `warn`
  / `page`), every annotation carries a `runbook_url:` pointing at the
  `docs/runbooks/<AlertName>.md` stub index below.
- **Alertmanager role** at `deploy/ansible/roles/alertmanager/` mirrors
  the prometheus role's shape (defaults / tasks / templates / handlers /
  systemd unit), SHA-256-pins the 0.27.0 tarball, and binds 127.0.0.1:9093
  on loopback only. Secret material (SMTP password, Pushover token)
  loads via `_FILE` indirection from operator-provisioned files —
  same precedent as `FAAS_HOST_AGE_RECIPIENT_PATH` (gap G2 lean §17,
  sealed at rest).
- **Severity routing:** `info` → no notification (suppressed);
  `warn` → ticket-only email via `faas-warn` (4 h repeat);
  `page` → operator email + Pushover via `faas-page` (1 h repeat,
  `priority: 2` to bypass device quiet hours).
- **Scrape-config corrections** — PR #132's bind-address defaults
  (apid 9101, imaged 9102, schedd 9103, vmmd 9104, meterd 9106) plus
  the sibling-path overrides (`vmmd /metrics/fallback`,
  `schedd /metrics/fcvm`) so the alert rules' data sources are
  actually scraped. New jobs added: `builderd 9105`, `githubd 8083`.
- **Status page degraded flag** — `cmd/apid/status.go::fetch` runs a
  fourth PromQL query over firing warn/page platform alerts. It excludes
  `alert_preset_signals` and `alert_preset_correlation`, whose alerts describe
  one tenant's configured policy rather than fleet health, and alerts labelled
  `public_status="internal"`, which remain operator-actionable but do not mean
  the customer-facing service is degraded.
  alongside the existing three. The boolean lands on
  `pkg/api.StatusPage.Degraded` and `deploy/statuspage/index.html`
  renders a red "Service degraded" pill driven by it. The public page
  now shows prospects and customers the same picture the operator's
  pager sees.

#### Status page degraded-flag contract

- `Source = "prometheus"` — clean snapshot, no degraded pill.
- `Source = "degraded: firing alerts"` — at least one fleet/platform warn- or
  page-severity alert that represents customer impact is currently firing; the
  pill is visible. Tenant alert presets and `public_status="internal"` operator
  alerts remain visible without changing public status.
- `Source = "degraded: <error>"` — the full Prometheus pipeline is
  unreachable; the handler returns the last cached snapshot with the
  error stringified. Pre-existing graceful-degradation contract from
  PR #51 (status page must never 5xx during a transient Prometheus
  hiccup).
- The alert query failing in isolation (Prometheus reachable but
  `ALERTS{}` not yet populated, e.g. on a freshly-reloaded Prometheus)
  is treated as "no firing alerts" rather than poisoning the snapshot
  — the flag is intentionally conservative.

#### Status page history contract

- `uptime_30d_pct` is the time-weighted availability of complete five-minute
  platform observations for the last 30 UTC calendar days. It is derived from
  the same component telemetry and operator incident overlays as the public
  status endpoint; customer function results, timeouts, dead letters, and
  cancellations never lower platform uptime.
- `uptime_30d` always contains 30 daily points. The compatibility fields
  `successful` and `total` count available and observed five-minute platform
  intervals. A day with no complete platform telemetry has `total: 0` and
  `uptime_pct: null`, so missing coverage is not published as an outage.
- The switch to platform observations intentionally resets the legacy 30-day
  history to the observation-bucket retention window. Historical customer
  invocation failures are not backfilled into the new series.
- `incidents` contains incidents posted in the last 30 days, plus any still-
  open older incident. The public projection includes `started_at`,
  `resolved_at`, `severity`, `summary`, and the affected `component`.
- The history query is capped at 100 incidents and has a 2 s database timeout.
  If Postgres is unavailable, the endpoint still serves the current
  Prometheus-backed snapshot and leaves history empty/default for that
  refresh.

#### Runbook index

| Alert | Runbook | Severity |
|---|---|---|
| `FaasHighResidentRam`, `FaasHighResidentRamWarn` | [HighResidentRam](runbooks/FaasHighResidentRam.md) | page / warn |
| `FaasSnapshotFleetAvgHighPage`, `…Warn` | [SnapshotFleetHigh](runbooks/FaasSnapshotFleetHigh.md) | page / warn |
| `FaasLvFcUsageHighPage`, `…Warn` | [LvFcUsageHigh](runbooks/FaasLvFcUsageHigh.md) | page / warn |
| `FaasBuildQueueBacklog` | [BuildQueueBacklog](runbooks/FaasBuildQueueBacklog.md) | warn |
| `FaasWakeLatencyHigh` | [WakeLatencyHigh](runbooks/FaasWakeLatencyHigh.md) | warn |
| `FaasColdBootFallbackHigh` | [ColdBootFallbackHigh](runbooks/FaasColdBootFallbackHigh.md) | warn |
| `FaasApiAvailabilityLow` | [ApiAvailabilityLow](runbooks/FaasApiAvailabilityLow.md) | page |
| `FaasBuildSuccessLow` | [BuildSuccessLow](runbooks/FaasBuildSuccessLow.md) | warn |
| `FaasDaemonDown` | [DaemonDown](runbooks/FaasDaemonDown.md) | page |

CI gate: `promtool check rules` runs in `lint + build` against
the same tarball the production ansible role pins (`prom_version: "2.54.1"`),
catching malformed PromQL or dangling matchers at PR time.

---

Post-M8 = private beta (founding doc M2–M3 hand-held phase).

### Post-M8 — per-app reserved eviction tier (issue #475). ✅ (this PR)

Customer-facing opt-in to a reserved tier that protects an app
from cross-account RAM-pressure eviction. Closed 6-tuple counter
set pre-instantiated at boot; idle-still-park guarantee enforced
by leaving `ReapIdle` and `ReapAggressive` unchanged.

- **Schema** — `apps.eviction_priority` text NOT NULL DEFAULT
  'best_effort' + `apps_eviction_priority_chk` CHECK
  (migration 00138; replay-safe via ADD COLUMN IF NOT EXISTS +
  DO-block `pg_catalog.pg_constraint` guard). Pre-#475 rows stay
  on the historical LRU path bit-for-bit.
- **Plan tier** — `Plan.EvictionPriorityReservedAllowed` (Free = false,
  Hobby / Pro / Scale = true) + `Plan.ReservedConcurrencyPerAccount`
  (Hobby 1, Pro 2, Scale 4). The cap counts APPS, not instances
  (issue #475 binding interpretation).
- **Reaper** — `SelectEvictions` sort comparator prepends a tier
  early-out (best_effort before reserved) so cross-account RAM
  pressure parks every best_effort candidate before any reserved.
  `ReapIdle` and `ReapAggressive` are intentionally unchanged.
- **Counter** — `schedd_evicted_priority_total{priority, reason}`
  (priority ∈ {best_effort, reserved}, reason ∈ {idle,
  eviction_aggressive, eviction_ram}). Pre-instantiated so the
  §12 panel has zero rows from idle fleet.
- **apid PATCH** — `PATCH /v1/apps/{slug}` accepts
  `eviction_priority`. 402 `plan_eviction_priority_reserved_not_allowed`
  on Free; 422 `plan_eviction_priority_reserved_quota` when the
  per-account cap is exhausted. 422 `validation_failed` for any
  value other than the closed set. Audit kind
  `app.eviction_priority_changed` (subject `&acct.ID`) emitted
  from apid only on actual value change.
- **SDK** — `sdk/go::Client.SetAppEvictionPriority(ctx, slug,
  priority)` thin one-liner + `UpdateAppRequest.EvictionPriority
  *string` for bundled PATCH payloads.
- **CLI** — `gregale app <slug> --eviction-priority=best_effort|reserved`
  (Free rejected server-side). Text output surfaces the current
  tier alongside the other per-app knobs.

ADR-075 / issue #475 / migration 00138.

## M8 — Outbound webhook delivery reliability

- **Schema** — `app_webhooks` (slot 140) + `app_webhook_deliveries`
  (slot 141). Slot 139 carries a fence reservation matching
  `00130_reserve_slot.sql` (ADR-041). The deliveries partial index
  `(status, next_attempt_at) WHERE status IN ('pending','in_flight')`
  keeps the dispatcher's claim query O(due-rows) rather than
  O(table). The `(account_id, created_at DESC)` index backs the
  customer-facing deliveries endpoint without a sort.
- **State** — `CreateAppWebhookIfUnderQuota` mirrors
  `CreateCronIfUnderQuota` (apps-row FOR UPDATE + per-account
  count under tx). `ClaimDueAppWebhookDeliveries` uses a
  `FOR UPDATE SKIP LOCKED` claim transaction with
  `ORDER BY account_id, next_attempt_at LIMIT $cap` — per-account
  fairness emerges from the query, no token bucket needed.
- **Dispatcher** — `pkg/webhook.Dispatcher` runs as a third
  goroutine in schedd (alongside the cron drain + scheduler
  watchdog). 5s tick, 32-row cap, 7-attempt DLQ. Backoff schedule
  `30s, 2m, 10m, 1h, 6h` with ±25% jitter via `crypto/rand`.
  Clock injection via `Sleeper` + `Now` struct fields (not package
  vars) makes the 7.5h DLQ path testable in ≤1s wall. Property test
  `TestDispatcher_Fairness_PerAccountRoundRobin` (5 accounts × 100
  rows × 10 ticks) pins the round-robin claim.
- **Header set** — `X-Faas-Webhook-Signature`,
  `X-Faas-Webhook-Timestamp`, `X-Faas-Webhook-Attempt`,
  `X-Faas-Delivery-Id`. The delivery id is stable across retries —
  customers can dedupe by it. The alert path keeps `X-Faas-Alert-*`
  unchanged (the `pkg/webhookout.HeaderSet` seam separates the
  two).
- **Secret sealing** — `secretbox.SealBytes(recipient, "APP_WEBHOOK",
  plaintext, 256)` at apid-write time; dispatcher unseals with
  `secretbox.OpenBytesMulti` against the host age identity. The
  plaintext is destroyed at function exit and never crosses the
  wire after the create round-trip — the response carries only
  `webhook_secret_sealed_masked: "***"`.
- **API** — endpoints under `/v1/apps/{slug}/webhooks[/...]`:
  list, create, get, update, delete, rotate-secret,
  list-deliveries, attempt-history, delivery-health, retry-delivery. Plan-tier gate (`WebhookPerApp
  == 0` → 402 `plan_webhooks_not_allowed`); quota gate
  (per-app / per-account → 422 `plan_webhook_quota`). Closed enum
  drift on `retry_policy` and `event_filter` surfaces as 400
  `app_webhook_invalid` BEFORE the row is created.
- **Event vocabulary (issue #2444)** — new subscriptions expose only the
  producer-backed events `app.parked`, `app.woken`, and
  `usage_statement.finalized`. The delivery ledger retains its historical
  closed set so old delivery rows remain readable across upgrades. App park
  records a durable transition with the status update and emits only after
  instance drain, with schedd recovery if apid exits before completion
  (ADR-342). Usage statement finalization writes its event and matching
  subscription snapshot in the same transaction, then relays to one durable
  delivery row per subscription (ADR-344). Parked-to-active wake transitions
  are recorded with the app status change and schedd recovers `app.woken` after
  readiness (ADR-343). The existing retry endpoint can replay every event.
  OpenAPI carries a payload schema for each B5 event and for the finalized usage
  statement payload.
- **CLI** — `gregale webhooks <list|add|update|rm|deliveries|retry>`
  (mirrors `gregale crons`). Closed-set drift on `--retry-policy`
  surfaces locally before the round-trip (same posture as
  `--eviction-priority` from PR #647).
- **SDK** — `sdk/go/internal/api` plus the Node and Python generator regens
  carry the new surface; both generated clients expose the finalized usage
  statement payload and the new event filter value.
- **Audit** — `app.webhook_created`, `app.webhook_updated`,
  `app.webhook_deleted`, `app.webhook_secret_rotated`,
  `app.webhook_delivery_retried`, plus the dispatcher-emitted
  `webhook.delivered` / `webhook.failed` / `webhook.dead`.
- **Delivery health** — scoped webhook health APIs and the dashboard show
  queue counts, oldest overdue age, and 24-hour terminal success rate.
  Schedd exports separate fleet claimable-age and receiver-held due-count/age
  signals, plus dead-delivery and poll-success metrics, with alert rules and an
  operator runbook.

ADR-076 / issue #476 / migrations 00140 + 00141.

## M8 — API maintenance mode (ADR-091 amendment). 🚧

Two complementary customer-facing primitives that ship together as one feature cluster:

1. **`apps.maintenance_mode` bool** — coarse per-app gate; one PATCH flips the whole app into maintenance, every request returns 503 + `Retry-After: 60` + `Problem.code = "app_maintenance_mode"`.
2. **`kind=maintenance` edge rule** — fine-grained per-route gate; a `(match_host, match_path, match_methods)` tuple returns 503 + `Retry-After` (per-rule, default 60 s, hard cap 24 h) + `Problem.code = "edge_rule_maintenance"`. Optional `message` (≤512 B) goes into `Problem.detail`.

The coarse gate runs BEFORE the fine-grained rule, so when both fire on the same request the customer sees `app_maintenance_mode`, not `edge_rule_maintenance` — the load-bearing customer-facing guarantee is "an app I put into maintenance is in maintenance; the per-rule contract is a no-op in that case". Both primitives fire BEFORE auth, BEFORE wake, so a maintenance 503 never pays a cold-boot cost.

- **Schema** — migrations `00222_edge_rules_kind_maintenance.sql` (DROP+ADD on `edge_rules_kind_check`, widening to `{route, rewrite, redirect, headers, cors, jwt, ip, validate, limit, maintenance}`) and `00223_apps_maintenance_mode.sql` (`ADD COLUMN IF NOT EXISTS maintenance_mode boolean NOT NULL DEFAULT false` + partial index `WHERE maintenance_mode = true` + `apps_maintenance_mode_notify` AFTER UPDATE trigger that emits `pg_notify('app_changed', NEW.id::text)` ONLY when `maintenance_mode IS DISTINCT FROM old.maintenance_mode`).
- **Cluster shape** — three-PR cluster (PR-A control-plane widening + fence, PR-B runtime surface + gateway hot-path, PR-C rollout-closer + e2e + spec backfill). Mirrors the kind=validate cluster (PR #840 / #841 / #848). PR-A consumed slots 220 + 221 via fences; PR-B consumed both slots and removed the fences.
- **Hot-path slot** — `pkg/gateway/handler.go:2943` inserts `applyAppsMaintenanceMode` (coarse gate on the substituted app) + `applyEdgeRuleMaintenance` (fine-grained rule) at the TOP of the `haveApp` chain (§4.1.2.0 + §4.1.2.14), BEFORE redirect/rewrite/headers/CORS/JWT/IP/limit/validate/auth/wake. Same deny-before-cost posture as ADR-091 D4 codifies for every other cost-control kind.
- **Cache invalidation** — new `db.NotifyAppChanged = "app_changed"` pg_notify channel; gatewayd listener drops only that app's entry from the apps LRU (`Backend.ResetApp(appID)` at `pkg/gateway/pgbackend.go:1025`), not wholesale `FlushRoutes`. A maintenance flip on one app doesn't evict every other app's cache entry — `TestHandleInvalidation` pins the differential.
- **Per-rule quota** — counts against the existing `EdgeRulesPerApp` quota (5/25/100/500 by plan). No new per-rule counter for v1 (D20.10 if customers ask).
- **Defaults** — `api.EdgeRuleMaintenanceRetryAfterSeconds = 60` (env-overridable via `FAAS_EDGE_RULE_MAINTENANCE_RETRY_AFTER_SECONDS`). Cap: `api.MaxEdgeRuleMaintenanceRetryAfterSeconds = 86400` (24 h, hard, enforced at apid Validate).
- **Free-tier-allowed.** No `IsPaidOnly()` on either primitive.
- **Metrics** — new `gateway_app_maintenance_total{plan}` (pre-instantiated at the closed `{free, hobby, pro, scale}` set, plan=string(app.Plan)) + new `gateway_edge_rule_match_total{kind="maintenance", outcome=match|miss|blocked}` pre-instantiation loop (mirrors the 9 prior kinds). No existing metric names changed.
- **Audit** — `app.maintenance_mode_match`, `edge_rule.maintenance_matched`, `edge_rule.maintenance_blocked`. Cross-account rules silently fall through (`outcome=blocked` + audit `maintenance_blocked`, same posture as every other kind, ADR-091 D5).
- **E2E** — `cmd/e2e/edge_rules_maintenance_e2e_test.go` (3 tests: match-returns-503, default-retry-after, cross-account-falls-through) + `cmd/e2e/apps_maintenance_mode_e2e_test.go` (2 tests: patch-true-returns-503, coarse-gate-beats-edge-rule).
- **TTL / auto-disable** — out of scope for v1; deferred to D20.7.

ADR-091 amendment. No new ADR slot.

## M8 — Per-route observability (ADR-093). 🚧

Opt-in per-app route breakdown on the gatewayd-internal hot path,
surfaced as Prometheus `_by_route` series and a control-listener
reader at `GET /v1/apps/{slug}/routes`. Bounded by a 50-route cap
with a non-evicting `__route_other__` overflow bucket (ADR-093 D2).

- **Schema / State** — `apps.route_metrics_enabled boolean` (migration
  00216). Partial index `WHERE route_metrics_enabled = true` keeps
  the col-store cheap on Free-tier (where the column is always false).
- **Plan gate** — Hobby/Pro/Scale default-on; Free stays off. Two
  accessors at `pkg/api/limits.go:2649` `RouteMetricsEnabled()`
  (create-time default) and `:2662` `RouteMetricsResponseAllowed()`
  (PATCH-time gate) so a Free PATCHing true surfaces
  `plan_route_metrics_not_allowed` (403, `pkg/api/errors.go:720`).
- **Metric primitives** — `pkg/gateway/metrics.go:404-417`
  `requestsByRoute`, `durationByRoute`, `failuresByRoute`. The
  `_by_route` suffix is required so the new `{route}` label set
  doesn't collide with the pre-existing `{app, code}` CounterVec
  (Prometheus rejects two CounterVecs with the same name but
  different label sets at registration time).
- **API** — `GET /v1/apps/{slug}/routes` (apid reverse-proxy to
  gatewayd-internal `GET /v1/internal/apps/{slug}/routes`).
  OperationId `getAppRoutes` (api/openapi.yaml:1394-1427, `x-issue: 273`).
- **Operator kill-switch** — `route_metrics_enabled` toml + env
  `FAAS_GATEWAY_ROUTE_METRICS` (`cmd/gatewayd-internal/config.go:107-121`).
  Cluster default wired by commit 5 of this PR
  (`deploy/ansible/roles/gatewayd_internal_service/templates/gatewayd.toml.j2`).
- **Alert** — `GatewayWildcardRoute` — sustained
  `__route_other__` overflow > 1 reqps for 10m. Runbook
  `docs/runbooks/GatewayWildcardRoute.md`. Recording rules + the
  alert land on the box via
  `deploy/ansible/roles/prometheus/tasks/main.yml` (commit 1 of
  this PR — G1+G2 closure).

## M8 — Per-service wire-protocol selector (issue #67 / ADR-124). 🚧

Customer knob: pick the wire protocol at the public edge
(`http1 | http2 | grpc`) via `apps.app_protocol`. Default `http1`
universal; `http2` universal; `grpc` Hobby+/Pro/Scale only.
Closes issue #67 (Cloud Run parity for k8s/Cloud-Run migrators
carrying gRPC services).

- **Schema** — migration `00382_apps_app_protocol.sql`
  (`ADD COLUMN IF NOT EXISTS app_protocol text NOT NULL DEFAULT
  'http1'` + `apps_app_protocol_chk` CHECK mirroring the
  `eviction_priority` precedent at `00346_deployments_annotation.sql:54-60`).
  Round-trip + replay-safety pinned at `00382_apps_app_protocol_test.go`
  (replay-safe ADD COLUMN IF NOT EXISTS + DROP/ADD CONSTRAINT IF
  EXISTS pattern from the streaming migration).
- **Plan gate** — `pkg/api/limits.go::Plan.AppProtocolAllowed(protocol)`
  mirrors the `StreamingResponseAllowed` precedent. `grpc` is gated
  per-plan on `AppProtocolGrpcAllowed bool` (Free=false,
  Hobby/Pro/Scale=true). apid applies the gate at `CreateApp` AND
  at `UpdateApp` — Free PATCH to `grpc` surfaces
  `plan_app_protocol_grpc_not_allowed` (RFC 7807, 403). Invalid
  values (`!∈ {http1, http2, grpc}`) surface
  `app_protocol_invalid` (400). The column-level CHECK is the
  belt-and-braces backstop, not the primary validator.
- **Closed-set DTO + validator** — `pkg/api/dto.go`: `CreateAppRequest.AppProtocol *string`,
  `UpdateAppRequest.AppProtocol *string`, `AppResponse.AppProtocol string`. `pkg/api/diff.go`:
  `DiffAppConfigPatch.AppProtocol *string` for the `deploy --diff`
  path. CLI: `cmd/gregale/commands2.go`, `commands5.go`,
  `commands_diff.go` wire `--app-protocol http1|http2|grpc` (create,
  update, diff).
- **Read-side plumbing** — `cmd/gatewayd-internal/backend.go::toApp`
  copies `state.App.AppProtocol` onto the gateway's local
  `gateway.App.AppProtocol` so the cached app struct carries
  the closed-set value without re-reading the apps row.
- **Header stamp** — `pkg/gateway/handler.go::decideProtocol`
  + `r.Header.Set("x-faas-protocol", decideProtocol(h, r, app))`
  at the site `x-faas-stream` is stamped today (~5131 streaming
  path; ~5261 buffered path). Empty→`"http1"` preserves
  pre-ADR-124 behaviour bit-for-bit. `pkg/gateway/forwardproxy.go::fwdStreamOnceWithEvents`
  reads the header and emits a `slog.Debug` "framing selection"
  line on the gatewayd-internal debug stream so operators with
  debug logging on can correlate per-app protocol choice with
  bridge-side framing behaviour.
- **OpenAPI / SDK** — `api/openapi.yaml` adds `app_protocol` to
  `AppResponse`, `CreateAppRequest`, `UpdateAppRequest`,
  `DiffAppConfigPatch` (type `string`, `enum: [http1, http2, grpc]`,
  `example: http1`). `make spec-sync` mirrors to
  `pkg/apid/openapi.yaml`. SDK node + Python regenerated.
- **Diff engine** — `pkg/deploydiff/{diff,engine,quota,render_text}.go`
  gain the `app_protocol` branch (closed-set Modify; Free-account
  gate on `grpc` Add). Mirrors the streaming/require_authn
  pattern at `pkg/deploydiff/quota.go:105`.
- **E2E** — `cmd/e2e/app_protocol_test.go` pins (CI, no metal):
  Free-app `grpc` rejected, Hobby default = `"http1"`,
  Hobby persistence of `"http2"` round-trip, PATCH of
  invalid value → 400 `app_protocol_invalid`.
- **Out of scope (filed §17 G19)** — end-to-end H2 framing on
  the customer→guest leg (i.e. `grpc` actually reaches the
  guest's `:8080` as gRPC trailers, not as H1+chunked). The
  bridge already speaks H2C on the vmmd side and H1+chunked
  on the guest side per PR #750 / ADR-079; this PR ships the
  customer knob without changing the bridge. G19 is a separate
  multi-week ADR (bridge-side termination on the guest side
  + coordination with the guest base image).

ADR-124 (new file at `docs/adr/124-app-protocol-selector.md`).
Closes §17 G18; opens §17 G19.

### Tier A — observation period

Operational follow-up (no code change). 2-week observation window
after PR #856 lands. Empirical answer to "does the 50-cap actually
bound cardinality or do apps collapse into `__route_other__`?" feeds
either Tier B hardening (per-method cap, post-rewrite route label,
`AppRoutesResponse` cap-visibility) or Tier C bigger ADRs (per-route
SLI, per-plan budget).

- **Alert surface** — `GatewayWildcardRoute` is `severity: info`
  and routes to the EMPTY `faas-silent` receiver
  (`alertmanager.yml.j2:53-56`). No page, no email, no Slack. Visible
  in Prometheus `/alerts` and Grafana only. The status-page degraded
  pill filters `severity=~"page|warn"` (line 519-541), so this alert
  does **not** move the pill by design.
- **Weekly review** — pull firing alerts directly:

  ```sh
  curl -s http://127.0.0.1:9090/api/v1/alerts \
    | jq '.data.alerts[] | select(.labels.alertname=="GatewayWildcardRoute")'
  ```

- **Mid-sprint Hobby-tier audit** — run
  `make hobby-route-audit` (uses `deploy/scripts/adr093-hobby-audit.sh`)
  against the control plane. Counts `__route_other__` vs real-route
  entries per Hobby-tier app. Read-only.
- **Post-deploy smoke** — operator-side runbook at
  `docs/runbooks/PostDeployAdr093.md`. Four artefact checks (rule
  file, systemd unit, toml, Grafana panels 103/104) + a rule-load
  check + an end-state snapshot. Read-only.
- **Decision at end-of-sprint**:
  - *Cap sufficient* (no Hobby-tier app saturated) → Tier B polish;
    Tier C (per-route SLI + per-plan budget) becomes the next major ADR.
  - *Cap partial* (some Hobby apps saturated, most didn't) → Tier B
    per-method cap.
  - *Cap insufficient* (most Hobby apps immediately saturated) →
    ADR-093 needs re-scoping (lower default cap, revert Hobby's
    default-on, or move to a customer-driven enable per plan).

## Auth default flip — issue #695 / ADR-080

Closes spec §17 G15. Cloud Run's analogue is IAM-authenticated by
default with `--allow-unauthenticated` opening a route; this flip
makes Gregale match that posture for newly-created apps.

- **Schema** — `apps.auth_default_flipped_at timestamptz NULL`
  (migration 00156, slot 155→156). Nullable so the migration is
  forward-only contraction (down-migration drops the column).
- **Grandfather mechanism** — migration stamps every pre-flip
  row at flip-time (`UPDATE apps SET auth_default_flipped_at =
  COALESCE(auth_default_flipped_at, now()) WHERE
  auth_default_flipped_at IS NULL`, idempotent), and emits one
  batch audit row `apps.auth_default_global_flipped` with the
  migrated count (replay-safe via `WHERE NOT EXISTS`).
- **Per-plan defaults** — `Plan.RequireAuthnDefault()` +
  `Plan.PublicAuthModeDefault()` in `pkg/api/limits.go`. Truth
  table:
  | Plan  | require_authn | public_auth_mode |
  |-------|---------------|-------------------|
  | Free  | `false`       | `"open"`          |
  | Hobby | `true`        | `"open"`          |
  | Pro   | `true`        | `"bearer"`        |
  | Scale | `true`        | `"bearer"`        |
  Hobby stays on `"open"` because bearer scope is gated off at
  that tier — defaulting to a mode a customer can't realise
  would strand them.
- **apid** — `buildApp` stamps both per-plan defaults onto the
  returned `state.App` at create-time. Wire shape unchanged:
  `AppResponse.require_authn` + `AppResponse.public_auth` already
  shipped in #560 / #477.
- **DTO** — new read-only field `AppResponse.auth_default_flipped_at`
  (omitted via `omitempty` on fresh-create apps; surfaces RFC3339
  for grandfathered apps so dashboards can render the
  "since YYYY-MM-DD" suffix).
- **CLI** — `gregale apps list` adds an AUTH column. `AUTH: open` /
  `AUTH: required` / `AUTH: required + basic`, suffixed with
  `· since YYYY-MM-DD` on grandfathered apps. Per-app opt-out:
  `gregale app <slug> --no-require-authn`.
- **Dashboard** — new `Page.ActionRequiredSurface` banner on the
  account view. The banner surfaces the migration date and the
  universal opt-out command (one place; no per-app link rot).
- **E2E** — `cmd/e2e/deploy_wake_metal_test.go`,
  `cmd/e2e/streaming_metal_test.go`,
  `cmd/e2e/wake_timeline_metal_test.go` opt out at create-time
  via `RequireAuthn: &falsy` because their anonymous probes
  aren't testing the authn surface.

ADR-080 / issue #695 / migration 00156.

## M8 — Scheduler divergence and bounded dispatch (ADR-191). 🚧

Follow-on to ADR-190, closing the two scheduler-side causes behind it:

- **Instance divergence.** A sweep every 30 s compares live rows against what
  the owning vmmd reports through the existing capacity-telemetry stream (no
  new RPC). Four gates before a row counts: the node must have reported
  something, the instance must be past a 60 s grace, it must be absent on two
  consecutive sweeps, and an empty snapshot resets all candidates. Disjoint by
  construction from the dead-node reconciler, which acts only on silent nodes.
- **Bounded loop dispatch.** One work pool replaces the four unbounded
  `go func` arms and the prime slot pool. Prime still overflows to inline
  (dropping strands a deployment in `snapshotting`); reconcile kinds overflow to
  drop, since each is idempotent over a durable table with a safety ticker.
  `MainLoopBudget` 180 s → 60 s, schedd `WatchdogSec` 180 s → 120 s.

**Ships inert.** `FAAS_SCHEDD_RECONCILE_ENFORCE` is unset, so the divergence
sweep counts and logs but writes no row.

Remaining, in order:

1. Read `schedd_instance_divergence_total{outcome="suppressed"}` against
   production reality for one week. Every count should be explainable by a vmmd
   restart, a host OOM, or a failed destroy.
2. Flip `FAAS_SCHEDD_RECONCILE_ENFORCE=1` in the compute-only drop-in and
   promote `FaasInstanceDivergence` from warn to page in the same change.
3. Close the known gap: a node whose only instance dies reports nothing and is
   skipped. Needs a vmmd-side "up with zero VMs" assertion.

## M8 — Daemon durability primitives (ADR-190). ✅

The "alive but not working" and "control-plane outage leaks into the data
plane" outage classes (schedd prime wedge 2026-09-03, fsn-1 connection
exhaustion 2026-09-12, route 404s during Postgres outages) are closed at
their seams rather than at the last call site that hit them:

- **Default gRPC deadline.** `wire.DialContext` installs a unary client
  interceptor: calls without a deadline are bounded by
  `FAAS_GRPC_DEFAULT_DEADLINE` (60 s) and counted on
  `grpc_client_calls_without_deadline_total{method}`. A forbidigo rule
  keeps every client on the sanctioned dial path.
- **Liveness-gated systemd watchdog.** `pkg/wire.Liveness` + `StartWatchdog`
  beat per named loop (schedd `main`, vmmd `sweep`, `runtime` everywhere)
  and send `WATCHDOG=1` only while no loop is past budget; every
  `Type=notify` unit now carries `WatchdogSec=` from the generator.
  `FaasDaemonLoopStalled` explains the restart.
- **Last-known-good routes.** `PGBackend.Lookup` serves the stale tier
  when the Router errors (never on not-found), bounded by
  `FAAS_GATEWAY_ROUTE_STALE_TTL`; `gateway_route_lookup_stale_served_total`
  shows the edge coasting.
- **One LISTEN connection per daemon.** `pkg/db/notify_hub.go` multiplexes
  every `SubscribeWithReconnect` on a pool onto one connection with the
  same fail-fast and LISTEN-active-on-return contracts; `FAAS_DB_NOTIFY_HUB=0`
  is the kill switch. `DaemonMaxConnections` is deliberately unchanged
  until `pg_stat_activity` confirms the drop in production.

Remaining: lower per-daemon pool budgets after one production cycle;
add real loop beats to gatewayd-internal and imaged (both run on the
`runtime` loop only today); run the SIGSTOP watchdog drill on the
acceptance node and record it under `docs/drills/`.

## M8 — Deploy configuration contract (ADR-143). ✅

The "read but never set" outage class (PR #1286 function runner paths, PR
#1287 AppErrors listener) is closed at the root:

- **One unit source.** `deployctl generate` now emits every service role's
  `files/faas-*.service` (8 roles) + `deploy/systemd/`; the retired
  `deploy/controlplane/` tombstone is deleted (ADR-110 Phase 2). vmmd,
  gatewayd-internal, gatewayd-public and builderd copies had drifted.
- **Declared environment.** `pkg/daemonunitspec/envcontract.go` +
  `envcontract_test.go`: every `FAAS_*` a daemon reads is declared with a
  delivery source and the test proves delivery both ways; table rendered
  to `docs/ops/env-contract.md`. Found and fixed three more silently-off
  gates: streaming (`streaming_enabled=true` in gatewayd-internal.toml),
  jobs dispatch (`FAAS_JOBS_DISPATCH=1` in public-beta units), GeoIP
  database (new `geoip` role, pinned DB-IP release).
- **Restart handlers** on every service role (`daemon-reload` +
  `try-restart`), notify on every unit/drop-in/config task.
- **Verified convergence.** `vars/daemons.yml` topology (lockstep-tested),
  `fleet_verify` role at the end of both bootstrap plays, strict
  `verify.yml` / `make verify-fleet`; CI runs `scale_check.yml` for real
  and `ansible-lint` (production profile).
- **Spec §11 host posture** as `host_hardening` (sshd drop-in with lockout
  guard, fail2ban, unattended security upgrades without reboot, auditd,
  kernel sysctls).

## What's next

M0 → M8 are the spec-defined milestones (spec §14, lines 444–461).
Items below are operator verification still on the M8 board plus
explicitly open issues that the doc otherwise implies are closed.

### M6

*(Closed — PR #60 closes #57. See [M6](#m6--builderd--real-image-pulls-) above.)*

### M7

- ~~**`cmd/meterd/main.go` wiring** — `defaultDeps` leaves `parker`
  and `stripe` nil.~~ **Closed by PR #69** (`worktree-harden-meterd`).
- ~~**`pkg/stripex/usage.go::PushUsageRecord`** — `nil`-returning
  `TODO stripe-go`.~~ **Closed by PR #69.**
- **Provider-pluggable billing (Polar default; Paddle/Stripe compatibility
  paths)** — see the M7 body above. Customer-facing plan and usage surfaces
  use the provider-neutral `billing.Provider` contract; do not infer the
  active provider from this historical milestone section.

### M8

- **TLS edge cutover** — production TLS terminates at the upstream
  Caddy/Cloudflare edge and forwards trusted plain HTTP to
  `faas-gatewayd-public.service`. The old CertMagic-in-`gatewayd-public`
  work is retained in ADRs and legacy alert runbooks for archaeology; the
  supported operator procedure is
  [`docs/ops/gatewayd-public-tls-cutover.md`](ops/gatewayd-public-tls-cutover.md).
- **§14 V2 latency driver** — 100 platform-only park→wake cycles per app class,
  p95 < 350 ms from capacity admission/`wake.boot_started` through the first
  upstream byte on the reference SSD node. The reusable
  `scripts/ops/wake_performance_gate.py` reports the full-wake and raw-restore
  p50/p90/p95/p99 distributions and rejects incomplete runtime cohorts. CDN,
  Internet and client-distance timing stays outside this gate. Reference-SSD
  execution is recorded here
  when the native x86_64 metal acceptance run is available. Runs on
  `make test-metal RUN_REGEX='TestDeployWakeMetal'`.
- **Documented timed restore drill** — §14 M8: PG + one app back
  serving on a clean VM < 30 min, recorded as executed. Run
  `sudo make backup-restore-drill` on EX44; it writes
  `docs/drills/<UTC-date>-<HHMMSS>-restore-drill.md`. Apply the `m8-done`
  label only after that PASS artifact is committed; CI rejects missing,
  placeholder, or older-than-30-day evidence.
- **Status page + SLO dashboard** — public SLOs from spec §12
  (API 99.5 % monthly, wake p95 < 1 s, build success ≥ 99 %).
  Pipeline (Prometheus scrape + Grafana JSON + `apid /status` +
  `apid /status/slo.json`) in via PR #51; Grafana provisioning +
  D1–D5 threshold fixes + M1/M2 panels via PR #156 (ADR-031).
  Operator verification (Grafana panels render non-zero data, SLO
  JSON returns denominators) is the reference-node follow-up.
- **§11 checklist item-by-item sign-off** (cgroups v2 only,
  `unprivileged_userns_clone=0`, auditd, unattended-upgrades,
  etc.). The IPv6 egress item (ADR-023) is now in via PR #51;
  remaining items are operator verification on a reference node.
- **Gate-A runbook** — 2nd-node active-passive (founding doc R3).
- **M2 / M5 §14 metal gate sign-off** — the body/trim fixture
  mismatch flagged in PR #55 is resolved at the code level
  (PRs #151, #159, #135). The remaining item is a clean-checkout
  `make test-metal` run on the native acceptance host recording the gate green.

### Container service follow-ups

- **Replica placement spread** — service replica admissions now ignore the
  app's sticky-warm hint and choose by current fleet headroom, so a two-node
  fleet can keep desired replicas on separate compute nodes. The scheduler
  coverage lives in `TestConvergeServiceReplicasSpreadsAcrossComputeNodes`.
- **Replica lifecycle observability** — schedd now exports the bounded
  `schedd_service_replicas{app,state}` gauge with desired, ready, starting,
  draining, and unavailable capacity. Terminal and parked history rows are
  excluded from the live projection, so operators can see rollout or
  recovery shortfalls without inferring them from scheduler logs.
- **Workload networking** — the gateway exposes a deterministic cross-VM
  service endpoint registry (ADR-167), a trusted node-local service proxy
  (ADR-168), and a tenant-bridge guest listener with HostIP caller binding
  (ADR-169). ADR-170 adds node-local DNS for `<slug>.svc.gregale`, backed by
  the same `HostBridgeIP:10080` proxy; the netns firewall admits DNS and proxy
  traffic before the lateral-movement deny. Named TCP public multi-port routing
  now uses the `app--port-<name>` selector and the existing vmmd bridge
  (ADR-176); durable node-local host-port leasing now reserves declared TCP
  and UDP listeners (ADR-177). Opt-in raw TCP ingress with optional
  per-listener TLS termination and opt-in UDP ingress (ADR-389) shipped with
  the stateless container service (#4024, 2026-10-01). Both are disabled by
  default, and their native acceptance is pending
  ([container compatibility](container-compatibility.md)). Loopback discovery
  within one task remains supported (ADR-164 and ADR-165).
- **Resource and cost isolation** — named RAM/CPU profiles, per-node vCPU
  admission, ephemeral disk ceilings, and the account-level compute + S3 +
  managed-PostgreSQL usage projection are present; runtime per-container
  CPU/memory fences are enforced in host and guest cgroups. Sidecar roots are
  read-only and their only platform-owned writable path (`/tmp`) now has an
  explicit tmpfs ceiling derived from the sidecar RAM profile, with a safe
  64 MiB default for inherited profiles. ADR-175 adds a customer-selectable
  16..512 MiB sidecar scratch quota and named per-workload guest `io.weight`
  policies (`low`, `standard`, `high`); omitted values preserve the inherited
  defaults. Persistent volumes are out of scope for the stateless container
  service ([container qualification](container-qualification.md)); public
  named TCP listeners are now covered by ADR-176 and host-port allocation by
  ADR-177.

### Open security & infrastructure issues

These are still open on GitHub. Earlier revisions of this file
sometimes implied they were closed; they aren't.

- **#695** — Flip `apps.require_authn` global default to `true`
  (Cloud Run `--no-allow-unauthenticated` parity). Migration
  `00156` landed with a grand-father stamp on every pre-flip
  row + one batch audit row; new apps stamp the per-plan default
  via `api.Plan.RequireAuthnDefault()` /
  `api.Plan.PublicAuthModeDefault()` at `apid::buildApp` time.
  Per-plan truth table: Free `false/"open"`, Hobby `true/"open"`,
  Pro `true/"bearer"`, Scale `true/"bearer"`. Hobby stays on
  `"open"` because bearer scope is gated off at that tier — a
  bare-bearer-default without a usable scope would strand the
  customer. Existing apps are reachable exactly as they were at
  flip-time; opt-out per app via
  `gregale app <slug> --no-require-authn`. Operator dashboard renders
  the migration banner on the account view when
  `count(apps where auth_default_flipped_at is not null) > 0`.
  See ADR-080.
- ~~**#254** — App logs SSE stream is a stub (tier-1 ship-blocker).
  Move 4: per-instance ring + schedd StreamAppLogs RPC + vmmd
  Logs RPC end-to-end. Consumer side (Move 3, PR #291) shipped.
  Producer side (per-instance ring + schedd fan-out + vmmd Logs
  RPC + apid handler) shipped in this PR; ADR-043 records the
  decision. The follow-up PR wires the production schedd
  StreamAppLogs in cmd/apid so the apid stub is replaced by the
  real fan-out.~~ *(closed by Move 4 — see ADR-043)*
- **#144** — `NftResetCommands` missing ip6 reset (snapshot-restore
  Wake fails on second add).
- ~~**#146** — host egress chain deny lines were dead-code; the
  forward-chain ordering fix in PR #128 / #151 closed the original
  bug, and the remaining audit (shared catalog provenance + OCI
  6to4/Teredo + cross-renderer invariant + generated operator
  artifact) closed in PR-D. See `docs/denylist.md` and
  `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet`.~~ *(closed by PR-D — moved to the closed list below)*
- **#147** — `stripeWebhook customer.subscription.updated` should
  validate `Plan` via `api.Plan.Valid()`.
- **#148** — `bootstrap.sh` should pin the Go toolchain via
  SHA-256 (closes a toolchain-supply-chain gap; sister to #143,
  which is closed). RETIRED 2026-08-15 by issue #911 / PR-1:
  the v1 bootstrap.sh is a tombstone now; the v2 path is the
  renderer + release install (PR-2 + PR-3). The toolchain-pinning
  gap lives on in the cd-controlplane.yml / `make build` paths.
- **#145** — streamed OCI blob SHA-256 verification against the
  URL-path digest (spec's digest-pinned immutability).
- **#125** — `sqlc-check` in the CI bundle to prevent sqlc source
  drift.

#### Closed via PRs (full audit entry in PR-D)

- **#146** — closed by PR-D. See `docs/denylist.md` and
  `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet`.
- **#90** — document `/v1/*` as a permanent platform path
  reservation (issue #85 follow-up).

## Wave 0 — stateless-only contract. 🚧 (PR-C draft pending review)

G13 ("the platform is stateless-only but nothing encodes it") is
closed across three PRs and an ADR; PR-C is staged as draft PR #421
and will flip to ✅ once the review-blocking items clear.

- **PR-A (PR #413, merged):** deploy-accept gate. New RFC 7807 code
  `CodeStatelessOnlyViolation` (422); apid tarball scan rejects
  `VOLUME` / `mkfs.{ext4,xfs}` / `mount -t ext4/xfs` / top-level
  `data/`+`db/` before any build slot is consumed;
  `pkg/imaged::buildImageLayer` consults `pkg/imaged/base.go::
  StatefulDenyListMatch` against the resolved OCI ref
  (postgres / redis / mysql / mariadb / mongo / cockroach /
  cassandra / clickhouse). Both paths feed
  `pkg/oci::SentinelToCode` → `deployments.error_code`.
- **PR-B (PR #417, merged):** customer-facing pattern. Four
  `gregale init --template={s3-uploader, slack-bot,
  rest-api-postgres, cron-worker}` templates plus
  `docs/storage.md` teaching the managed-service pattern.
- **PR-C (ADR-047, draft PR #421):** runtime advisory. Guest-init
  `fanotify` mark on the closed path set (`/data`, `/db`,
  `/var/lib/postgresql`, etc.) → debounced 1s per
  `(path, mask-set)` batch → AF_VSOCK DGRAM
  (`port=1025, msg_type=2`, distinct from
  `VsockResumePort=1024`) → vmmd gRPC client
  (`/run/faas/apid.sock`, first vmmd-issued gRPC client) →
  apid `pkg/audit.Auditor.Emit("stateless.advisory", ...)`.
  Surfaced via `GET /v1/audit-events?kind_prefix=stateless.advisory`
  (plus `?app_id=` and `?include_anonymous=` for the dashboard
  drill-down), the new `db.NotifyStatelessAdvisory` SSE channel,
  `gregale audit-events [--kind-prefix=…] [--app-id=…]
  [--include-anonymous]`, `gregale tail --include-stateless`, and the
  dashboard `app_detail.html` "Stateless advisories" link into
  `/dashboard/audit-events?kind_prefix=stateless.advisory&app_id=…`.
  Advisory-only — spec §17 G13 explicitly forbids EROFS for Wave 0.
  vmmd default-local stays DB-less
  (`Manager.advisoryClient == nil` short-circuits to a no-op).

The three-ship-blockers contract: PR-A blocks at deploy time,
PR-B teaches the right pattern, PR-C makes the runtime failure
visible. Wave 1 follow-ups: EROFS on state-shaped paths (gated
on PR-C telemetry showing customer misuse), apps_list "warnings
detected: N" badge (deferred — backend count endpoint not in
this PR's scope), and vmmd-side `vmmd_node_id` stamp on the
audit row for traceability (currently `actor='apid'`).

## M8 — Wire-protocol selector: bridge-side H2C terminator (ADR-126). ✅

Closes spec §17 G19. PR #1023 / ADR-124 landed the customer-facing
`apps.app_protocol ∈ {http1, http2, grpc}` knob (closed-set
validation, default `http1`), and `gatewayd-internal` stamps
`x-faas-protocol` for downstream observability, but the framing
the guest's `:8080` actually received was still HTTP/1.1 + chunked
regardless of the customer's choice. This PR closes the wire-shape
follow-on: every `app_protocol ∈ {http2, grpc}` customer now
reaches their guest's `:8080` as native H2 frames with native gRPC
trailers — no bridge-side re-framing, no H1 downgrade inside the VM.

- **Bridge-side framing switch (ADR-126 §Decision 1).** New
  `cmd/vmmd-stream-bridge/h2c_terminator.go::handleH2CStream`
  originates HTTP/2 prior-knowledge frames to the guest via
  `golang.org/x/net/http2.Transport{AllowHTTP:true}`; HPACK state
  owned by the transport, no hand-rolled framing. The legacy
  `writeH1RequestHead` + `writeChunkedBody` path stays verbatim as
  `handleH1Stream` — zero behavior change for `app_protocol=http1`.
  Per-stream dispatch via `FAAS_BRIDGE_PROTOCOL ∈ {h1, h2c}` env
  var (read per-request in `framing.go::currentBridgeFraming()`,
  mirrors the per-request `FAAS_STREAM_BRIDGE_VERSION` pattern).

- **Guest-side listener opt-in.** Every shipped runner
  (`go124`, `node22`, `node24`, `python312`, `python313`) routes
  through the shared `guest/runners/internal/h2c_listener.go::ListenAndServeH2C`
  helper. The helper opts into H2C via stdlib
  `srv.Protocols.SetUnencryptedHTTP2(true)` (Go 1.24+; replaces the
  deprecated `golang.org/x/net/http2/h2c.NewHandler`) AND
  `SetHTTP1(true)` — the latter is load-bearing because stdlib's
  `Protocols` struct starts with NO protocols set, so calling only
  `SetUnencryptedHTTP2(true)` breaks every H1 caller with
  `connection reset by peer`. Caught by
  `TestH2CListener_H1Fallback`.

- **Wire-additive proto bump (ADR-016).** `ForwardHTTPRequestInit.app_protocol = string`
  (new field 7) carries the per-app protocol from vmmd to the
  bridge; `ForwardHTTPResponseInit.trailers` (new repeated field 7)
  carries gRPC trailer HEADERS forward. No SQL migration; the vmmd
  closed-set validation lives at the customer-intent layer
  (`apid` rejects out-of-set values with
  `400 app_protocol_invalid` before the gRPC frame leaves apid).

- **Two rollback switches (ADR-126 §Decision 7).**
  `FAAS_BRIDGE_PROTOCOL=h1` on the bridge process (per-vmmd
  surgical, forces legacy H1+chunked for any app_protocol), and
  `FAAS_STREAM_BRIDGE_VERSION=v1` on vmmd (wholesale shell-bridge
  fallback, pre-existing ADR-028 amendment). Both are env-var
  only, no restart of unrelated processes.

- **Snapshot invalidation is contained.** Every app adopting
  `app_protocol ∈ {http2, grpc}` must adopt the new h2c-capable
  base image (via the next `make images-push` cycle); the
  `app_protocol=http1` slice stays valid forever (ADR-005 cold-boot
  fallback handles the rebuild transparently).

- **Coverage:** `pkg/vmmdgrpc/forward_v2_internal_test.go` (closed-
  set translator + env-var derivation, 6 sub-tests); `cmd/vmmd-stream-bridge/framing_test.go`
  (per-stream env lookup + shared helpers + hop-by-hop trim);
  `cmd/vmmd-stream-bridge/h2c_terminator_test.go` (unary + gRPC
  trailers + dial-failure + dispatch); `guest/runners/internal/h2c_listener_test.go`
  (H2 prior-knowledge + H1 fallback + PORT env override);
  `cmd/e2e/bridge_h2c_terminator_e2e_test.go` (real bridge binary
  against a loopback H2C guest listener); metal sibling at
  `cmd/e2e/bridge_h2c_terminator_metal_test.go` documents the
  §14 M8 row 5 acceptance gates for `make test-metal`.

- **Operator-facing changes:** zero. The closed-set validation,
  per-app opt-in, and per-stream framing dispatch are all
  transparent to existing customers — `app_protocol=http1` rides
  the legacy path verbatim; `app_protocol ∈ {http2, grpc}` opts
  into the new wire shape by changing one customer-side CLI flag.

## M8.6 — Wire-protocol selector hardening (ADR-127 / G19.1). 🚧

Hardening follow-on to ADR-126 / G19 (the wire-shape shipped in
PR #1050). The post-merge audit run the same day surfaced four
production-readiness gaps that the shippable surface did not
address; this PR closes three of them and ships the docs + ops
material for the fourth. **Layer 12 (metal acceptance) is
deliberately out of scope** and lands in two follow-on issues
(G19.2 un-stub `cmd/e2e/bridge_h2c_terminator_metal_test.go`; G19.3
add `deploy/ansible/roles/metal-h2c-acceptance/`).

- **Layer 6 — snapshot invalidation on `app_protocol ∈ {http2, grpc}`
  adoption.** New `pkg/fcvm.FAAS_BASE_IMAGE_VERSION = "v1"` stamps the
  h2c-capable base rootfs (mirrors `Snapshot.FCVersion` per ADR-005;
  apps adopting the new wire-shape must adopt the new image). New
  `pkg/state.PgStore.MarkAllSnapshotsStaleByAppProtocol` + `MarkSnapshotStaleByAppProtocol`
  (mirror the F2 bulk + single-row patterns at `pkg/state/pgstore.go:9739`
  and `:9654` respectively). New `pkg/imaged.Handler.MarkAppProtocolSnapshotsStale`
  caller, invoked by `pkg/imaged/loop.go::runFCSweep` after F2 (the
  existing FC-version sweep); emits `warm_snapshot_stale` audit kind
  with subject `"app_protocol:v1"`. `app_protocol=http1` rows stay
  valid forever.

- **Layer 9 — bridge listener + transport security pins.** Three
  outbound transports (`cmd/vmmd-stream-bridge/h2c_terminator.go::newGuestH2CTransport`,
  `pkg/vmmdgrpc/forward.go::newStreamBridgeH2CTransport`,
  `pkg/gateway/internal_proxy.go::newInternalProxyH2CTransport`)
  carry `MaxReadFrameSize=1 MiB` + `MaxHeaderListSize=1 MiB` +
  `StrictMaxConcurrentStreams=true`. Inbound bridge listener
  (`cmd/vmmd-stream-bridge/main.go::srv`) wraps the handler with
  `golang.org/x/net/http2/h2c.NewHandler` (stdlib `Protocols.Get("h2")`
  has no per-protocol knob exposure; the wrapper is the canonical way
  to set per-protocol server limits) and pins
  `MaxConcurrentStreams=100` + `MaxReadFrameSize=1 MiB` +
  `IdleTimeout=60s` + `ReadIdleTimeout=30s` + `PingTimeout=15s` +
  `WriteByteTimeout=30s`; server-side `MaxHeaderBytes=1 MiB` via
  `api.DefaultMaxHeaderBytes`. `handleH2CStream` now has two
  `defer`s at the top of its body: `defer recover()` (logs the
  panic with stack + writes `500 bridge panic: ...` so a guest-side
  bug cannot crash the bridge) and `defer transport.CloseIdleConnections()`
  (so transport leaks do not pin guest-side fds).

- **Layer 7 — bridge observability.** New
  `pkg/wire.OpsMetrics.bridgeFramingTotal *prometheus.CounterVec`
  registered as `vmmd_bridge_framing_total{app_protocol, bridge_protocol, framing}`;
  pre-instantiated across the closed cross-product (3 × 2 × 2 = 12
  series) at boot so the dashboard renders a zero row from idle
  fleet (`TestOpsMetrics_BridgeFramingTotal_PreInstantiated`).
  `framing ∈ {match, mismatch}` where `mismatch` means
  `bridge_protocol` ≠ the canonical `appProtocolToBridgeProtocol`
  translation — the operator-forced surgical-rollback signal.
  `cmd/vmmd-stream-bridge/main.go::newHandler` now emits an
  `slog.Info` framing-selection line on every request with
  fields `framing`, `app_protocol_env`, `guest`, `method`, `path`
  (Info not Debug because `FAAS_BRIDGE_PROTOCOL` env flip is the
  operator's primary rollback signal). New dashboard
  `deploy/grafana/bridge-protection.json` (UID
  `faas-bridge-protection-adr-127`, schemaVersion 39) with four
  panels: framing rate by (app_protocol, bridge_protocol, framing),
  MISMATCH rate with red threshold 0.1 ops, active
  `bridge_protocol=h1` count on `http2|grpc` apps, and bridge H2C
  handshake latency p99. Companion Prometheus alerts at
  `deploy/ansible/roles/prometheus/files/bridge.rules.yml` under
  `family: bridge`: `FaasBridgeFramingMismatch` (warn, `> 0.1 ops`
  for `1h`) + `FaasBridgeRollbackStuck` (page, mismatch rate sustained
  for `4h`). Runbook at `docs/ops/h2c-rollback.md`.

- **Layer 11 — docs parity.** Spec §4.1 line 115 above this row is
  rewritten to drop the pre-ADR-126 "filed in §17 G19 as a multi-week
  follow-on ADR" language and surface both ADR-126 (wire-shape in
  fleet) and ADR-127 (hardening follow-on) as the load-bearing pair.
  §17 G19 row stays `RESOLVED` — ADR-126 is the close, ADR-127 is
  the hardening overlay. `docs/ops/h2c-rollout.md` + `docs/ops/h2c-rollback.md`
  shipped next to `docs/ops/secrets-rotation.md` cover the rollout
  + two-switch rollback + escalation policy.

- **Out of scope (filed as follow-on issues).**
  - **G19.2** — `cmd/e2e/bridge_h2c_terminator_metal_test.go`: un-stub
    5 metal tests. Replace unconditional `t.Skip(...)` lines with
    `metalAvailable(t)` gates using the canonical pattern at
    `cmd/e2e/deploy_override_port_metal_test.go:165`. Build tag
    `//go:build metal` preserved. Depends on G19.1 + G19.3.
  - **G19.3** — `deploy/ansible/roles/metal-h2c-acceptance/`. The
    opt-in role is now implemented with the five-app fixture contract,
    KVM/x86_64 preflight, root-owned secret environment, non-root oneshot
    harness, and five named §14 M8 row 5 gates. It is invoked only through
    `deploy/ansible/metal-h2c-acceptance.yml`; the normal production
    `bootstrap.yml` remains unchanged. G19.2 still owns enabling the Go
    metal tests once a real acceptance host is available.

## M8.7 — Public-release backend hardening foundation (ADR-140). 🚧

This slice closes three backend failure modes that are disproportionately
expensive in a public deployment:

- warm placement hints are committed only after instance creation and ledger
  admission;
- stale schedd residency data fails closed after 30 seconds, preserving only
  the guaranteed builder slot; and
- HTTP and raw gateway streams actively cancel and drain request-body copying
  on upstream failure or client disconnect.

Remaining public-release gates are tracked separately: durable event replay,
M9 two-node and leak-drill acceptance, service-replica convergence, real OTLP
export, and the remaining state/export scale work.

## M8 — Wake hot path: single pre-boot staging session (ADR-192). 🚧

Production wake timelines (2026-09-20, 10 samples on the GCP SSD nodes) put
the restore window labelled `stage_snapshot_ms` at a median of 88 ms and up to
355 ms, versus 26 ms in the rc.98 acceptance run. The cost was not snapshot
I/O: `stagePreBootFiles` loop-mounted drive1 once per file (`secrets.env`,
`env.json`, service resolver, workload files). vmmd now writes them in one
session (`pkg/fcvm/vmm.go::stagePreBootFiles`, `loopMountSession`), and the
`wake.restore_breakdown` event splits `stage_pre_boot_files_ms` out of
`stage_snapshot_ms` and carries the Manager.Wake phases
(`lease_acquire_ms`, `env_prepare_ms`, `pre_network_ms`, `setup_network_ms`)
that previously reached only slog — the 6.6 s production wake had 5.2 s in
that unattributed gap. Open evidence: a `make test-metal` +
`make leakcheck` run on a native KVM host, then a re-run of
`scripts/ops/wake_performance_gate.py` to record the new
`stage_pre_boot_files_ms` distribution against the rc.98 baseline.

### Recoverable operator force-park (ADR-1525)

Force-park now has an immutable VM-generation execution journal, fenced claims,
verified capture/cleanup receipts and atomic terminal state, intent, audit and
snapshot publication handoff. Fresh traffic cancels before execution; a proven
stop requiring cold boot is distinct from a captured park and an uncertain
outcome. Reclaim never repeats a started VM effect. See
[operator park recovery](ops/operator-park-recovery.md) for coordinated rollout.
Targeted portable memory/PostgreSQL, scheduler, manager, RPC, API/CLI and race
checks pass, as do differential lint, SDK validation, OpenAPI parity and schema
parity. Vmmd and the metal tests compile; the new metal tests skip without
staged VM images. Native lifecycle qualification remains pending: this workspace
has no `/dev/kvm`, and leak inspection fails because `/dev/loop0`–`/dev/loop7`
are unavailable. Migration `20261017120000001` has not been deployed.
