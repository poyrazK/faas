# Product Control export pilot

This adds commercial configuration and the Gregale customer portal to the
existing CSV exporter. The exporter continues to use its ordinary HTTP Operation,
trusted workload identity and private artifact upload. Product Control follows
`customer-export@default` across source deployments; it needs no definition ID
in the browser and does not use the starter's development token form.

Use a dedicated internal app on an Operations-qualified native host. Keep
`FAAS_PRODUCT_CONTROL_ENABLED=1` restricted to the internal deployment. Configure
the Operations preview cohort for the app/environment and these three tenants,
workload trust and artifact storage before the trial. Apply platform migrations.
See [the Product Control runbook](../../../docs/ops/product-control.md) and
[the exporter deployment instructions](../README.md). No script here enables a
preview gate, deploys code, issues credentials or qualifies a host.

1. Initialize and deploy the existing exporter with its packed internal SDK.
   Keep `customer-export` in `gregale.yaml`. The handler works without
   `GREGALE_EXPORT_DEFINITION_ID`; that variable is only for the starter's
   standalone browser. Product Control resolves the deployed definition itself.
2. Create an account-owned Application Auth app with signup disabled. Invite the
   three internal test users and set passwords through the existing Auth flow.
   Each customer needs a separate Auth organization. Create organizations and
   memberships using the [Application Auth SDK](../../../docs/application-auth.md).
   No Auth admin key is passed to Product Control. For this full-access pilot,
   give each pilot user an owner/admin role in their own test organization.
   Ordinary members can only view work; see Customer team permissions below.
3. Register three platform tenants, for example
   `gregale platform-tenants add --external-ref product-pilot-alice --name Alice`.
   Repeat for Bob and Carol. Record their UUIDs and their Auth organization UUIDs.
4. Review `product.json`. It assigns Starter 10 starts/month at EUR 0.25 per
   success and Pro 20 at EUR 0.10. Set `PRODUCT_APP`, `PRODUCT_AUTH_APP`,
   `PRODUCT_{ALICE,BOB,CAROL}_TENANT` and `PRODUCT_{ALICE,BOB,CAROL}_ORG` in your
   shell, then run `bash configure.sh` using an owner CLI profile. Alice and
   Carol receive Starter; Bob receives Pro. All IDs must belong to this account.
   Alternatively, use the owner console's **Guided setup** with feature `export`,
   Operation `customer-export`, environment `default`, and these reviewed plans.
   Confirm publication, select the three existing tenants by name, and preview
   assignments with their existing Auth organization UUIDs. The setup flow
   preserves other catalog entries and refuses stale publication confirmations.
5. Open `https://API_ORIGIN/v1/product-control/portal?app=EXPORT_APP` in separate
   browser profiles and sign in using each customer's email/password. Confirm
   the correct plan, price and remaining starts before running the export.

For reviewed identity linking without copying organization UUIDs, use the owner
console's **Onboard a customer** flow after creating the Auth users/workspaces.
Sign in with each workspace owner's/admin's existing Auth account, select the
workspace by name and existing tenant, review its plan, and confirm. The existing
Auth invitation workflow supplies access; setup sign-in is not customer activation.
The activation page records a later customer workspace request and the first
verified success admitted after that setup. CLI setup is documented in the
[onboarding guide](../../../docs/ops/product-control.md#guided-customer-onboarding).

Record the deployed revision, Operation IDs, receipts and output hashes for
these checks. Use `gregale product explain OPERATION_ID --app EXPORT_APP --json`
and owner usage reads to compare commercial and execution evidence.

Run `gregale product doctor --app EXPORT_APP --tenant TENANT_UUID` for each
customer before the trial. It observes configuration on the responding API node;
it does not qualify the host, enable a cohort or verify live membership/storage.
Missing or unverified prerequisites include their next action.

Use **Customer readiness and activation** in the owner console, or
`gregale product customers overview --app EXPORT_APP --json`, to inspect one
bounded page of assigned customers together. Readiness uses current-month
allowances; work needing review includes older retained months. Guided activation
shows historical customer visits and verified success after setup. Manual
assignments without guided setup stay explicitly untracked. This overview does
not replace the live trials below or qualify native execution.

Use **View activity timeline** on a customer, or `gregale product customers
activity --app EXPORT_APP --tenant TENANT_UUID --json`, to inspect retained setup,
plan, work and billing records together. Expand the source details to verify a
run's original terms after a plan change, then follow its review link. Page older
records with the returned cursor. Legacy assignment and Auth team history are
not reconstructed; missing events do not establish failure or inactivity.

Use **Notification email**, or `gregale product email list --app EXPORT_APP
--tenant TENANT_UUID`, to inspect personal notification queue totals, backlog
age and delivery reasons. Counts cover all retained statuses; history filters
and pagination do not turn page counts into totals. After correcting a provider
rejection, requeue an eligible delivery with `gregale product email retry --app
EXPORT_APP --tenant TENANT_UUID --delivery DELIVERY_UUID --attempt N`. Repeat
the same values to recover a lost response. The original attempts, deadline,
provider key and current customer access checks remain enforced. Local mode
captures messages without sending email. See the [owner email controls
guide](../../../docs/ops/product-control.md#owner-notification-email-controls).

After obtaining three real successful exports, create and fill an explicit
manifest with `gregale product pilot init --app EXPORT_APP --feature export
--file product-pilot.json`. Fill `app_id` from `product doctor --json`, then each
customer's actual tenant, Operation and catalog revision IDs. Compare the expected
plans and charges with the reviewed terms before running `gregale product pilot
verify --app EXPORT_APP --file product-pilot.json --output pilot-report.json --json`.
For the complete pilot, set `version` to `2` and include a relative `output_file`
CSV and reviewed `output_sha256` for every baseline case under the manifest
directory. Merge the help and complete-journey fragments described below.
Keep native/browser receipts alongside this report. Version one remains available
for the earlier ledger-only workflow.

The verifier checks retained admission, confirmed quote/allowance, original prices,
terminal evidence or retained reconciliation state, and billing coverage. It
records local CSV hashes without contents. Optional help and journey evidence is
described below. Its exit 0 means requested comparisons matched and any requested
journeys were recorded; login/isolation, actual downloads, retry/redeploy behavior
and native qualification remain explicit unverified scenarios. The trials below
must still be performed. Verification never replays work or mutates billing.

| Trial | Expected evidence |
| --- | --- |
| Export 2 rows for each customer | Private `export.csv` has header plus two rows; each customer sees only their runs. One start and one success; prices 25,000 / 10,000 / 25,000 millicents. |
| Reload during a running export | The same Operation appears and progress resumes; no new submission. |
| Lose the start response, then reload | Receipt lookup finds the accepted Operation. If unresolved, the portal asks for the original inputs and an explicit retry with the saved key. |
| Open the same pending feature in two tabs | Request locks preserve one unresolved key. A stale tab discovers an acceptance rather than starting replacement work. |
| Reassign Carol to a reviewed higher-priced revision after she opens the page | Admission returns `product_quote_changed`, consumes no start, and requires another visible confirmation. |
| Cancel an accepted/running export | Cancellation remains cooperative. A confirmed cancelled run consumes a start and records no success charge. An uncertain dispatch needs reconciliation. |
| Execute a deliberately failing revision in this isolated pilot app | A verified failed run consumes one start and records no success charge. Restore the reviewed exporter before continuing. |
| Interrupt dispatched work through the existing native Operations failure lane | `requires_reconciliation`; no automatic retry or charge. Use the owner recovery inspection/preview and record a decision supported by actual output evidence. Confirmed success settles once at the original price. |
| Deploy a second reviewed exporter revision | New keys use the new serving definition, while old keys still refer to their original Operation and price. |
| Remove membership, suspend a tenant, or unlink its organization | Subsequent requests lose workspace access. Organization/realm mapping is rechecked at admission; no new customer authority is inferred from JWT claims. |
| Disable Product Control | Its customer/API surfaces return 404. Already admitted work still executes, recovers and settles its independent receipt. |

Keep a receipt's key and inputs unchanged while resolving uncertain work.
The portal's replay window is one day; it never automatically reruns work.
Commercial receipts are retained independently of result files. A missing or
expired result is not evidence that a run failed or can safely be replaced.

The portable suites exercise policy, cookies, organization mapping, quotes,
receipts and browser flows with a simulated Auth/execution boundary. The real
export, Auth deployment, private download and native interruption trial must be
recorded on the qualified host before opening this service to customers. Current
usage remains provisional; use the billing checks below to close a past month.
This service records invoice handoffs and never collects payment or refunds.


## Billing pilot evidence

During the live trial, use `product statements review` and `statements receipts`
for each of the three tenant IDs and the current UTC month. Verify successful
charges are 25,000 / 10,000 / 25,000 millicents, failures/cancellations are
nonbillable, and interrupted runs remain unresolved until verified or explicitly
waived. Capture the exporter output hashes, Operation IDs and billing review hash
alongside the native execution evidence. Attempting to finalize this open month
must return `product_billing_period_open` without creating a document.

Use an actual closed pilot month for the following owner commands; preserve the
original UTC admission month even when success arrived later. Never change a
host's clock or production receipt timestamps to force month closure.

1. Page every receipt. Reconcile verifiable unknown outcomes through Operations.
   Permanently waive an unverifiable pilot run only with an explicit reason and
   the commercial decision to forgo any eventual charge. Verify no execution is
   cancelled or restarted by that decision.
2. Review again. Finalization with the earlier hash must fail. Finalize with the
   current hash only when unresolved is zero. Repeat the request with that same
   hash after dropping its first response: the statement UUID must remain the same.
3. Issue a 5,000-millicent credit against one finalized successful export using a
   saved credit UUID. Repeat with the same UUID and arguments: only one note
   exists. Changed arguments, an over-credit or a credit against a waived run
   must fail. The original statement total remains unchanged.
4. Reserve one stable external reference per document with `statements handoff`.
   Drop a response, read `handoff-status`, and retry using the same reference.
   Another reference for the same document, or the same reference for a different
   document, must conflict. The credit note handoff must carry a negative amount.
   Use each document UUID as the invoicer's idempotency key before any invoice is
   created, and query the external provider after an uncertain response.
5. Sign in as each customer, select this month in **Statements and credits**, and
   verify only that customer's statement/credits appear. Logout while a billing
   response is in flight; it must not repopulate the workspace.
6. Disable/re-enable the internal flag. Billing routes must return 404 while off;
   documents and handoff references must remain unchanged after re-enabling it.

The portable three-customer/two-plan billing suite seeds historical receipts to
exercise closure immediately and checks an in-flight PostgreSQL settlement race.
It does not qualify the actual native exporter or a payment provider. The first
native trial can validate live execution and the open-month guard now; actual
period closure waits for that UTC month to end, or uses existing historical pilot
receipts. Full commands are in the linked Product Control runbook.

## Customer team permissions

Owners/admins receive work and billing permissions by default; members can view
work. Manage plan always requires an explicit grant, including for owners/admins.
Use **Customer permissions** in the owner console, or review and apply the
example policy:

```sh
gregale product access review --app EXPORT_APP --policy-file ./access.json --file access-review.json
gregale product access apply --app EXPORT_APP --file access-review.json
```

`viewer`, `operator`, `billing`, `plan-manager` and `team-manager` are example Application Auth custom role names.
Configure those roles in your existing Auth authorization configuration before
assigning them to members; applying this product policy does not create Auth
roles. Auth config replaces its entire role/policy configuration, so preserve
existing entries. Product permissions can be combined, and `work:run` includes
viewing activity and results. The policy replaces all product role grants and
affects every linked workspace on its next request. Existing customers retain
their pinned prices. Keep this internal behind the default-off flag.

## Self-service plan pilot

Run these drills after capturing the initial three-customer baseline and its
expected 25,000 / 10,000 / 25,000 millicent prices. Keep that original evidence.
Publish `plan-policy.json` through **Self-service plans**, or with
`product plan-policy review/apply` in the runbook. The policy offers both
Starter → Pro and Pro → Starter. It does not migrate any customers.

Grant Manage plan to the chosen pilot user's existing role in **Customer
permissions**, preserving the other role grants. The example policy grants
only `plan-manager` this authority; either configure that existing Auth role
first, or explicitly add `plans:manage` to a reviewed owner/admin product grant.

1. As Alice, compare Starter and Pro prices and allowances in **Manage plan**.
   Confirm Starter → Pro, save its reference and verify that prior accepted
   Starter runs still show 25,000 millicents. A future success uses 10,000.
   This month's used starts must remain the total across both plans.
2. Review Pro → Starter. Consume another export start before confirming that
   review; confirmation must require fresh review and consume no extra start.
   Confirm new terms. If used starts exceed Starter's cap, remaining starts are
   zero and admission refuses another export until a later UTC month or upgrade.
3. Drop a plan-confirm response. Reload and check the saved reference: it must
   recover the recorded receipt without posting another change. For an unrecorded
   attempt, review and explicitly confirm again with the saved reference. Change
   a plan through the owner console, then retry an earlier confirmed request:
   it must return its original receipt without restoring the earlier assignment.
4. Revoke Manage plan, remove membership, or change the organization mapping
   while a review is open. Confirmation must fail. Billing-only and default
   owner/admin access must not grant plan authority. Another workspace must not
   read Alice's receipt or history. Logout with a review response in flight;
   its terms must not repopulate the workspace.
5. Close all owner choices. No new transition is offered, and authorized users
   can still read recorded receipts. Disable/re-enable Product Control; plan
   routes and assets return 404 while off and retained history remains intact.

Capture actual Auth, browser, run and receipt evidence on the qualified host.
Portable tests verify policy and persistence; they do not complete this live
trial. Plan changes introduce no recurring fee, payment collection or proration.

## Customer account recovery

Account recovery can be implemented and checked locally while deployment access
is pending. On the product sign-in page, choose **Forgot password?** after entering
the product name. It opens the current owned Auth app's recovery page in a separate
tab. Enter the invited email there, explicitly request a link, reset the password,
then return to the product and sign in normally. Opening the link alone sends no
email and grants no product or workspace access.

During the eventual internal trial, verify actual delivery and the Auth app's
one-time/expired-link, password and session policies. Confirm a realm change uses
the new Auth app, arbitrary redirect/query selectors fail, credentials never
appear in the Product Control URL, and the disabled service hides the route.
Keep these deployed checks separate from local navigation fixtures. Full details
are in the [recovery runbook](../../../docs/ops/product-control.md#customer-account-recovery).

## Customer Help and complete customer journeys

Run these drills on the selected internal deployment after preserving the three
baseline successes. Help itself consumes no starts. Use the original selected
run for each customer; later plan changes must preserve that run's original
commercial terms. Keep each customer's browser profile and workspace distinct.

1. In each portal, use **Get help** on the baseline run. Prefilling must not send
   anything. Submit an explicit request, retain its ID and capture its initial
   context. The owner queue must show only requests for that product/customer;
   the customer must see only permitted requests in their workspace.
2. Capture the accepted request's text/context hashes using the owner CLI below.
   After a plan or binding change, the original request context must remain
   unchanged. An exact create retry must keep the same ID and context; changed
   details under that ID must conflict without creating another request.
3. From the owner queue, follow readiness/activity/run review links, then publish
   a customer-visible response and status. Resolve each baseline request after
   reviewing it. Check that the originating portal shows the actual latest reply
   and status. Concurrent owner edits with the same expected version must not
   overwrite each other; an older retry must not restore an obsolete response.
4. For one new request, interrupt the acknowledgement after acceptance. Preserve
   its reference and original details. Reload must recover by GET without another
   POST. Two tabs must retain the same pending reference. Browser recovery storage
   must contain only its scoped ID/hash, without text, diagnostics or credentials.
5. Use the reviewed role/revocation drill on existing pilot identities. Losing
   View work must hide run help; feature help needs Run work or View billing;
   workspace help needs a current product permission. Verify direct IDs and
   history pages, pending requests, logout and workspace switching. An old reply
   must not reappear after authority is lost. A changed Auth realm/organization
   mapping must not inherit earlier help. Preserve a workspace owner throughout.
6. Compare accepted starts and billing before/after help while no other work is
   submitted in that workspace. Help and status updates must change neither.
   Test text escaping and verify disabled-gate behavior during the existing
   operator rollback drill. Retain evidence; the verifier does not execute drills.

Inspect a scoped request without printing its message, response or diagnostics:

```sh
gregale product pilot help --app EXPORT_APP --tenant TENANT_UUID --request HELP_UUID --json
```

This is inspection, not an expectation check. Retain its initial
`message_sha256`/`context_sha256`; separately review the expected latest
`response_sha256`, `status` and `version` after the owner response. Do not replace
an earlier context expectation with a changed observation merely to make a test
pass. Merge the objects from [the help template](pilot-help.template.json) into
a new copy of the existing manifest and fill actual IDs and reviewed values.
Run cases must reference the same tenant's selected baseline/additional operation.
The report compares hashes and references and excludes all customer text.

For the complete pilot, merge [the version-two journey template](pilot-complete-journeys.template.json)
into that new manifest. Each journey links the baseline tenant, selected operation
and selected help request. Record `started_at` from the observer's sign-in start;
append chronological `steps` with the following shape:

```json
{
  "stage": "sign_in",
  "outcome": "observed",
  "observed_at": "2026-10-17T09:00:01Z",
  "evidence_file": "browser-evidence/alice-sign-in.json",
  "evidence_sha256": "REVIEWED_SHA256_OF_REDUCED_EVIDENCE"
}
```

The timestamp above is illustrative; record actual times from the same observer
clock. Stages are `sign_in`, `paid_feature`, `usage`, `plan_change`, `help_request`
and `owner_response`, followed by `inbox_update`. Record blocked attempts as `blocked`, then append recovered
observations rather than removing friction. In this journey, explicitly review
and confirm an offered plan change after the baseline success; compare carried
usage and original prices. A requested three-customer journey stays incomplete
until all three have every observed stage, matched help response and successful
selected baseline run evidence and both current inbox sources described below.
Version two requires help about that same baseline run. Evidence files must be nonempty regular files within the
manifest directory, at most 4 MiB each. Each journey has at most 32 observations.
Keep reduced artifacts free of credentials, HAR/cookies, invitation tokens and
customer-authored text. The report includes paths/hashes/byte counts, not contents.

`first_success_ms` measures observed successful work from sign-in start;
`blocked_steps` counts blocked observations. `help_resolution_ms`, when available,
measures creation to the latest resolved update of a matched help record, including
any reopenings. It does not reconstruct a first response or first resolution.
These are operator observations, not independently verified product benchmarks.

Run the existing `product pilot verify` against the new manifest with a new output
path. Ledger, help or artifact mismatches return exit 1. An incomplete requested
journey returns 3 unless a mismatch takes precedence; matched comparisons and
recorded requested journeys return 0. Legacy ledger-only manifests still work.
Every report keeps live browser scenarios and native qualification unverified;
retain the separate source/deployment-qualified trial records to assess release.

## Customer Updates inbox pilot

Use each existing customer's real browser session after completing their baseline
run and resolving help about that run. Keep Product Control internal behind its
existing default-off gate. Record reduced observations for these checks:

1. Open **Updates** and confirm the selected successful run and latest help reply
   or resolution. Their links must open the existing readers without marking
   anything read or starting work. Record the actual notification page response
   body through that browser session, without headers/cookies. The
   [response shape](pilot-inbox.template.json) is a blank aid, not live evidence.
2. Explicitly mark read and unread. Confirm **Unread** filtering and the shared
   workspace label. A second authorized member of that same workspace sees the
   same read metadata. A later owner help update must become unread again; a
   stale revision must not overwrite it. Refresh and record the latest page
   after any owner edit before verifying the manifest.
3. Lose an accepted read acknowledgement and reload. Recovery must read current
   state with GET without replaying the POST. Record request counts and retained
   state without credentials. Work starts, prices, help contents and billing
   must remain unchanged while no other work is submitted in that workspace.
4. Exercise current permissions and direct IDs using the reviewed pilot roles.
   Work and run help need View work; feature help needs Run work or View billing;
   workspace help needs a current product permission. Another tenant/app or a
   new organization/Auth realm mapping must not inherit private items or read
   marks. Logout, switch workspaces and revoke access with a response in flight;
   private data must clear and remain cleared. Preserve a workspace owner.
5. During the existing rollback drill, disable Product Control on this selected
   internal deployment. Its private notification routes and assets must hide;
   restore the reviewed state and confirm retained source/read metadata remains.
   This inbox sends no email and is a rolling source projection, not a history
   of every intermediate update.

Append chronological `inbox_update` steps with the actual response body file and
reviewed SHA-256. Each observed page must be at most 128 KiB and must belong to
that journey's app/tenant. Record multiple page observations if necessary to
cover both selected sources. Read metadata can be read or unread; reading an
item is not a condition of completing the stage. A blocked attempt instead
references a reduced failure artifact within the ordinary journey bound.

The verifier compares current work/help source kinds and microsecond timestamps
and checks the page's shape/scope. It cannot verify the page was captured in a
real session, that members saw it or that read recovery/isolation worked. Retain
those actual browser records separately. Every local report leaves these live
checks and native qualification unverified. Start the trial only after selecting
the internal API/access profile and qualified host; do not use the public default.

## Usage guidance pilot

Preserve the three baseline exports, original price evidence and completed plan
drills before consuming the remaining allowance. Use the current assigned limit
and actual accepted-start count, including earlier failed, cancelled and pending
runs. The following 8/10 examples assume Alice still has the reviewed Starter
grant of ten starts. Do not reassign a smaller quota or rewrite receipts to make
an existing customer's counts fit the example. Keep extra trial work in these
dedicated internal workspaces.

1. Sign in through the real portal and inspect **Usage and next steps**. Compare
   the assigned revision, used/remaining counts and feature availability with
   the product response's `guidance` and a cookie-authenticated
   `GET /v1/product-control/customer/EXPORT_APP/guidance` using the selected
   `X-Product-Organization`. Capture a reduced record of these fields without
   credentials, cookie headers, inputs, results or a raw network archive. All
   query selectors, including `tenant_id`, `period` and `role`, must be rejected.
2. Bring Alice to eight accepted starts in the current UTC admission month,
   counting her baseline and every previous attempt. Verify the 80% warning,
   `8 of 10 starts used`, two remaining and the progress meter. At ten accepted
   starts, verify zero remaining, the exhausted explanation and a disabled new
   Run button. An additional fresh submission must be rejected without another
   admission. Record the exact reset: 00:00:00 UTC on the first of the next
   month. Do not change host clocks or receipt timestamps to simulate rollover;
   an observed real rollover remains pending until it occurs.
3. With a near-limit view open, use a different workspace to verify its own
   allowance. Logout or switch workspaces with a refresh pending; the old
   counts, warning and action must stay cleared. Use a Run-only role to verify
   allowance guidance without billing amounts, a Billing-only role to verify
   the permission explanation, and a View-only or Team-only role to verify that
   no feature counts are returned. Remove membership and verify subsequent
   guidance requests are denied. Restore access only through a reviewed action.
4. To exercise the warning's plan-review button, configure a trusted pilot role
   with both `work:run` and `plans:manage` in a copy of the existing access policy,
   preserving other grants and Auth role definitions. The example's separate
   `operator` and `plan-manager` roles do not individually provide both. With
   Starter → Pro explicitly offered, the button must load current choices and
   prices without previewing, confirming or changing the plan. Confirm only
   after the existing explicit review. Verify the new limit retains this
   month's used starts and earlier runs retain their original charge terms.
   Without Manage plan, show administrator guidance; without an offered
   transition, show product-team guidance and no plan-review button.
5. Through the existing owner review flows, pause new runs for the selected
   workspace and verify the paused explanation, then restore its prior state.
   Inspect the existing missing-binding/deployment drill for the temporary
   unavailability explanation. Neither state should suggest that changing
   plans repairs access or deployment. If testing a zero grant, create and
   review a dedicated pilot plan explicitly; its explanation must say the plan
   includes no starts, rather than promising that a reset enables execution.
6. Carry an accepted submission with a deliberately lost acknowledgement into
   an exhausted-allowance view. Reload must recover the same Operation through
   read-only lookup without another start. Interrupt a guidance refresh and
   verify that old guidance is replaced by an unavailable message. A delayed
   older refresh must not replace newer counts or restore an obsolete action.
7. During the existing flag rollback drill, include both the guidance endpoint
   and `/v1/product-control/portal-guidance.js`. With Product Control disabled,
   GET, POST and OPTIONS must return 404 with private/no-store behavior. Record
   flag changes and restore the reviewed internal state through the deployment's
   normal operator workflow.

Record these as live browser/API scenarios separately from `product pilot
verify`: its ledger comparison does not certify guidance, membership, browser
fencing or real month rollover. The [usage guidance contract](../../../docs/ops/product-control.md#usage-and-next-steps)
describes the shared projection. Retain each drill's actual deployment revision,
customer/Operation references, timestamp and reduced evidence. Pending drills
remain pending; portable fixture results are retained separately.

## Customer team pilot

Preserve the original baseline and completed plan-change evidence first. Upgrade
the existing Auth app using the new template and its normal release task; this
appends migration `0003_reviewed_team_actions.sql`. Explicitly enable its
default-off `AUTH_PRODUCT_CONTROL_TEAM_ENABLED=1` flag as well as the existing
apid Product Control flag. Configure a trusted `team-manager` Auth role with
`org:read` and `members:manage`, preserving existing Auth roles/policies.

Review/apply `access.json` only after preserving your current role grants. It
adds the separate `team:manage` permission for `team-manager`, without work,
billing or plan access. Review/apply `team-policy.json` using **Team roles** or
`gregale product team-policy review/apply`. Its owner delegation is inactive
until you explicitly grant that role Manage team. Auth `members:manage` is
workspace-wide authority on existing Auth APIs; delegation only limits the
Product Control surface. Choose trusted pilot managers and review all connected
products that use this organization.

1. As the team manager, load **Team**. Check the member list and effective
   permissions. Only delegated roles may be assigned or changed. A default
   owner/admin, billing-only member and platform service token cannot use this
   team boundary. A team-only manager must receive no work or billing data.
2. Invite a test email using an approved role. Review both product and workspace
   permissions, confirm, then accept through the hosted Auth invitation page as
   that confirmed email. Wrong/unconfirmed emails, expired/revoked invitations
   and a second acceptance must fail. Check that membership appears in the team
   list after reload. Verify actual configured-provider delivery separately from
   the no-provider one-time link workflow.
3. Change a member's role and remove that member through separate reviews. Own
   role/removal controls must be absent. Delegation must permit both current and
   target roles. Auth must retain an owner, including under competing owner
   changes. Capture actor, before/after roles, time and action references in
   history; no invitation token may appear there.
4. Drop a confirming response. Reload and check the saved reference; only GET
   recovery is allowed. A recorded action must not repeat. For a lost manual
   link, revoke the invitation and explicitly create a new one. If unrecorded,
   review and confirm again using the original UUID. Change a role elsewhere,
   then retry an older recorded confirmation: it must not restore that role.
5. Change Auth membership, role definitions, product delegation, product grant
   or customer mapping with a review open. Confirming must reject changed state
   observed before dispatch. An authorized request can finish across a later
   change in the other service; capture this cross-database boundary. Logout or
   revoke access with team/history responses pending; private data and links
   must stay cleared. Another workspace cannot read these action receipts.
6. Disable each feature flag separately. Product routes/assets must return 404
   with apid off; Auth reviewed routes must return 404 with Auth off and Product
   Control must report team management unavailable. Re-enable and verify retained
   memberships/history. Do not describe disabling a flag as revoking invitations.

Record the actual Auth migration/deployment, configured email provider, browser,
invitation acceptance and action receipts on the qualified host. Portable tests
and a source-bound baseline report do not complete these live drills or qualify
a new source version for release.

### Internal personal email notifications

The customer portal now has personal Work outcomes and Help replies opt-ins,
plus delivery history. They start disabled. Local implementation uses a private
mail sink and fictional verified addresses; it needs no live provider credential.
The Auth app must include the new recipient-grant migration/runtime and its
separate default-off email protocol flag. Keep both Product Control and real
email sending internal. Follow the [email runbook](../../../docs/ops/product-control.md#personal-notification-email)
for local mode, consent renewal, queue limits and the later provider setup.

Signed provider feedback now adds delivered, bounced and complained states to
portal history, owner **Notification email** and `gregale product email list`.
Delivered means the receiving mail server accepted the message; it does not
prove inbox placement or reading. A permanent bounce or complaint pauses the
affected personal subscription. Saving preferences or retrying a message cannot
resume it. A separate customer action can recover a hard bounce after verifying
a different address and renewing consent; complaints remain under review. Other
subscribers are unaffected.

Use signed loopback fixtures to verify duplicate/out-of-order callbacks and
pauses without live credentials. The optional Product Control webhook secret
is separate from sending configuration. Keep all real switches off until live
qualification. See the [feedback and pause guide](../../../docs/ops/product-control.md#provider-delivery-feedback-and-paused-subscriptions).

### Internal notification recovery

After verifying a replacement sign-in address, the customer can explicitly
resume a hard-bounce subscription in the portal. Only future updates become
eligible. Same-address recovery, complaint holds and legacy pauses without
address evidence stay blocked. Late bounces for the previous recipient cannot
pause a recovered replacement; a complaint still establishes a review hold.

Inspect confirmations through owner **Notification email → View notification
recovery history** or `gregale product email recoveries --app EXPORT_APP
--tenant TENANT_UUID`. An earlier receipt does not prove sending is currently
active. Keep the existing feature and real-send flags off while testing with
fictional addresses and signed loopback callbacks. Follow the
[recovery runbook](../../../docs/ops/product-control.md#recovering-notifications-after-a-hard-bounce).


New personal notification emails include a confirmation link to unsubscribe
without signing in, plus mailbox one-click POST headers. Opening a link does not
change preferences. Confirmation stops both notification categories for that
recipient and workspace and cancels queued mail; a request already being sent may
still complete. Older links cannot override newer consent. Customers can inspect
personal opt-out history in the portal and enable only future updates by saving
fresh consent. Owners read scoped confirmations with
`gregale product email opt-outs --app exports --tenant CUSTOMER_UUID`; they cannot
resume personal notifications. Feature flags remain default off. See the
[unsubscribe runbook](../../../docs/ops/product-control.md#unsubscribing-from-notification-email).

### Customer activity timeline

With Product Control enabled in an isolated internal environment, open the
portal's **Updates → Workspace activity**. Confirm that a permitted member can
follow a work outcome, exact plan confirmation, finalized billing month and help
request. Viewing history must not submit work, confirm a pending review, mark an
update read or change personal email consent. Filters and older pages stay scoped
to the selected workspace and current permissions.

Repeat with work-only, billing-only and plan-manager roles. Work-only members
must receive no statement/credit amounts or plan confirmations, including after
reusing a broader role's old cursor. Switch workspaces and sign out during a
pending timeline read; old records must not return. Expired result details may
leave a verified outcome in history. Help shows its latest retained update, and
a credit note does not establish a refund. The inbox remains a separate 30-day
view with shared read state.

No credentials or deployed services are needed for the local memory/PostgreSQL
and browser fixtures. This checklist is not live pilot evidence. See the
[customer activity runbook](../../../docs/ops/product-control.md#customer-workspace-activity)
and [ADR-1559](../../../docs/adr/1559-product-control-workspace-activity.md).

Run details in Recent work and workspace activity explain the original plan,
consumed start and retained outcome. Members with billing access also see the
agreed success price, finalization and signed credits. Result expiry does not
remove retained outcome evidence; unavailable evidence is marked explicitly.
See the [run-details runbook](../../../docs/ops/product-control.md#customer-run-details).

Customer **Work history** searches retained receipts by exact reference, original
feature, outcome and inclusive UTC start dates. It can find runs from removed
features or earlier plans. Use Next page/First page, then View run details for
permitted results, billing-month and contextual help actions. Recorded outcomes
remain after result expiry; unavailable outcomes do not assert failure. View work
is required, searches are explicit, and browsing makes no product/email changes.
See [Customer work history](../../../docs/ops/product-control.md#customer-work-history).
