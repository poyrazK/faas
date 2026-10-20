# Gregale Product Control

**Status: internal.** All Product Control endpoints, owner console and portal assets return 404
unless the operator enables `FAAS_PRODUCT_CONTROL_ENABLED=1` (or
`product_control_enabled = true` in apid.toml). The environment variable wins;
any nonempty override other than `1` disables it. The CLI is under advanced help.
This implementation does not change Operations' separate preview qualification.
Restart apid after changing this startup flag.

With the flag off, the dashboard omits Product Control navigation, and every
Product Control API, portal page, script and stylesheet returns 404 with
`Cache-Control: no-store`, including unsupported methods and unknown paths
within its namespaces. The Product Control email worker does not start.
Internal capabilities are omitted from the customer capabilities API and
generated capability matrix; default CLI help and completions omit `product`.

The additive migrations support replay against their existing effects. Column
groups and their initial backfills run only when their new columns are absent;
replay preserves later email constraints and recorded commercial evidence.
The environment clone registry identifies catalogs/customer assignments as
configuration and help as customer data, with explicit isolation blockers until
qualified strategies exist. Financial/action history and personal email consent
are not copied between environments.

Product Control connects a customer's product features to Gregale Operations:

| Product concern | Internal implementation |
| --- | --- |
| Feature | A named Operation in an explicit environment, or a pinned definition UUID |
| Plan | Explicit feature grants, monthly start caps and per-success prices |
| Customer | Existing platform tenant with a revision-pinned plan assignment |
| Execution | Existing Operations admission, idempotency, progress and recovery |
| Usage | Independent commercial receipt committed with operation admission |
| Value | One priced unit when Operations verifies logical success |
| Customer experience | Application Auth login, guided forms, confirmed prices, progress, downloads and reload recovery |
| Owner experience | Dashboard customer overview, reviewed plan changes, attention queue and billing controls |

## Internal owner console

For a new product, open **Guided product setup**. Choose an
existing Application Auth app, name one Operation/environment, and review two
plans' integer prices and monthly allowances. The review shows the resulting
catalog and compares current grants. Other existing entries are preserved.
Confirm publication, choose existing tenants by name, then preview each plan
assignment. Auth invitations/organizations use the existing Auth workflow; first
assignments link their organization UUIDs. Setup never asks for an Auth admin key.

Publication preserves pinned customer terms. A changed catalog head invalidates
confirmation. Same-request retries recover while the desired revision is still
current; after another publication, review again rather than restoring an older
head. Changing the latest Auth app changes every portal customer's login realm;
review its effect on existing organization links before confirming.

### Customer permissions

Open **Customer permissions** in the owner console to review what each
Application Auth workspace role can do. The policy applies to every linked
customer workspace in this product on its next request, including customers
whose plan prices are pinned to an older revision.

| Permission | Allows |
| --- | --- |
| `work:read` — View work | Workspace activity, status, events, recovery, results and private files |
| `work:run` — Run work | Start and cancel workspace runs; includes View work |
| `billing:read` — View billing | Usage totals, statements and credit notes |
| `plans:manage` — Manage plan | Compare and confirm offered workspace plan changes; read their receipts/history |
| `team:manage` — Manage team | Read the workspace team and reviewed team history; invite, revoke, change or remove explicitly delegated roles |
| `billing:manage` — Manage billing details | Read and change the billing address and business the workspace is invoiced at, kept in your Paddle |

With no explicit policy, owners and admins have work and billing permissions, owners can
also manage billing details, and members can only view work. **This narrows the earlier internal behavior where all members
could run paid features and read billing.** Review role access before enabling
an existing internal deployment. Custom roles are denied until granted. An
explicit policy replaces the complete map, including owner/admin grants;
`{"roles":{}}` denies all customer roles. The platform owner can still repair the
policy through the owner console/API. Removing `access` from a catalog restores
the default mapping.

Edit a role, then review the full before/after matrix and confirm. Custom roles
must exist in [Application Auth](../application-auth.md#organizations-and-roles);
configure roles there; the customer team controls below reuse Auth for membership
and invitation changes. Unknown roles have no product access. **Manage plan and
Manage team are opt-in for every role, including owner/admin.**
Up to 32 roles, 32 bytes per role name, and five distinct
supported permissions per role are accepted; names match `[a-z][a-z0-9_-]*`.

The CLI supports the same review flow using the example `access.json`:

```sh
gregale product access inspect --app exports
gregale product access review --app exports --policy-file ./access.json --file access-review.json
# Inspect the current/proposed permissions, then apply the saved review.
gregale product access apply --app exports --file access-review.json
```

Reviews are exclusive mode-0600 files, bound to their app and original catalog.
Preserve the same file for a lost-response retry. If another publication has
changed the head, review again. The change preserves the reviewed prices,
plans, bindings and login realm. It does not migrate customer assignments.
Full-catalog `product apply` can also set `access`; keep the rest of the catalog
current when using that owner configuration interface.

Private owner API: `GET /v1/apps/{slug}/product/access` inspects the effective
map and whether it is default; `POST /v1/apps/{slug}/product/access/preview`
accepts the policy object and returns a read-only before/after preview;
`PUT /v1/apps/{slug}/product/access` accepts its retained `change` object.
Confirmation checks both the original head and desired revision. Cookie API
mutations require the exact HTTPS origin; dashboard forms also require signed
CSRF confirmation. Tenant credentials cannot use these owner policy endpoints.

Every customer request verifies its live Auth role and session. A policy or
membership change affects subsequent requests; an already-authorized request
can finish across a concurrent change. The portal displays only permitted
sections/actions. Billing-only members receive no work details or start forms;
operators receive prices and remaining allowances for consent but no billing
usage totals. Detected permission changes close the current workspace and fence
pending responses; sign in again to continue with current access. Saved request
recovery metadata remains available. Platform tenant **service credentials**
retain their explicit platform scopes and are managed separately from Auth users.

### Customer team management

The portal's **Team** section shows current members, pending invitations and
effective product permissions. A customer needs explicit `team:manage` in the
latest product access policy **and** `org:read` plus `members:manage` in their
current Auth workspace role. This grants no work, billing or plan access.

The platform owner configures **Team roles** in the Product Control console:
allow a manager role to manage specific target roles, review the complete
before/after delegation and confirm. Both the member's current role and a new
role must be allowed. Empty/omitted `team.delegations` permits no changes. Roles
must be declared in the product access policy and configured separately in Auth.
The owner CLI supports the same flow:

```sh
gregale product team-policy inspect --app exports
gregale product team-policy review --app exports --policy-file ./team-policy.json --file team-review.json
# Inspect the saved delegation review, then apply that exact file.
gregale product team-policy apply --app exports --file team-review.json
```

Reviews are private mode-0600 files, bound to their product and immutable catalog
base. A concurrent publication conflicts; an old confirmation cannot restore an
earlier delegation after a later publication. Publication emits a scoped audit
event and changes no memberships, prices or customer assignments.

For the internal pilot, upgrade the Application Auth template with its appended
`sql/0010_reviewed_team_actions.sql`, run its normal release migration, and
explicitly set **`AUTH_PRODUCT_CONTROL_TEAM_ENABLED=1` on that Auth app**. Keep
the existing default-off **`FAAS_PRODUCT_CONTROL_ENABLED=1` apid gate** separate.
Restart/redeploy the affected service after changing its startup flag. An older
or disabled Auth boundary returns `product_team_unavailable` through Product
Control. Enabling the apid flag alone grants no team authority. Existing Auth
membership APIs retain their own behavior and permissions.

Use the current template's `node migrate.mjs` release entry point. Its verified
file runner applies the GoTrue overlay and resumes existing upstream versions;
the stock GoTrue `migrate` command uses embedded SQL and ignores that overlay.
See [ADR-1711](../adr/1711-verified-application-auth-release-migrations.md) for the
runtime verification and compatibility boundary.

Each invitation, role change, removal and revocation has a read-only preview
followed by explicit confirmation. Review both product flags and workspace
permissions: Auth roles affect the entire workspace, including other connected
products. Product delegation constrains Product Control; it does not narrow
`members:manage` authority on other Auth APIs. Use trusted managers. Product
Control disallows changing/removing your own membership; another workspace
owner manages it. Auth still prevents removal or demotion of the last owner.

Recipients accept using the hosted Auth invitation page and the invited,
confirmed email. The default expiry is 7 days (API bounds: 5 minutes–14 days).
The existing configured email provider sends the link; without one the portal
shows an acceptance URL once, with a field for copying it. The recipient then
returns to the product portal and signs in. Invitation tokens are hashed in
Auth and absent from journal history and browser storage. If the confirming
response is lost, recover its receipt, revoke the pending invitation, and
explicitly create another if its original link is unavailable. Recovery never
regenerates or resends a token. Provider delivery uses the existing stable
delivery key but cannot be committed atomically with PostgreSQL.

Auth commits each membership/invitation mutation together with its immutable
receipt. Concurrent identical confirmations recover one result. Reusing its
UUID with different arguments conflicts; retrying an old recorded action after
a later role change cannot reapply it. Product previews bind current customer,
catalog, actor, verified scope and Auth membership/role definitions. Changed
reviews require reloading and explicit confirmation. The two services do not
share a database transaction: a Product-authorized in-flight request may finish
across a later Product policy/link change; Auth checks remain transactional.

The browser stores only `{id, hash}` under account/product/tenant, protected by
Web Locks. Login, reload and **Check saved action** only read status. If no
receipt exists, review current state and explicitly confirm using the saved
reference. Never replace a reference while the result is uncertain. Logout,
workspace changes and detected authorization changes clear private team data
and invitation URLs and fence pending responses. A catalog revision change
requires loading current team data again.

History contains reviewed Auth team actions for this workspace, including those
from other connected products; it excludes older direct Auth administration and
invitation acceptance. Pages contain up to 100 actions using scoped cursors.
Receipts retain the original actor/role and time, never plaintext tokens. They
remain until their Auth organization is deleted; this release has no automatic
receipt expiry. Suspended Product customers can read/recover existing actions
but cannot make new changes; inactive tenants/accounts lose the boundary.

Turning off the apid flag hides all Product team routes/assets. Turning off the
Auth flag hides its new reviewed routes. Neither rollback deletes existing
memberships/invitations or rolls back recorded actions. Revoke any outstanding
invitations through the existing authorized Auth workflow when necessary.

The [team pilot drills](../../examples/customer-operation-export/product-control/README.md#customer-team-pilot)
must pass on the qualified Auth/email deployment before wider availability.

#### Internal Auth review API

The reviewed team routes require the release migration
`0010_reviewed_team_actions.sql` and the separate
`AUTH_PRODUCT_CONTROL_TEAM_ENABLED=1` flag on the Auth app:

| Internal route | Contract |
| --- | --- |
| `GET /authz/v1/orgs/{org}/team` | Current actor, members, pending invitations, role definitions and canonical review snapshot |
| `POST /authz/v1/orgs/{org}/team-actions` | `{id, action, expected_snapshot, ...}`; reviewed `invite`, `set_role`, `remove_member` or `revoke_invitation` |
| `GET /authz/v1/orgs/{org}/team-actions/{id}` | Recorded action receipt; exact retries never repeat the old membership mutation |
| `GET /authz/v1/orgs/{org}/team-actions?cursor=...` | Workspace-scoped reviewed history, up to 100 actions per page |

All require a live session with `org:read` and `members:manage`; existing owner
restrictions still apply. A role/membership change invalidates an unconfirmed
snapshot. Auth serializes verification, membership mutation and journal insert
in one transaction. Action IDs cannot be reused for different requests. Optional
correlation context contains only bounded non-secret UUIDs/revision hashes and
never grants permission. Plaintext invitation tokens are excluded from retained
requests/results and returned only on initial manual-link creation. Acceptance
uses the existing hosted invitation page. These routes do not change permissions
on the existing Auth APIs. The Product Control delegation policy constrains
that product surface; Auth's `members:manage` remains workspace-wide authority.

### Customer self-service plans

Open **Self-service plans** in the owner console. Allow each direction explicitly,
such as Starter → Pro and Pro → Starter, review the resulting choices and confirm.
The source is the customer's current plan name; the target must be a plan in the
latest catalog. A source can name a retired plan. An explicit same-name transition
allows customers with older pinned terms to adopt that plan's latest revision.
An unchanged plan and revision is never offered. **Close all choices** stops new
changes without changing any assignments or retained receipts.

The policy is closed when omitted or empty. It allows up to 32 source plan names
and 32 distinct targets per source. Publish choices with the example
`plan-policy.json` through the dashboard or advanced CLI:

```sh
gregale product plan-policy inspect --app exports
gregale product plan-policy review --app exports --policy-file ./plan-policy.json --file plan-policy-review.json
# Review the current/proposed choices, then publish the saved change.
gregale product plan-policy apply --app exports --file plan-policy-review.json
```

The owner API uses `GET /v1/apps/{slug}/product/plan-policy`,
`POST /v1/apps/{slug}/product/plan-policy/preview` with
`{"transitions":{"starter":["pro"],"pro":["starter"]}}`, and
`PUT /v1/apps/{slug}/product/plan-policy` with the preview's `change` object.
CLI reviews are exclusive mode-0600 files bound to the app and immutable base.
Publication checks the reviewed head and expected revision, preserves all other
catalog fields, and never migrates customers. Exact retries recover while that
head remains current. A later publication requires another review.

Separately, grant `plans:manage` to an existing Application Auth role through
**Customer permissions**. The example `plan-manager` grant is illustrative;
configure that role in Auth before assigning it. Neither billing access nor an
owner/admin role implies plan authority. A manager without work/billing grants
receives commercial plan comparisons and its change history, without operations,
results, bindings or billing documents. Combining permissions is explicit.

In the customer portal, **Change your plan** loads only offered choices. The customer
reviews feature inclusion, per-success prices, monthly allowances, used starts
and remaining starts, then explicitly confirms. Changes apply immediately to
future starts. Already accepted runs keep their admitted price. Consumed starts
carry across plans and catalog revisions within the UTC month; a downgrade below
that count leaves zero starts available. There is no subscription fee, proration,
payment collection or scheduled downgrade in this release.

A review binds the current assignment, both sets of terms, latest catalog head,
UTC month and relevant consumed starts. Confirmation rechecks these with current
role policy, Auth app identity and organization/tenant mapping in the same
transaction as the assignment and immutable receipt. A relevant new start, owner
change, policy publication or month rollover requires a fresh review. Suspended
customers have no new offers. Tenant/account suspension or revoked permission
also denies access to plan-change receipts.

The browser saves only a non-secret UUID, target/revision and review hash before
posting; site storage and Web Locks are required. After a lost response, use
**Check saved confirmation**. Reload only looks up the saved reference and
never resubmits. If not recorded, load/review current choices and confirm again
with that reference. If an earlier confirmation was already recorded, its actual
terms are shown. Exact confirmed retries return the retained receipt without
restoring an old assignment, even after an owner changes that customer later.
Changed arguments under the same UUID conflict. Keep the reference for support
if a status check is unavailable. **Plan change history** pages recorded customer
self-service changes; owner-initiated assignments remain in the owner workflow.

All of this remains behind the same internal default-off feature flag. Run the
[self-service pilot drills](../../examples/customer-operation-export/product-control/README.md#self-service-plan-pilot)
after capturing the initial three-customer commercial baseline.

### Guided customer onboarding

Open **Onboard a customer** from the owner console. Connect to the product's
existing Application Auth app with a workspace owner/admin's invited user
account. Choose a workspace by name, select an existing active customer or enter
a new name and stable reference, then select a plan. Review the workspace's
access, feature prices and monthly allowances before confirming setup.

The setup connection uses a short-lived, HttpOnly cookie scoped to this product's
owner onboarding page, for at most ten minutes. It has no refresh token. Use
**Disconnect workspace session** when finished. The connection does not mark
customer activation; an actual customer must open their workspace afterward.

Workspace creation, invitations and password recovery use the existing
[Application Auth workflow](../application-auth.md). Send invitations there,
then share the customer portal path shown on the activation page. This setup
flow never requests an Auth admin key. Only a live workspace owner/admin can
link its identity. Workspace members receive the product permissions described
below according to their current Auth role. Membership is checked again before initial confirmation and on each
customer request.

The equivalent internal CLI flow uses an owner profile plus a separate
short-lived Application Auth user access token in a private file:

```sh
gregale product customers workspaces --app exports --auth-token-file ./auth-user-token
gregale product customers onboard --app exports --external-ref acme --name Acme \
  --workspace acme --plan starter --auth-token-file ./auth-user-token --file acme-review.json
# Review the saved prices, workspace and customer, then confirm explicitly.
gregale product customers onboard --app exports --file acme-review.json --confirm \
  --auth-token-file ./auth-user-token
gregale product customers onboarding --app exports --id ONBOARDING_RECEIPT_UUID
```

Use `--tenant TENANT_UUID` instead of `--external-ref`/`--name` for an existing
customer. Existing assigned customers retain their pinned revision and plan;
onboarding can add a first organization link, but does not replace an established
link, change plans or lift suspensions. Use the customer plan-change workflow
when terms need to change. Existing unassigned tenants receive reviewed terms.
The stable reference can resolve an existing tenant only when its name matches.

Review creates no customer or assignment. Its new file uses mode 0600 and refuses
overwrites; preserve it across retries. Initial confirmation requires a current
Auth owner/admin membership. An exact retry after a lost response returns the
original setup receipt without changing later assignments, and can omit the
token file once setup has already committed. Changed confirmations, intervening
catalog/realm/month/customer changes and competing setups require another review
in a new file. Tenant creation, assignment and receipt insertion commit together.
Existing quota applies and previously accepted work keeps its prices and starts.

Activation pages and status reads show setup completion, whether the current
assignment still matches, the first observed authenticated customer workspace
request, and the first verified successful action admitted after setup. A prior
access observation is not a present membership check. Unknown, failed and
cancelled runs do not establish success; retained proof survives result expiry.
None of these states grants native qualification. See
[ADR-1707](../adr/1707-product-control-guided-customer-onboarding.md).

### Product readiness

Open **Check readiness** for an app or selected customer, or run:

```sh
gregale product doctor --app exports --json
gregale product doctor --app exports --tenant TENANT_UUID
```

The private `GET /v1/apps/{slug}/product/doctor?tenant_id=UUID` uses owner read/MFA
policy and the default-off flag. Customer credentials have no owner diagnostic
access. The response identifies the responding API node and observed time, plus
latest and assigned catalog revisions. Without a customer, entitlement, allowance
and cohort checks remain unverified. With one, diagnostics inspect its access,
identity-link configuration, bindings, quota and existing Operations prerequisites.

`observed`/`configured` describe metadata/configuration at that time. They do not
prove live Auth membership, storage I/O, gateway/runtime behavior or native
qualification, and reserve no capacity. Configuration is `blocked` when a known
prerequisite is missing, `unverified` when it could not be observed, or `observed`
when the inspected prerequisites are present. Native qualification remains
`unverified` in this report. CLI exits are 0/1/3 for these configuration states;
request/validation errors are nonzero. Follow each check's remediation and rerun.

### Customer readiness and activation overview

Open **Customer readiness and activation** in the owner console, or run:

```sh
gregale product customers overview --app exports
gregale product customers overview --app exports --json
gregale product customers overview --app exports --cursor LAST_CUSTOMER_UUID --json
```

The private owner `GET /v1/apps/{slug}/product/customers/overview?cursor=UUID`
and `/dashboard/apps/{slug}/product-control/customers` use the existing
default-off flag, owner read/MFA policy, and no-store responses. One request
observes at most ten assigned customers under a shared twenty-second deadline,
with at most 3 MiB of JSON. Large diagnostic reports produce smaller pages with
normal continuation, so they remain readable by the CLI without skipping customers.
The optional cursor is the last emitted tenant UUID, not an access credential.
Each page is an observation window on the responding API node, not a population
total, shared database snapshot, admission reservation or live Auth probe.

Each customer shows its pinned plan/revision, workspace-link configuration,
current feature/binding/allowance checks, guided activation progress, and pending
work across all retained admission months. **Need review** excludes ordinary
accepted/running work. Its next action opens the earliest month requiring review;
follow the existing review workflow and inspect every relevant month. Resolved
and waived receipts do not count. Missing Operation results still need review.

Activation uses the latest guided onboarding record. It distinguishes no guided
record, waiting for a workspace visit, waiting for first success, first success
recorded, and changed setup. A manually assigned customer can already have
activity without a guided activation record. Recorded visits/successes describe
history after setup; current membership and native qualification remain separate.
Changed setups retain historical success proof and link to current review.

Expand **Feature and configuration checks** for observations and remediation.
Next actions link to the existing readiness, onboarding or customer review page;
the overview never changes a plan, starts work, repairs an identity, sends mail
or enables a flag. Changed assignments observed during a read, a UTC-month
boundary or a failed read require another request rather than a partial success.
CLI exit zero means the requested observation succeeded, including rows with
blockers; use `product doctor --tenant UUID` for its configuration exit status.
Every overview keeps native qualification `unverified`. See
[ADR-1712](../adr/1712-product-control-customer-readiness-overview.md).

### Customer activity timeline

Open **View activity timeline** from an assigned customer or its readiness row,
or read one page with an owner CLI profile:

```sh
gregale product customers activity --app exports --tenant TENANT_UUID
gregale product customers activity --app exports --tenant TENANT_UUID --json
gregale product customers activity --app exports --tenant TENANT_UUID --cursor SAVED_CURSOR --json
```

The private owner `GET /v1/apps/{slug}/product/customers/activity?tenant=UUID`
requires the canonical tenant UUID; optional `cursor` selects older events.
The console lives at `/dashboard/apps/{slug}/product-control/activity?tenant=UUID`.
Both keep the existing read/MFA, ownership, no-store and default-off boundaries.
Customer credentials cannot use this owner surface.

History brings together recorded guided setup, workspace visits, first verified
success after each setup, reviewed owner/customer plan changes, work admission
and verified outcomes, waivers, billing documents and invoice handoffs. Expand a
record to see its source ID and retained details, or follow its existing review
link. Each run keeps its original commercial terms and any available admission
evidence, even after its result expires or the customer changes plans. A customer
plan change shows its recorded role; other sources do not invent an actor.

Pages contain at most fifty events and 3 MiB of JSON under one twenty-second
deadline. The next cursor binds the account/app/customer and last event's time,
kind and source ID, preserving simultaneous milestones without duplication.
Each page is a fresh observation; refresh newest activity for later arrivals.
An existing cursor does not freeze the source data or retention.

The scope is retained Product Control records, not a complete customer audit log.
Legacy direct assignments, transient Operation states and Auth team changes are
not reconstructed. Missing history does not establish inactivity or failure.
Workspace visits and success are historical facts, not current access/health.
Admission is not success; waiver is not an execution outcome; invoice handoff
is not payment or refund. Reading history performs no writes or external calls.
See [ADR-1713](../adr/1713-product-control-customer-activity.md).

### Repeatable pilot verification

```sh
gregale product pilot init --app exports --feature export --file product-pilot.json
# Fill app_id from doctor --json, real tenant/Operation IDs and pinned revisions.
# Review expected plans, outcomes and integer charges; three baseline users, two plans.
gregale product pilot verify --app exports --file product-pilot.json --output pilot-report.json --json
```

`init` and report creation refuse overwrites and use private files. The verifier
only reads scoped receipts, immutable catalog terms, billing reviews and optional
Customer Help records; it never
submits, retries, cancels, deploys or closes billing. Each case requires explicit
expected charge millicents, including zero for nonbillable outcomes. Additional
failure/cancellation/reconciliation cases must belong to the three baseline
customers. Retained receipts can verify terminal evidence after result expiry.
Aggregate billing lines must cover every referenced receipt; selected cases do
not establish the customer's complete monthly total. Concurrent changes can
require another observation. A report's manifest SHA-256 binds its expectations.

Optionally set a case's `output_file` and `output_sha256` together. The CSV path
must be relative to the manifest directory, stay within it, and contain at most
16 MiB. Its bytes are hashed without including contents in the report. This
proves a local file matches its expected digest, not that the customer downloaded
it or that a native exporter was qualified. Native/browser scenarios remain
explicitly unverified until the separate live trial is performed and recorded.
Use `gregale product pilot help --app exports --tenant TENANT_UUID --request
HELP_UUID --json` to inspect only scoped IDs, timestamps and hashes. Its result
is `inspection_only`, not an expectation check. Retain original text/context
hashes separately from reviewed later response/status/version expectations.

Optional `help_cases` compare retained requests against explicit status/version
and reviewed message, response and context hashes. Cases belong to a baseline
customer; run references must name that customer's selected operation, and
feature references name this pilot's feature. Run context must match that
admission's original feature, revision, plan and month. Reports retain only scoped IDs,
timestamps and hashes. Text hashes use exact UTF-8 bytes; the context uses the
existing canonical JSON encoding of `api.ProductHelpContext`. Capture expectations
from the accepted request before later owner edits to verify its original
snapshot, then separately review the expected current response and version.
These owner reads do not prove customer-session visibility or browser recovery.

Optional `journeys` record the three baseline customers' sign-in, paid feature,
usage, plan change, help request and owner response observations. Each references
a selected run/help case and local reduced evidence files with reviewed hashes.
Paths stay inside the manifest directory, files are nonempty regular files at
most 4 MiB, and contents are excluded from the report. Each customer has at most
32 observations. Keep credentials, raw HAR/cookies, invitation tokens and help
text out of evidence. Operator times produce `first_success_ms`; blocked attempts
produce `blocked_steps`. Matched resolved help produces `help_resolution_ms`
from creation to its latest resolved update, including any reopenings. This does
not reconstruct first response/resolution or establish product latency targets.

`help_state` is `not_requested`, `observed` or `blocked`. `journey_state` is
`not_requested`, `recorded`, `incomplete` or `blocked`; missing customers/stages or
missing successful work/owner response leave requested journeys incomplete.
Even complete operator records leave qualification and live browser scenarios
unverified. [ADR-1716](../adr/1716-product-control-internal-pilot-evidence.md)
defines the evidence boundary.

For the complete internal pilot, set manifest `version` to `2`. All three
baseline cases then require nonempty downloaded CSVs with reviewed hashes, and
all three journeys must select their baseline run and help about that same run.
Version two also requires an observed `inbox_update` stage for each customer.
Capture the actual `api.ProductNotificationPage` response JSON through that
customer's browser session and reference its local file/hash from the stage.
Do not capture cookies, authorization headers or raw HAR. The owner verifier
does not call private customer routes or substitute owner authority for a session.

Inbox JSON uses the existing 128 KiB response bound. Strict parsing checks its
app/tenant, canonical identities, shared workspace scope, thirty-day window,
item references/revisions and read-version shape. Across observed pages, both
the selected successful work and latest matched help update must be present
with retained source kind/time/feature. Older or missing sources leave the
journey incomplete. Separate `inbox_update` observations can capture multiple
pages. Blocked attempts can reference reduced failure artifacts; observed pages
are never copied into the report. The page's account ID is shape-checked, not
compared with an account ID from doctor, which does not return one.

Use the [complete journey fragment](../../examples/customer-operation-export/product-control/pilot-complete-journeys.template.json)
and [inbox response shape](../../examples/customer-operation-export/product-control/pilot-inbox.template.json)
as preparation aids. They contain no actual observations. Version one remains
compatible; an explicitly requested version-two pilot cannot fall back to a
ledger-only success. Inbox browser visibility/read recovery and native
qualification always remain unverified by this tool. See
[ADR-1718](../adr/1718-product-control-complete-internal-pilot.md).

Pilot exit 0 means requested comparisons matched and requested journeys were
recorded; 1 means a ledger/help/artifact mismatch; 3 means an incomplete requested
journey unless a mismatch takes precedence. Legacy ledger-only success remains
exit 0. Request or manifest errors are nonzero. No exit code grants qualification.
See the [pilot guide](../../examples/customer-operation-export/product-control/README.md)
and [ADR-1706](../adr/1706-product-control-guided-setup-and-readiness.md).

### Existing owner workflows

Enable the existing flag, apply migrations, restart apid, and sign in to the
normal owner dashboard. Open **Apps → your app → Manage Product Control** or
`https://API_ORIGIN/dashboard/apps/APP_SLUG/product-control`. The link and console
are absent while the flag is off. This uses the existing live owner session and
MFA policy; mutations also require verified email. API keys and customer portal
sessions do not authenticate the console.

The catalog and platform tenants must already exist. Select an assigned customer
or enter an existing account-owned tenant UUID to make its first assignment.
The page shows plan/revision, identity mapping, feature access, prices, starts
used and remaining allowance for the selected UTC month. Binding availability
is checked separately for the latest and assigned catalog revisions. Lists have
at most 100 entries and explicit next-page links.

Use **Preview plan change** to compare prices, access and monthly caps, then
confirm. Existing Auth organization links are preserved automatically. First
assignments can supply an organization UUID. Terms change immediately for future
admissions; starts already used do not reset and accepted runs keep their original
terms. No subscription proration occurs. An intervening assignment, identity,
catalog or UTC-month change requires a new preview. Confirmation IDs and
before/after receipts are durable: retry the same confirmation after a lost
response; an older retry cannot reapply itself over a newer change. Expired or
replaced CSRF sidecar cookies require a fresh review.

**Needs attention** lists closed months without statements and runs whose outcome
requires review. Ordinary current-month accepted/running work is excluded.
Missing results never establish failure. Binding problems appear separately.
Open a customer's month, inspect the run's commercial evidence, and use its
Operations link for execution recovery.

Billing controls review and confirm finalization, permanent waivers, credits and
invoice references using the rules below. Waivers forgo eventual charges without
cancelling runs or restoring allowance. Credits use stable UUIDs and cannot exceed
the remaining original charge. Handoffs are immutable and readable on reload;
use the document UUID as the external invoicer's idempotency key and recover an
uncertain external result before retrying it. No invoicing/payment provider is
called. Credit amounts are whole millicents: 100,000 equal one currency unit.
Reasons must contain no secrets; credit reasons are visible to the customer.

Run views exclude inputs, result bodies, storage locations and credentials.
Commercial receipts remain visible after execution result expiry. Partial document
pages never claim a net total. See [ADR-1705](../adr/1705-product-control-owner-console.md).

## First internal workflow

Start with one paid export capability. Declare and qualify its `export` Operation
using the existing Operations workflow. Configure
the Operations preview cohort, workload trust and artifact storage as documented
in [Operations](../operations.md); the Product Control switch grants none of these.
Apply platform migrations before starting the updated apid.

```sh
gregale product init --operation export --scope default --file product.json
gregale product validate --file product.json
gregale product apply --app exports --file product.json
gregale product inspect --app exports
gregale product features list --app exports
gregale product plans list --app exports
gregale product customers assign --app exports --tenant TENANT_UUID --plan starter
gregale product customers list --app exports
gregale product usage --app exports --tenant TENANT_UUID
gregale product statements list --app exports --tenant TENANT_UUID --period 2026-10
```

`init` creates a new file and refuses to overwrite an existing file. The starter
grants 100 export starts per UTC month at 25,000 millicents (€0.25) per verified
success. Edit the catalog before publishing. Catalog validation rejects duplicate
JSON members, unknown fields, invalid bindings, negative prices and unbounded
grants. Currency is fixed for an application's Product Control history.

An assignment pins both plan and catalog revision. Publishing another catalog
does not migrate existing customers. Assign explicitly to adopt the current
revision, or supply `--revision HASH` to select an earlier one.

The generated feature follows `export` on the fully serving deployment in
`default`. Keep that Operation declaration in the source manifest: normal
source-based `gregale deploy` installs a new immutable definition, and Product Control resolves
it when the deployment becomes live at 100% traffic. No definition UUID copying,
catalog publication or customer reassignment is needed for a code-only deploy.
Existing customer revisions, prices, start counts and accepted work stay pinned.
Image-only deployments still require the existing Operations definition
registration workflow; Product Control does not extend image admission.

For an intentionally fixed binding, use `init --definition DEFINITION_UUID`.
Fixed bindings keep the original contract; publish and explicitly assign a new
catalog before retiring their code. Catalog references alone do not extend
deployment retention.

## Deployment bindings

A feature selects exactly one binding form:

```json
{
  "label": "Export",
  "operation": {"name": "export", "scope": "default"}
}
```

Or `{"label":"Export","definition_id":"DEFINITION_UUID"}`. A named binding
requires an explicit environment and resolves only the named Operation belonging
to this app and account. Commercial configuration can be published before code
exists; the feature stays unavailable until its binding can be resolved.

New work requires one live deployment carrying 100% of the environment's traffic,
with no other live deployment carrying positive traffic. Pending, failed and
zero-traffic candidates cannot take over a feature. During a split canary,
new starts return `409 product_operation_binding_unavailable`; they resume after
the rollout completes or rolls back to one fully serving revision. A serving
deployment missing the named Operation does not fall back to older code or a
different environment. The binding is rechecked inside Operations admission
under its existing code locks. A deployment changing between resolution and
admission can require an explicit retry with the same key.

Submit `{app, feature, input, expected_quote}` with a stable `Idempotency-Key`. The server resolves
the binding; the CLI and portal never need to submit a deployment UUID. A retained
duplicate resolves to its original definition before considering today's grant
or deployment, including when the customer is paused or no live binding exists.
Different input still conflicts. The independent Operations preview gate remains
required for submission and retries. The older `{feature, definition_id, input}`
form remains supported; its definition must pass the same admission policy.

To pause new work while retaining the assigned revision:

```sh
gregale product customers assign --app exports --tenant TENANT_UUID --plan starter --revision HASH --suspended
```

With the customer's platform-tenant access token selected through the normal
CLI credential mechanism (`FAAS_TOKEN` or a dedicated profile):

```sh
gregale product quote --app exports --feature export --json
# Review the displayed price and allowance, then supply that exact hash:
gregale product start --app exports --feature export --input request.json --key export-2026-10-15 --quote QUOTE_HASH
gregale product usage --app exports --self --json
```

Keep the same key and input for retries of the same work. A Product Control key
is scoped by account, app, tenant and feature, independently of the Operation's
deployment, definition name, environment and price revision. A new key means new
work. Conflicting input returns 409. Publishing a catalog opts the entire app
into Product Control admission. Every new Operation for that app must pass
through the product policy boundary, including definitions introduced by later
deployments. Declare free features as explicit zero-price grants. Already
accepted Operations remain readable and recoverable. Retry guarantees follow
Operations' existing deduplication window; commercial receipt retention does
not extend that request-identity window.

## Customer portal and login

Configure an existing account-owned [Application Auth](../application-auth.md)
app before enabling customer login. Use invited internal users and disable
public signup during the pilot. The latest catalog selects `auth_app`; the
server verifies its owned managed-PostgreSQL Auth binding and calls only its
platform HTTPS hostname. Changing this selector changes the portal realm for
all customers without migrating their pinned commercial prices.

```sh
gregale product init --operation customer-export --scope default --auth-app exports-auth --file product.json
gregale product apply --app exports --file product.json
gregale product customers assign --app exports --tenant TENANT_UUID --plan starter --auth-org AUTH_ORGANIZATION_UUID
```

An Auth organization may map to one tenant per product. Keep separate organizations
for separate customers. Every current member can use that customer's features
and read its runs. CLI/API assignment replaces the configuration: repeat `--auth-org`
when changing a portal customer's plan, revision or suspension. Omitting it
unlinks that customer's portal login; existing scoped CLI credentials retain
their separate policy. No arbitrary customer ID, JWT claim or owner session
selects portal authority. Inactive accounts/tenants cannot open a workspace.

Open `https://API_ORIGIN/v1/product-control/portal?app=exports`. Customers sign in
with their invited email/password and automatically receive their mapped
workspace; users with several mapped organizations get a workspace selector.
The Auth app supplies normal login, rotating refresh and local session sign-out.
Product Control stores tokens only in Secure, HttpOnly, SameSite=Strict cookies
scoped to the product's customer API. Passwords/tokens never enter browser
storage, URLs, redirects or JSON responses. Mutations require the same HTTPS
Origin. The shared API failure limiter protects login. Hosted OAuth/magic-link
flows and organization creation are not in this internal portal.

Each request checks the Auth app's live session and organization memberships;
revocation/bans and membership changes are not cached. An owner mapping/realm
change is checked again under admission locks. Auth verification and platform
admission use separate databases, so an already authorized in-flight request has
the normal authorization race. Auth outages fail closed. The bridge grants only
tenant Operations read/manage scopes, and each operation must belong to this
product app as well as the selected customer.

The portal shows the assigned plan/features, current provisional usage,
remaining starts across price revisions, deployment availability and recent
retained work. Flat scalar object schemas produce guided fields; other schemas
use JSON input. Progress/usage refresh every four seconds without replacing
inputs being edited. Customers can cancel work, view succeeded JSON and download
verified private files. An unknown outcome asks for owner reconciliation and
never triggers replacement work. History is bounded by the existing per-feature
history page limit; saved acceptance receipts also restore runs whose features
have since been retired.

### Customer account recovery

On the sign-in page, enter the product name and choose **Forgot password?**.
Recovery opens in a separate tab using the current product's Application Auth
hosted page. Enter the invited email there, request a reset link and complete the
existing Auth password flow. Return to Product Control afterward and sign in
with the new password. The Auth app's configured post-reset destination is
unchanged; resetting does not automatically sign in to Product Control.

The link uses `GET /v1/product-control/customer/{slug}/account-recovery`, which
returns 303 to the current owned Auth app's fixed `/ui/forgot-password` page.
It uses the latest catalog realm even for customers on older commercial revisions.
No email, password, token or arbitrary callback is accepted in the URL. Query
parameters are rejected. The explicit empty redirect fragment discards caller
fragments; `no-store`, `no-referrer` and `noopener noreferrer` protect navigation.
The endpoint sets no cookies and requires no existing customer session. Existing
pre-authentication rate limits and the Product Control default-off gate apply.

Opening recovery sends no email by itself. The customer explicitly requests it
inside Application Auth; its existing provider configuration, reset-token policy,
allowlisted redirects and session rules own delivery and completion. An unavailable
or unbound Auth app cannot provide a recovery destination. Product Control does
not create users or workspace access through recovery.

Local verification can exercise navigation with fictional Auth API responses.
The later internal trial must verify actual recovery delivery, one-time/expired
links, configured password policy and post-reset session behavior on the selected
Auth deployment. See [ADR-1719](../adr/1719-product-control-customer-account-recovery.md).

### Personal notification email

In **Your email notifications**, each customer chooses Work outcomes and/or Help
replies for their own verified sign-in address in the selected workspace. Both
start off. Choices are personal; inbox read state stays shared. Save explicitly
to enable, disable or renew choices. Consent lasts 30 days. Only new updates after
opt-in are eligible, and renewing expired consent starts from renewal time.
Messages link to the portal and omit help text, run inputs and results. Multiple
changes between scans may appear as one email for the latest retained update.

The portal's **Recent email deliveries** shows queued, captured locally, accepted
by provider, provider-reported delivered/bounced/complained, failed or cancelled,
plus attempts, report time and safe reasons. Refresh status explicitly. Provider
acceptance is not proof of delivery, and a delivered report proves neither inbox
placement nor reading. Changing preferences
cancels queued attempts; an already in-flight provider request may complete.
Removing membership, losing the required role/product permission, changing the
Auth realm or tenant mapping, an unverified/changed address, or an expired source
blocks the next attempt. After an address change, save choices again to consent
for the newly verified address. Auth outages retry without bypassing access.

The private customer routes are `GET`/`PUT
/v1/product-control/customer/{slug}/email-preferences` and `GET
/v1/product-control/customer/{slug}/email-deliveries`. They require the live
customer cookie and selected organization; PUT requires same-origin HTTPS and
exact `work_outcomes`, `help_replies`, `expected_version` fields. A stale version
returns 409. No recipient address or actor/tenant selector is accepted. History
is personal, permission-filtered, bounded to 25 records and retained for 30 days.

Email delivery defaults off separately from the Product Control feature. For a
local fixture, use `FAAS_PRODUCT_CONTROL_ENABLED=1`,
`FAAS_PRODUCT_CONTROL_EMAIL_MODE=local` and an absolute
`FAAS_PRODUCT_CONTROL_EMAIL_SINK_DIR` pointing to a private 0700 directory.
The existing host age identity seals recipient capabilities; tests generate
fixture keys locally. Captures are 0600 JSON files named by stable delivery UUID.
Treat a sink containing real addresses as private; use fictional customers for
local development. Captured messages never become provider-accepted messages.

The Auth app needs its new runtime, the append-only `0004` release migration and
`AUTH_PRODUCT_CONTROL_EMAIL_ENABLED=1`. Existing Auth deployments without that
protocol cannot enable email. It issues a narrow recipient capability, not an
Auth session or admin key. It expires after 30 days and is bound to the current
verified address. Product Control encrypts it at rest. No customer login token or
plaintext recipient address is saved in the platform database. The 30-day grant
lifetime fits the documented [30-day host identity overlap](host-age-rotation.md).
Keep the previous identity available during that overlap; early retirement makes
older grants unavailable and customers must save their choices again.

The worker runs every minute while enabled. It has a 50-second cycle budget,
25-subscriber/25-attempt batches, two-minute leases and six attempts within
12 hours. Postgres claims use `SKIP LOCKED`; each source revision has a stable
provider key across crashes/retries. Preferences are limited to 500 per tenant
and delivery records to 1,000 per subscription within the rolling window.
When capacity is reached the scan retains its cursor until capacity becomes
available. The feature is an update notification service over retained sources,
not an event-delivery guarantee or an audit archive.

Real sending is a later internal release action: `resend` mode additionally
requires `FAAS_PRODUCT_CONTROL_EMAIL_SEND_ENABLED=1` and the existing
`FAAS_MAIL_RESEND_API_KEY`/`FAAS_MAIL_FROM` provider configuration. Missing settings
fail startup. Log, noop and Postmark are rejected as Product Control delivery
modes. This stream uses the raw idempotent adapter and its own durable retry loop;
provider error bodies and addresses do not enter worker logs. No real sending or
flag enablement is part of local verification.

See [ADR-1720](../adr/1720-product-control-personal-notification-email.md).

### Owner notification email controls

Open **Notification email** in the Product Control owner console, or run:

```sh
gregale product email list --app EXPORT_APP
gregale product email list --app EXPORT_APP --tenant TENANT_UUID --status failed --json
```

The view shows pending/sending, captured locally, provider-accepted,
provider-reported delivered/bounced/complained, failed and cancelled counts for
the selected product/customer's retained 30-day window,
plus the oldest pending age. Totals cover all statuses, independently of the
history filter and page. History has 25 messages per page, newest first; preserve
the same app, tenant and status with the returned `--cursor`. Each page observes
current state afresh. It shows the latest state and cumulative attempt count of
each delivery, not a per-attempt journal. An empty queue does not prove email is
configured, that all customers were scanned or that anyone has opted in.

Use the fixed reason and retry explanation to choose the next action:

| Observation | Next action |
| --- | --- |
| Queued with authorization unavailable | Restore the owned Auth service; retain the worker's automatic delay. |
| Queued with transport unavailable | Restore email transport; preserve Retry-After and automatic backoff. |
| Provider rejected | Correct the provider configuration; retry only when history says eligible. |
| Consent or access changed | Review current customer choices/access; a cancelled message remains cancelled. |
| Source changed | Review the newer portal update; the old message remains cancelled. |
| Budget/window exhausted | Investigate the underlying failure; there is no force-resend or deadline extension. |
| Captured locally | Inspect the private fixture sink; no email was sent. |
| Accepted by provider | Acceptance is recorded; no matched delivery report has been retained. |
| Provider reports delivered | The receiving mail server accepted the message; inbox placement and reading remain unknown. |
| Provider reports bounced | Inspect the safe state and subscription pause; permanent bounces block future attempts. |
| Provider reports complaint | The affected subscription is paused; do not requeue or try to bypass it. |

For an eligible provider rejection, click **Queue retry for failed attempt N**,
or copy that delivery's customer, delivery UUID and failed attempt into:

```sh
gregale product email retry --app EXPORT_APP --tenant TENANT_UUID --delivery DELIVERY_UUID --attempt 1 --json
```

Use the observed attempt, not a guessed value. The action returns a durable
requeue receipt and performs no inline send. Repeat the **same** values after a
lost response to recover that receipt, even if the worker has since completed.
Refreshing history shows current delivery state. A repeated receipt cannot
requeue a completed message. Receipts remain available while their delivery is
retained; past retention, an unavailable receipt is not a reason to force resend.

Only provider-rejected failures with remaining attempts and original delivery
time can requeue. The action keeps the original key, six-attempt budget and
12-hour deadline. Current consent, Auth authority, mapping, permissions and source
revision are checked before recovery and again by the worker before sending.
Metadata eligibility is not a live access guarantee. Revocation or a newer
update between review and send can still cancel the queued attempt. Existing
in-flight provider requests follow the original email contract.

Private owner routes are `GET /v1/apps/{slug}/product/email-deliveries` with
optional `tenant`, `status`, `cursor`, and `POST
/v1/apps/{slug}/product/email-deliveries/{delivery}/retry` with exactly
`tenant_id` and `expected_attempts`. Observation requires account-owner read
scope; recovery requires deploy-write scope, MFA policy and verified email.
The browser uses the owner dashboard session and bound CSRF forms. Customer
sessions and tenant credentials cannot access these routes. Responses omit
addresses, Auth identities, grants, message content and private source bodies.

Apply the new platform migration before using owner controls. Product Control
still defaults off and email still defaults disabled. Disabled transport allows
diagnostics but blocks a new retry. Local mode exercises the entire recovery
path using fictional identities and the private capture directory; use local
HTTP fixtures for provider rejection, outages, stable-key recovery and repeated
receipt checks. No provider credentials are needed. Real delivery qualification
remains separate. See [ADR-1721](../adr/1721-product-control-owner-email-controls.md).

### Provider delivery feedback and paused subscriptions

After the feedback migration, Product Control can receive signed Resend events
at `POST /v1/product-control/email/resend-events`. The optional
`FAAS_PRODUCT_CONTROL_EMAIL_WEBHOOK_SECRET` is a distinct `whsec_` secret for
this endpoint, with a base64-encoded 24–64-byte key. It does not enable sending.
An empty setting leaves feedback unavailable (503) while the product is enabled;
the default-off product gate still returns 404 for every method while disabled.
The existing platform/billing webhook and suppression list are separate.
Real provider configuration remains a later release action.

Subscribe to `email.delivered`, `email.bounced` and `email.complained` when
qualifying the real provider. Signature authentication uses the original body
and one each of `svix-id`, `svix-timestamp`, `svix-signature`, with five-minute
clock tolerance, 1,024-byte header limits and a 64 KiB body limit. Keep clocks
synchronized. No query selector or owner/customer token is accepted. Invalid
signatures and malformed recognized events return 400; unsupported events and
accepted duplicates return 200. Storage/capacity failures return 503 for retry.

Successful sends now retain the provider message UUID. An ambiguous 2xx response
without a valid receipt retries the original idempotency key within the existing
budget. Existing accepted records without provider IDs cannot acquire later
reports retroactively. Local captures have no provider feedback. History shows
the highest-precedence retained state and its provider event time: complaint
over bounce over delivered over accepted. Out-of-order events cannot undo a
complaint or bounce, and duplicate callbacks create no delivery or attempt.

An early callback is buffered by exact provider UUID and kind until the matching
send receipt commits. The buffer stores metadata only, never addresses, raw
bodies, subjects or bounce diagnostics. Unmatched reports cannot pause anyone.
Its global limit is 10,000 rows and its lifetime is 24 hours from first receipt;
duplicates cannot extend that lifetime. Cleanup handles up to 1,000 expired rows
per transaction, and receipt matching independently excludes expired rows. A
signed unrelated provider message can occupy this bounded buffer without
affecting Product Control customers. Retained reports use the existing rolling
30-day message window, not a permanent delivery archive.

A permanent (`Permanent`) bounce or complaint pauses only the affected person's
product/workspace subscription. Temporary or unclassified bounces are recorded
without automatically pausing. The portal shows the pause, and owner history
explains why retry is blocked. Queued messages are cancelled with
`recipient_suppressed`; leased messages stop at their next readiness check.
Already in-flight requests can complete. Other subscribers remain independent.

Saving preferences, renewing consent or changing an address does **not** clear a
pause. Hard-bounce recovery now has a separate explicit customer action described
below. Complaint holds require review and cannot use that action. There is no
force-send control. Preference choices remain editable without implying that
sending resumed.

For local verification, use signed loopback provider fixtures with fictional
customers. Cover callbacks before acceptance, duplicates, delivery after a
bounce, complaint precedence, permanent versus temporary bounce, unchanged
attempt counts, subscription isolation and persistent pauses after saves. No
provider credential or real email is needed. A later live release must qualify
actual webhook provisioning, delivery and provider suppression policy separately.
See [ADR-1722](../adr/1722-product-control-email-provider-feedback.md).

### Recovering notifications after a hard bounce

The portal shows the reason and time of a personal pause. To recover a hard
bounce, the customer verifies a **different** sign-in email address in their
account, returns to **Your email notifications**, selects at least one category
and explicitly clicks **Resume notifications with my verified replacement
address**. The server renews Auth consent and checks that the currently verified
address differs from the address used by the bounced message. Case-only edits
do not qualify. No address is entered into the Product Control request.

The action enables only future eligible updates. It does not send a test email,
replay the inbox, reopen cancelled deliveries, reset attempts or extend delivery
deadlines. Changing checkboxes or clicking **Save email preferences** continues
to preserve a pause. Owners cannot resume another person's notification consent.
Complaint pauses remain under review, even if the address changes.

A successful confirmation returns an immutable recovery UUID, prior/current
preference versions, prior/current recipient generations, selected categories
and confirmation time. After an uncertain response, use **Check or repeat the
same recovery**; the portal keeps the original request unchanged and never
resubmits automatically. Sign-out or a workspace change clears that in-memory
request. If the page was reloaded, inspect personal recovery history and current
pause state before reviewing another action. A receipt proves the historical
confirmation; a later bounce or complaint can pause the subscription again.

The private confirmation route is `POST
/v1/product-control/customer/{slug}/email-recovery` with exactly `id`,
`expected_version`, `expected_generation`, `work_outcomes`, `help_replies`.
It requires current customer cookies, selected organization and same-origin
HTTPS. Repeating the same authorized request returns its original receipt before
renewing a grant. Changed bodies with the same ID, stale versions/generations,
complaints or an unchanged address return 409. Invalid fields return 400;
unavailable dependencies or capacity fail without clearing the pause.

Customer history is `GET
/v1/product-control/customer/{slug}/email-recoveries`; it is personal to the
signed-in user and selected workspace. Owners use **Notification email → View
notification recovery history**, `GET /v1/apps/{slug}/product/email-recoveries`,
or:

```sh
gregale product email recoveries --app EXPORT_APP
gregale product email recoveries --app EXPORT_APP --tenant TENANT_UUID --json
```

Owner history accepts optional `tenant` and `cursor`; personal history accepts
only `cursor`. Preserve the original scope with a returned cursor. Both paginate
25 newest records at a time within 30 days, including tied timestamps. Recovery
is capped at 50 confirmations per subscription in that window. Owner API reads
require the existing read/MFA policy and ownership; browser reads require an
owner dashboard session. No owner/customer credential can inspect another
account's recovery records. History omits addresses, Auth identities, grants and
encrypted recipient evidence.

The sender retains only encrypted normalized-address evidence for accepted
messages from this implementation onward. Historical pauses without that
evidence cannot recover automatically. The portal also withholds recovery when
the required decryption identity is unavailable; review host identity retention
rather than bypassing the pause. This uses the existing age identity overlap and
Auth protocol; no Auth runtime upgrade or new secret is introduced.

Recipient generations advance only on confirmed recovery. A late hard bounce
for the previous generation remains visible on its original message but cannot
pause the replacement address or cancel its queued updates. A current-generation
bounce pauses normally. A complaint from any retained generation still creates
a review hold. Replaying an old confirmation cannot remove that newer hold.

Preference saves and recovery share a per-subscription consent gate before Auth
rotates the recipient capability. Requests have a five-second budget; PostgreSQL
waiters release their connection between bounded attempts. Keep pool capacity
available for the holder's short repository operations. Feedback transactions
use a separate short lock, so a callback during verification can still prevent
recovery. An Auth outage, revoked authority or a conflicting save cannot bypass
the final pause/version/generation checks.

Apply the platform recovery migration before using these routes. Product Control
remains default off and email defaults disabled. Local verification uses
fictional verified/replacement addresses, a loopback provider and signed
callbacks. Real address verification and provider suppression policy still need
live qualification before enabling real sending. See
[ADR-1723](../adr/1723-product-control-notification-recovery.md).

### Customer Help

Customers can use **Get help** on a feature or run, or open **Help and requests**
for a workspace question. Sending is explicit. Gregale attaches a permitted
snapshot of the assigned plan, blocking reason, current allowance or retained
run reference. Inputs/results, prices and billing amounts are not attached.
Context describes the time of submission; it is not a current health check.

Requests are shared within the verified workspace. General workspace requests
need any product permission; feature requests need Run work or View billing;
run requests need View work. The same checks apply to history and saved-reference
lookup, so members who lose a permission cannot read that request's message or
response. New Auth organization/realm mappings cannot inherit old help history.
Messages and responses are workspace-visible; the form is not an internal staff
note channel. Use a workspace question when the current role cannot attach
feature or run details.

Owners open **Customer Help** in the Product Control console, filter the queue,
review the original message/context, and update its status and latest response.
Statuses are Open, In progress and Resolved. The response is visible to authorized
workspace members. Existing links open readiness, activity and run review; a
help status update does not execute a repair or change access, charges or work.
The initial message and context are immutable; only the latest response/status
are retained, not a full conversation or edit history.

The internal customer API provides `POST /v1/product-control/customer/{slug}/help`,
`GET` on that collection, and `GET .../help/{request}`. Creation accepts
`{id,kind,message,feature?,operation_id?}`. Kind is `workspace`, `feature` or `run`,
with only the matching reference allowed. Account/app/tenant/realm and context
are derived server-side. Customer reads allow `status` and `cursor` only;
direct reads and creation allow no query selectors. Use the customer session and
live `X-Product-Organization`, never an owner/service token.

Owner reads use `/v1/apps/{slug}/product/help` and `.../help/{request}`; collection
reads accept optional `tenant`, `status` and `cursor`. Status filters are `active`,
`all`, `open`, `in_progress` or `resolved`. Owner `PUT .../help/{request}` accepts
`{expected_version,status,response}`. Reload on a conflict. An exact creation
retry retains the original UUID/body; an exact status retry observes an already
matching value without applying another change.

There are at most twenty active and two hundred retained requests per workspace,
twenty per page, a twenty-second service deadline, 2,000-byte message/response
bounds and a 16 KiB request-body bound. The existing flag stays default-off.
The browser saves only a request UUID/hash for acknowledgement-loss recovery;
reload reads its receipt and never automatically sends another request. Private
data is cleared on logout/workspace change. No email notification is sent.
See [ADR-1715](../adr/1715-product-control-customer-help.md).

### Customer updates

**Updates** shows the latest retained run outcome, runs needing product-team
attention, and owner help responses/status changes from the last thirty days.
Use All/Unread, Refresh or Older updates to browse; View run and View help request
open existing readers without executing work or marking the item read. Each
source has one current item, so intermediate help edits can coalesce. An item
leaves the window thirty days after its latest source change; an attention item
can also disappear when its retained run no longer needs reconciliation.

Read status is shared with permitted members of this workspace. Mark read or
Mark unread changes only that metadata. A newer source update becomes unread.
Current role permissions filter both the list and its unread count: View work
for runs, and the existing kind-specific help permissions for help updates.
Changing organization or Auth realm does not transfer old help/read state.
Existing retained tenant work remains governed by current work permissions.

The private customer API adds `GET .../notifications?filter=all|unread&cursor=...`
and `POST .../notifications/read` with `{id,revision,expected_version,read}`.
The full prefix is `/v1/product-control/customer/{slug}`. Use the live customer
cookie, same Origin and verified `X-Product-Organization`; bodies cannot select
authority. Keep the ID, revision and read version from the observed item. Refresh
on 409; a stale revision cannot acknowledge a newer update. After a lost response,
read the page before making another explicit choice. The portal does this
automatically and never replays an acknowledgement POST.

Pages contain at most twenty items; unread count covers the whole visible window.
Bodies are bounded at 4 KiB, responses at 128 KiB, service calls at twenty seconds,
and active read markers at one thousand per product tenant across realms.
Expired markers are ignored and pruned during metadata writes; this is not a
scheduled purge or an event archive. Only source references and metadata appear
in this projection, not messages, responses, inputs, results or commercial terms.
There is no email delivery. All inbox routes/assets remain behind the existing
default-off flag. See [ADR-1717](../adr/1717-product-control-customer-notifications.md).

### Usage and next steps

The customer portal shows used and remaining starts, a progress meter for a
positive allowance, an 80% warning, and the exact next reset at the first day of
the next UTC month. Counts include accepted failed, cancelled and pending runs
across plan and price revisions. Changing plans keeps this month's used starts;
usage above a downgraded limit remains visible. A zero grant explicitly says
the plan includes no starts rather than suggesting a reset will unlock it.

Paused workspace access, insufficient run permission and unavailable deployment
bindings have separate explanations. The portal suggests an existing plan
review only for a near-limit or exhausted feature when the user has Manage plan
and the current catalog offers a transition. It loads choices and prices for
the existing explicit review/confirmation flow. Other users are directed to a
workspace administrator or the product team. Guidance does not repair access,
reserve allowance or automatically start work/change a plan.

`GET /v1/product-control/customer/{slug}/guidance` returns the same projection as
the normal product response's `guidance` field, without reading work history or
observing onboarding access. Use the customer session cookie and
`X-Product-Organization` from the live workspace list. No query selectors are
accepted, including tenant, period or role overrides. Only Run work or View
billing roles receive feature counts; other permitted roles receive no feature
usage. The dedicated response contains no prices, billing amounts, binding
details or work payloads. Reads share a twenty-second deadline and recheck the
assignment, permission revision and UTC month. This is a current observation;
start and plan-confirmation transactions remain authoritative.

Failed current refreshes remove the old guidance and ask the customer to refresh.
Older in-flight refreshes cannot overwrite a newer view. Logout/workspace changes
clear guidance and fence its actions. Existing saved-request lookup and recovery
remain independent of allowance warnings. All routes/assets remain internal,
private/no-store and default-off, including unsupported methods at the pre-mux
gate. See [ADR-1714](../adr/1714-product-control-customer-usage-guidance.md).

New starts require an `expected_quote` hash bound to the authenticated account,
app, tenant, feature, assigned catalog/plan, currency, price, monthly allowance
and UTC month. The admission transaction checks it before consuming a start.
`409 product_quote_changed` means no new admission occurred: refresh, review the
new terms and explicitly confirm again. The UI does this without automatic
resubmission; the CLI uses `product quote` plus `start --quote HASH`. Retained
same-key retries keep their admitted price. The saved receipt records the
confirmed quote. A quote is neither a reservation nor a finalized invoice.

Before sending work, the browser saves a scoped versioned key, input SHA-256,
quote, timestamps and eventual Operation ID to local storage. No raw request
contents or credentials are stored there. Local storage and Web Locks must work
in the HTTPS browser; storage failure stops submission. Locks serialize tabs,
and session-change messages clear workspace memory in other tabs. Reload/login
uses a read-only submission lookup, including receipts for retired features. It
never starts work. Unresolved requests require the original inputs, the saved
key and another explicit retry within one day. After that, or if metadata is
invalid/too numerous, contact the product team to resolve the original outcome.
Sign-out clears the workspace but keeps these non-secret recovery records.
Deleting browser storage loses local recovery keys; inspect backend history
before creating any replacement. Result/file retention remains independent.

A runnable [export pilot](../../examples/customer-operation-export/product-control/README.md)
includes two plans and assignment commands for three existing internal customers.

## Explain a product run

```sh
gregale product explain OPERATION_UUID --app exports
gregale product explain OPERATION_UUID --app exports --json
# Follow the next_generation cursor when execution history has another page:
gregale product explain OPERATION_UUID --app exports --after GENERATION
```

This owner-only read links the authenticated app, customer, feature, admitted
plan/revision, definition and deployment, allowance used before admission,
execution generations, verified outcome, delivery state and provisional charge.
The allowance decision and binding are saved with the commercial receipt in the
admission transaction. The receipt and retained outcome are read together;
execution history and delivery state are separate projections and can change
during work. Execution history is bounded and paginated.
Input, result payloads, artifact locations and execution credentials are excluded.

`settled` means verified success recorded once at the original unit price;
`pending`, `awaiting_reconciliation` and `not_billable` distinguish ongoing,
unknown and failed/cancelled outcomes. Zero-priced success can be settled with
zero amount. Receipt evidence survives Operation result expiry. Expired work has
no retained outcome or execution detail; an uncharged expired receipt is
`unsettled`, without inventing a failure or success. New failed/cancelled receipts retain their verified nonbillable outcome after result expiry. A permanently waived receipt reports `waived` even if it later succeeds. Receipts from before the
evidence migration can lack historical binding and allowance fields; the CLI
reports that absence explicitly. This view explains accepted work, not rejected
submission attempts or finalized invoices.

## Review and close a billing month

All commands below use an owner CLI profile unless `--self` is present. Prices
and credits use integer millicents (100,000 millicents = one currency unit).
Current usage is provisional; `statements list` lists actual immutable statements
and credit notes rather than wrapping a usage projection.

```sh
gregale product statements review --app exports --tenant TENANT_UUID --period 2026-09 --json
gregale product statements receipts --app exports --tenant TENANT_UUID --period 2026-09 --json
# Follow next_cursor with --cursor UUID on receipts/list.

# Reconcile an unknown outcome through Operations when execution evidence exists.
# Otherwise explicitly and permanently waive any charge for that original run:
gregale product statements waive --app exports --tenant TENANT_UUID --period 2026-09 \
  --operation OPERATION_UUID --reason 'Internal pilot outcome cannot be verified'

# Review again after any resolution, then confirm exactly that review:
gregale product statements review --app exports --tenant TENANT_UUID --period 2026-09 --json
gregale product statements finalize --app exports --tenant TENANT_UUID --period 2026-09 \
  --review-hash REVIEW_HASH

gregale product statements list --app exports --tenant TENANT_UUID --period 2026-09 --json
# Customers can also use statements review/list --app exports --self --period 2026-09.
```

Only a closed UTC month can be finalized. Every receipt must have verified
success/failure/cancellation evidence or an explicit waiver. Missing/expired
Operation details never imply failure. First terminal proof is retained after
result expiry; older receipts backfill only independently recorded successes.
`product_billing_unresolved`, `product_billing_period_open` and
`product_billing_review_changed` return 409 without finalizing. Review lines
partition starts into charged successes, failed, cancelled, waived and unresolved.
They group by feature, catalog revision, plan and admitted price. Counts across
price changes still consume the original monthly allowance. Provisional usage
counts all verified successes, including a later success whose charge was waived;
its amount excludes waivers. Statement success counts include only charged successes.

A waiver applies only to an unresolved receipt. It permanently removes its charge,
even if execution is running or later succeeds. It does not cancel or restart
work, change its verified outcome, or restore a start allowance. Preserve the
same reason when retrying. The service does not undo waivers or edit reasons.

```sh
# Credits target verified successful, unwaived receipts in a finalized month.
# Choose and save CREDIT_UUID before the first request; preserve every field.
gregale product statements credit --app exports --tenant TENANT_UUID --period 2026-09 \
  --id CREDIT_UUID --operation OPERATION_UUID --amount-millicents 5000 \
  --reason 'Service credit for the internal export trial'

# One immutable external reference for EACH statement or credit note:
gregale product statements handoff --app exports --tenant TENANT_UUID --period 2026-09 \
  --document DOCUMENT_UUID --invoice-id INTERNAL_INVOICE_OR_CREDIT_REFERENCE
gregale product statements handoff-status --app exports --tenant TENANT_UUID --period 2026-09 \
  --document DOCUMENT_UUID --json
```

Credit notes are separate documents with negative amounts. Their cumulative
amount cannot exceed the target run's original charge, including concurrent
requests. They never rewrite a finalized statement. A repeated credit UUID must
retain its original customer, period, operation, amount and reason; otherwise it
conflicts. Credit reasons are visible to the customer and must contain no secrets.

Each document can be handed off once. Same-document/same-reference retries return
the original handoff; another reference or reuse for a different document returns
`product_billing_conflict`. Reserve the external reference through this API before
issuing an invoice. Use the document UUID as the external provider's idempotency
key; after a lost external response, look up that key before retrying. Statement
handoffs carry the original positive charge, credit note handoffs the negative
adjustment. Export both document types. This records an invoicing decision;
Gregale does not collect money, refund it, calculate tax or guarantee an external
provider's idempotency. External rounding remains the invoicer's responsibility.
Do not export existing request rate-card statements as a second charge for the
same product work; these ledgers are independent and are never automatically added.

### Export a statement to your own billing provider

Instead of a manual handoff, a finalized statement can be invoiced in the
account's own Stripe, Paddle or Lago
([ADR-20261018212918](../adr/20261018212918-product-control-payment-collection.md)).
Connect the provider once with `PUT /v1/account/billing-connections/{provider}`;
its collection mode and Stripe automatic tax apply as for other statement exports
([ADR-1311](../adr/1311-statement-invoice-collection.md),
[ADR-1313](../adr/1313-stripe-automatic-tax-on-exports.md)).

```sh
gregale product statements export --app exports --tenant TENANT_UUID --period 2026-09 \
  --document STATEMENT_UUID --provider paddle [--customer ctm_01...]
gregale product statements export-status --app exports --tenant TENANT_UUID --period 2026-09 \
  --document STATEMENT_UUID --json
```

The owner API is `POST /v1/apps/{slug}/product/billing/documents/{document}/invoice?period=YYYY-MM`
with `{"tenant_id","provider","customer"}` and the matching `GET` with `tenant_id`.

- The customer defaults to the tenant's external reference when it is one of
  the provider's customer IDs (`cus_…`, `ctm_…` or a Lago external customer ID);
  otherwise pass `--customer`.
- Each invoice line is one feature and plan at one admitted price: charged
  successes times that price, rounded to the cent with a rounding item where
  needed. Failed, cancelled, waived and unresolved runs are never invoiced.
- The provider invoice ID becomes the statement's handoff. A statement is
  either handed off by hand or exported, never both: a manual handoff blocks
  export, and once an export starts a manual handoff returns 409
  `product_billing_exported`, through the API and the console.
- A retry with the same provider resumes the same invoice; another provider
  conflicts. The export row exists before any provider call, so a lost
  response never creates a second invoice.
- Credit notes are not exported. Record them with `statements handoff` after
  issuing the credit in the provider. Paddle and Lago reject negative documents.
- `export-status` and `GET /v1/account/statement-invoices` show the provider's
  payment status, amount remaining, due date and hosted invoice link, synced by
  the existing payment-status worker
  ([ADR-1504](../adr/1504-statement-invoice-payment-status.md)).

Gregale still does not collect money, calculate tax or act as merchant of record;
the owner's provider does. Stripe and Lago can collect when the connection's
collection mode asks them to. Paddle exports stay drafts unless the Paddle
connection uses collection `send`
([ADR-20261019100333](../adr/20261019100333-paddle-send-collection.md)), which
bills them once the customer has a billing address.

#### Customer billing details

A Paddle customer can be billed only with an address; without one, a `send`
export fails with 422 "Paddle customer has no address" and keeps the draft
([ADR-20261020120000](../adr/20261020120000-product-control-customer-billing-details.md)).
Connect Paddle with `gregale account billing-connection connect paddle --collection send`
and let customers enter their own details: the portal's **Billing details**
section, for roles with `billing:manage` (the workspace owner by default), reads
and saves the workspace's address (country, postal code, region, city, two lines)
and, optionally, its business name and tax identifier. The routes are
`GET`/`PUT /v1/product-control/customer/{slug}/billing-details`.

- **Kept in Paddle only.** The details are written to the Paddle customer named by
  the customer's platform tenant external reference (`ctm_…`). Gregale stores none
  of them; the audit event `product.customer.billing_details_updated` names the
  changed parts (`address`, `business`) and never their values.
- **Unavailable** (409 `product_billing_details_unavailable`) when the account has
  no Paddle connection, the tenant's external reference is not a Paddle customer ID,
  or the Paddle key cannot manage customer addresses.
- **Billing.** With collection `send`, the next export, or a retry of one that
  failed for lack of an address, bills the transaction with the customer's active
  address and business, so Paddle issues the invoice with the connection's payment
  terms. A billed invoice cannot be changed in Paddle, so finalize statements only
  after review.
- Leaving the business name empty keeps the current business.

#### Payment status on statements

Each statement in `GET /v1/apps/{slug}/product/billing/documents` (and so
`statements list --json`) carries an `invoice` object once its export starts:
`provider`, `invoice_id`, `dashboard_url`, `export_status`, `payment_status`,
`overdue`, `currency`, `amount_minor`, `amount_remaining_minor`, `due_at`,
`paid_at` and `hosted_invoice_url`. Amounts are in the currency's minor unit
(cents). `overdue` has the ADR-1504 meaning: open, past `due_at`, with an amount
remaining. Credit notes and statements handed off by hand have no `invoice`.

The owner console shows each exported statement's invoice in place of the
handoff form, for example
`Invoiced in Stripe as in_… · overdue · due 2026-11-01 · EUR 12.50 remaining`,
with links to the provider dashboard and the customer's hosted invoice. An
export that has not finished says so; rerun `statements export` with the same
provider to resume the same invoice.

Customers see a reduced `invoice` on completed exports only, through
`statements list --self`, `GET /v1/platform-tenant-self/product/billing` and the
portal's **Statements and credits** section: payment status, overdue, amounts,
dates and the hosted invoice link. They never see the provider, invoice ID,
provider customer ID, dashboard link or an unfinished export. The hosted link is
kept only when the provider returned a plain `https` URL without credentials;
the portal labels it **View and pay invoice** while the invoice is open. Gregale
never renders a payment form.

#### Overdue holds

An app can refuse new starts from customers who have not paid an exported
statement, mirroring API monetization's consumer holds
([ADR-1315](../adr/1315-block-consumers-with-overdue-invoices.md)). The policy is
off until the owner sets it, and it lives outside the immutable catalog.

```sh
gregale product billing overdue-policy set --app exports --block-overdue-after-days 14
gregale product billing overdue-policy inspect --app exports --json
gregale product billing overdue-policy clear --app exports
```

The owner API is `GET/PUT/DELETE /v1/apps/{slug}/product/billing/overdue-policy`
with `{"block_overdue_after_days": 0..365}` (`0` holds as soon as an invoice is
past due) and `GET /v1/apps/{slug}/product/billing/overdue-holds`.

- **What counts.** A Product Control statement exported to Stripe, Paddle or Lago
  whose payment status is `open` or `uncollectible`, with an amount remaining and
  a due date at least the policy's days ago. Drafts have no due date and never
  count; neither do paid, void or deleted invoices. Credit notes are not exported.
- **Holds.** Every 5 minutes each apid replica tries to recompute one hold per
  customer, naming its oldest qualifying statement; an advisory lock lets one
  replica do it at a time. Placing and lifting are audited as
  `product.customer.overdue_blocked` and `product.customer.overdue_unblocked`;
  policy changes as `product.billing.overdue_policy_updated` and
  `product.billing.overdue_policy_deleted`. A held invoice's payment status is
  read at least every 15 minutes (ADR-1504).
- **Enforcement.** Operations admission refuses a held customer's new start with
  402 `product_invoice_overdue`, naming the invoice's due date and the provider's
  hosted page. No start is consumed. A retry of work that was never admitted is
  checked again; work already admitted keeps running and settling, and usage,
  results and billing stay readable.
- **Release.** A hold stops applying as soon as its invoice syncs as paid, void or
  deleted, and the next recomputation removes it. Clearing the policy lifts every
  hold at once. A customer with another overdue statement stays held by that one.
- **Separate from suspension.** A hold is not the owner's `suspended` flag. Both
  are shown separately, and lifting one never lifts the other.
- **Where it shows.** The owner console lists the policy and held customers, and a
  held customer's page shows a **Payment hold** row. Change the policy with the
  CLI or API; the console does not edit it. The customer portal explains the
  hold with the due date and a **View and pay invoice** link, disables new runs,
  and its usage guidance marks features `invoice_overdue`.
- **Rollback.** With `FAAS_PRODUCT_CONTROL_ENABLED` off, holds are not
  recomputed or enforced, because no new Product Control starts are admitted.

### Bill every customer in a billing run

Instead of reviewing, finalizing and exporting each customer by hand, an
account's month-end billing run
([ADR-1503](../adr/1503-month-end-billing-runs.md)) can do it for every Product
Control customer with the `product_customers` source:

```sh
gregale account billing-run start --month 2026-09 --sources product_customers --export stripe --wait
gregale account billing-run show RUN_ID --outcome held
gregale account billing-run schedule-set --sources app_consumers,platform_tenants,product_customers --export stripe
```

The source is accepted only while Product Control is enabled. It is never added
by default, so existing runs and schedules keep billing what they did.

- **Customers.** One item per app and assigned customer with receipts in the
  month, for apps with a published catalog. A tenant that is a customer of two
  apps gets two items. Items report `source_kind: product_control`, the app in
  `app_id`, the customer in `platform_tenant_id` and the statement document in
  `statement_id`.
- **Steps.** The run reviews the month. It holds the customer when the month is
  open (`product_billing_period_open`) or has runs with no recorded outcome
  (`product_billing_unresolved`); waive or reconcile them, then start another run
  for the month. Otherwise it finalizes with the review's hash (`--no-finalize`
  only reviews, reported as `drafted`) and, with `--export`, exports the
  statement like `statements export`. If the receipts change between the review
  and finalizing, the item is retried.
- **Already billed.** A statement that was already exported, or whose run is
  re-done after the month was finalized, is `up_to_date`. A statement handed off
  by hand is `skipped`. A zero total is never exported.
- **Audit.** Finalizing and exporting emit the same `product.billing.finalized`
  and `product.billing.invoice_exported` events as the endpoints.
- **Rollback.** With Product Control disabled, pending Product Control items are
  `held`; start another run for the month after enabling it. A previous apid
  release ignores Product Control items. If one completes a run during a rolling
  deploy before its Product Control items are billed, start another run for the
  month.

The customer portal's **Statements and credits** section reads a selected month
and explicitly pages through finalized documents. It shows provisional charges
when the month has not closed or been finalized, and separates finalized charges
from credit notes. It does not claim a net total from a partial page. Customers
cannot waive, credit, finalize or hand off invoices. These reads use the same
verified workspace mapping and flag as feature execution.

Billing bounds in `pkg/api/limits.go`: 100 receipts/documents per page, 1,000
invoice groups per review (exceeding it fails rather than truncates), 512-byte
reasons and 255-byte external references. Finalized data survives flag rollback;
existing admitted operations continue recording terminal proof while the flag
is off. See [ADR-1704](../adr/1704-product-control-billing-lifecycle.md).

Billing records outlive their app
([ADR-20261020090607](../adr/20261020090607-product-control-billing-records-survive-app-purge.md)).
When an app is purged 7 days after `gregale apps rm`, its receipts, waivers,
statements, credit notes, invoice handoffs and catalog revisions are kept; its
catalog head, customer assignments and other configuration are removed. Kept
records are no longer served through the app's routes, and a new app that
reuses the slug never sees them. They are erased with the account's final
deletion. Restore an app within the 7 days to keep using its billing routes.

## Internal API

All reads/work mutations are authenticated. The gated login and refresh endpoints
authenticate against Application Auth; the portal shell and static assets are public only while the flag is on.
Owner reads use the existing read/MFA policy; owner mutations use deploy-write,
MFA and verified-email policy. Tenant identity always comes from the credential.

| Method and path | Purpose |
| --- | --- |
| `GET /v1/apps/{slug}/product/billing/review` | Live or frozen review; tenant_id and period selectors |
| `GET /v1/apps/{slug}/product/billing/receipts` | Retained commercial evidence with cursor |
| `GET /v1/apps/{slug}/product/billing/documents` | Finalized statements and credit notes with cursor, each exported statement with its invoice and payment status |
| `POST /v1/apps/{slug}/product/billing/statements/finalize` | Confirm a reviewed, resolved, closed month |
| `POST /v1/apps/{slug}/product/billing/receipts/{operation}/waive` | Permanently waive an unresolved charge |
| `POST /v1/apps/{slug}/product/billing/credits` | Idempotent credit note for a finalized receipt |
| `GET/POST /v1/apps/{slug}/product/billing/documents/{document}/handoff` | Recover or record the immutable invoice reference |
| `GET/PUT/DELETE /v1/apps/{slug}/product/billing/overdue-policy` | Read, set `{block_overdue_after_days}` or clear the overdue policy; clearing lifts every hold |
| `GET /v1/apps/{slug}/product/billing/overdue-holds` | Customers whose new starts are refused for an overdue invoice |
| `GET /v1/platform-tenant-self/product/billing` | Authenticated tenant's selected month and documents, with completed invoices' payment status and hosted link |
| `GET /v1/product-control/customer/{slug}/billing` | Cookie-authenticated workspace billing |
| `GET /v1/apps/{slug}/product` | Current catalog; optional `revision` |
| `PUT /v1/apps/{slug}/product` | Publish catalog spec |
| `GET /v1/apps/{slug}/product/customers` | Assigned customers; optional `cursor` |
| `GET /v1/apps/{slug}/product/customers/overview` | Assigned customer readiness/activation/work overview; optional `cursor` |
| `GET /v1/apps/{slug}/product/customers/activity` | Retained history for required `tenant`; optional scoped `cursor` |
| `PUT /v1/apps/{slug}/product/customers/{tenant}` | Assign `{plan, revision?, suspended?, auth_organization_id?}` |
| `POST /v1/apps/{slug}/product/onboarding/workspaces` | List owner/admin workspaces with a transient `{auth_access_token}` |
| `POST /v1/apps/{slug}/product/onboarding/preview` | Review `{request, auth_access_token}` without creating a customer |
| `POST /v1/apps/{slug}/product/onboarding/confirm` | Atomically apply `{confirmation:{request,expected_hash}, auth_access_token?}`; exact retries recover the receipt |
| `GET /v1/apps/{slug}/product/onboarding/{onboarding}` | Retained setup receipt and observed activation progress |
| `GET /v1/apps/{slug}/product/usage` | `tenant_id`, optional UTC `period=YYYY-MM` |
| `GET /v1/apps/{slug}/product/operations/{operation}/explain` | Owner explanation; optional execution `after` generation |
| `GET /v1/platform-tenant-self/product` | Customer plan, features, recent work and usage; `app` slug, optional `period` |
| `POST /v1/platform-tenant-self/product/start` | `{app, feature, input, expected_quote}` plus stable `Idempotency-Key` |
| `POST /v1/platform-tenant-self/product/submissions/lookup` | Read-only `{app, feature, idempotency_key}`; no admission |
| `POST /v1/product-control/customer/{slug}/session` | Email/password login; HttpOnly session cookies |
| `POST /v1/product-control/customer/{slug}/session/refresh` | Rotate refresh credential in cookies |
| `DELETE /v1/product-control/customer/{slug}/session` | Revoke current Auth session and clear cookies |
| `GET /v1/product-control/customer/{slug}/workspaces` | Currently verified mapped organizations |
| `GET /v1/product-control/customer/{slug}/product` | Assigned view and feature quotes |
| `GET /v1/product-control/customer/{slug}/guidance` | Read-only current UTC-month allowance and next steps; no query selectors |
| `GET /v1/product-control/customer/{slug}/notifications` | Current visible updates; optional `filter=all|unread` and scoped `cursor` |
| `GET /v1/product-control/customer/{slug}/account-recovery` | Pre-sign-in 303 to the current owned Auth recovery page; no query parameters or credentials |
| `POST /v1/product-control/customer/{slug}/notifications/read` | Explicit workspace-shared `{id,revision,expected_version,read}` metadata update |
| `GET /v1/apps/{slug}/product/plan-policy` | Owner's current self-service choices |
| `GET /v1/apps/{slug}/product/team-policy` | Owner's current role-delegation policy |
| `POST /v1/apps/{slug}/product/team-policy/preview` | Read-only delegation review |
| `PUT /v1/apps/{slug}/product/team-policy` | Publish `{expected_head,expected_revision,policy}` with catalog CAS |
| `GET /v1/product-control/customer/{slug}/team` | Current members/invitations and approved roles; requires Manage team and live Auth authority |
| `POST /v1/product-control/customer/{slug}/team/preview` | Read-only `{id,action,target_id?,email?,role?,expires_in?}` review |
| `POST /v1/product-control/customer/{slug}/team/confirm` | `{request,auth_snapshot,expected_hash}`; atomic Auth mutation/receipt |
| `GET /v1/product-control/customer/{slug}/team/actions/{change}` | Read a scoped recorded action; no mutation or token recovery |
| `GET /v1/product-control/customer/{slug}/team/actions` | Workspace reviewed history; optional scoped `cursor` |
| `POST /v1/apps/{slug}/product/plan-policy/preview` | Owner's read-only transition-policy review |
| `PUT /v1/apps/{slug}/product/plan-policy` | Publish the reviewed policy with catalog CAS |
| `GET /v1/product-control/customer/{slug}/plans` | Current terms and explicitly offered changes; requires Manage plan |
| `POST /v1/product-control/customer/{slug}/plans/preview` | Read-only `{id,plan,revision?}` review |
| `POST /v1/product-control/customer/{slug}/plans/confirm` | Atomic `{request,expected_hash}` assignment and retained receipt |
| `GET /v1/product-control/customer/{slug}/plans/changes/{change}` | Read a scoped immutable confirmation receipt |
| `GET /v1/product-control/customer/{slug}/plans/changes` | Scoped self-service history; optional `cursor` |
| `POST /v1/product-control/customer/{slug}/start` | `{feature, input, expected_quote}` + `Idempotency-Key` |
| `POST /v1/product-control/customer/{slug}/submissions/lookup` | Read-only `{feature, idempotency_key}` |
| `GET /v1/product-control/customer/{slug}/operations/{id}` | Retained status/result |
| `GET /v1/product-control/customer/{slug}/work-history` | Receipt-backed search; optional `operation_id`, `feature`, `state`, UTC `from`/`through`, scoped `cursor` |
| `GET /v1/product-control/customer/{slug}/operations/{operation}/details` | Customer-safe original plan, allowance, outcome and permitted charge |
| `GET /v1/product-control/customer/{slug}/operations/{id}/events` | Existing bounded events/SSE |
| `POST /v1/product-control/customer/{slug}/operations/{id}/cancel` | Cooperative cancellation |
| `GET /v1/product-control/customer/{slug}/operations/{id}/artifacts/{artifact}` | Verified private download |

Cookie-authenticated workspace requests carry `X-Product-Organization`, selected
only from the live authenticated workspace list. These routes grant no owner
access. Their acceptance/lookup URLs stay in this cookie-scoped namespace.

The `pkg/api` Go client powers the internal CLI. These routes remain excluded
from the public OpenAPI/SDK contract during the internal pilot.

## Accounting and rollback contract

Admission holds the existing Operations account/tenant locks. It validates the
assigned revision and grant, counts this customer's starts for the feature in
the UTC admission month, and commits a price receipt with the operation and its
execution. A concurrent request cannot pass a full allowance. Idempotent retries
consume no further start, and plan/revision changes do not reset that month's
count. Starts include failed, cancelled and unresolved operations; a start cap
is an execution budget, not a promise of successful output.

The first verified `succeeded` transition settles that receipt in the same
PostgreSQL transaction as the operation and its event. The receipt is keyed by
logical operation, so retries, execution generations and webhook redelivery
cannot add a second charge. Invalid output, unknown outcomes, failures and
cancellation do not settle it. Explicit successful reconciliation uses the
original price. A completed receipt cannot be repriced by a later assignment.
Success is attributed to the original UTC admission month, including work that
finishes later. Receipts survive Operation result/event expiry; deleting the
owning app, tenant or account cascades its Product Control records.

Usage and the CLI's statements are **provisional month projections**, suitable
for reviewing an invoice handoff. They are not finalized invoices, a payment
processor, taxes, subscriptions, refunds or a complete billing ledger. Pending
work can settle into an earlier month. This pilot adds no automatic handoff to
request-based API monetization and no combined total; operators must select one
commercial charging basis for a capability. Gregale's own compute billing stays
under its existing RAM-and-running-seconds contract.

Disabling the operator flag removes every Product Control API/portal surface
and blocks new Product Control starts. Legacy submission paths still cannot
bypass the opted-in app's product policy. Already admitted operations retain their execution,
recovery and success settlement, preventing lost commercial evidence during
rollback. Existing tenant Operations reads remain under their original policy.

## Qualification

`TestProductControlInternalBoundary` covers the default-off switch, tenant/owner
separation, assigned-plan filtering, direct-submission refusal and the separate
Operations gate. `TestProductControlMemory` and `TestProductControlPostgres`
exercise transactional quota races, retries, pinned prices and success pricing.
The CLI has parser and safe-scaffolding tests. Native VM acceptance remains
governed by the underlying Operations lanes; this service does not qualify or
open an otherwise closed execution lane.

`TestProductControlDeploymentPilotMemory` and
`TestProductControlDeploymentPilotPostgres` exercise one product, two plans and
three isolated customers. They publish commercial terms before deployment,
change prices without migrating customers, redeploy and keep old request
identities, refuse missing bindings, confirm an unknown export outcome, and
retain the original charge evidence after removing the Operation result record.
The HTTP boundary also completes work while the internal flag is off and checks
that its explanation becomes available only after re-enabling the flag. Browser
contracts check remaining starts, unavailable bindings and uncertain retries.

For the next internal trial, run a real export on an Operations-qualified host
with three internal platform tenants and two reviewed plans. Verify the output,
compare `product explain` with each customer's provisional usage, then repeat
after a code deploy and a lost submission response. Portable fixture tests cover
the policy and persistence contract; qualification of the actual exporter,
a real Application Auth deployment and external invoicer integration remain separate rollout work.

`TestProductControlCustomerConsentMemory/Postgres` checks quote rejection without
quota use, retained original terms, unique Auth mappings, and concurrent-boundary
organization/realm changes. `TestProductControlCustomerPortalPilot` exercises real
HTTP handlers for three cookie-authenticated customers on two plans using a
simulated Auth boundary. Domain tests cover session cookies, Origin checks,
revocation, refresh, sign-out and redirect/response bounds. Browser contracts
cover guided input, changed-price reconfirmation, storage failure, input matching,
scoped recovery and logout fencing. These portable checks do not replace the
native exporter/file/reconciliation trial in the linked pilot guide.

Portal bounds are centralized in `pkg/api/limits.go`: Auth request/response
bodies 16/256 KiB, 10-second upstream timeout, at most 100 organizations, cookie
credential values at most 3,500 bytes, refresh cookies at most seven days, and
64 saved browser receipts per customer/product with at most 8 KiB each. The
one-day browser replay bound is shorter than supported Operations deduplication
retention. These bounds do not change a product plan's execution allowance.

`TestProductControlBillingMemory/Postgres` cover three customers, two plans,
closed-period/stale-review guards, durable nonbillable proof, permanent waivers,
late success, concurrent finalization and credits, per-run credit caps, invoice
handoff retries/conflicts and document pagination. PostgreSQL also verifies that
finalization waits for an uncommitted settlement before rejecting stale consent.
`TestProductControlBillingBoundary` covers the feature gate, owner/customer
separation, tenant-scoped reads and invoice handoff recovery. Portal browser
contracts verify signed credits, pagination, month selection and logout fencing.

`TestProductControlSelfServiceMemory/Postgres` cover competing/concurrent
confirmations, cross-customer ID collisions with rollback, pinned accepted prices,
carried allowances, stale usage, role/realm/mapping/status changes and history
pagination. `TestProductControlSelfServicePlansHTTP` checks live Auth authority,
private commercial projections, scoped receipts, owner policy CAS, Origin/CSRF
and the default-off routes. Browser contracts cover explicit comparisons,
storage failure, cross-tab references, response loss, read-only reload recovery,
history and logout/permission fencing. These checks do not qualify a real host.


### Unsubscribing from notification email

New notification emails carry an **Unsubscribe without signing in** link. It
opens a confirmation page for that recipient and workspace; only the confirmation
button changes preferences. Opening, scanning or previewing a link does not stop
notifications. Mailbox clients can also use the standard one-click POST headers;
both supported form encodings perform the same scoped action. No sign-in,
Application Auth call, provider call or confirmation email is needed.

An unsubscribe turns off both work outcomes and help replies, clears the recipient
grant, and immediately cancels queued messages. A leased job stops at its next
readiness check. A provider request already in flight may still finish; its
receipt and later delivery reports remain valid. Bounce and complaint pauses stay
in place. Other people, workspaces and products keep their own preferences.

The page removes the capability from browser history before requests or navigation
and keeps it only in memory. If a response cannot be checked, use **Check or repeat
unsubscribe** on that page or reopen the same original email link. Repeats recover
one recorded acknowledgement. After a later opt-in, an old acknowledgement is
explicitly historical and leaves the newer choices unchanged. Saving any newer
preferences invalidates older links without a receipt. Expired/replaced links show
a generic message; use a recent email or the signed-in portal instead.

Links and their receipts depend on the original email's retained 30-day delivery
window. The window starts when the email was queued, rather than when its link is
opened. Old delivered emails have no retroactively issued link. Hashes and
separately delivery-bound encrypted capabilities are retained privately; addresses,
Auth sessions and recipient grants are never put in unsubscribe URLs. Do not log
outbound unsubscribe headers, request bodies or raw query strings at a proxy. The
human link uses a URL fragment; the mailbox header URL necessarily uses a query
capability. The confirmation page sends no referrer and has no external assets.

The signed-in portal shows current notification choices and personal opt-out
history separately. Turning notifications back on requires selecting categories
and saving fresh consent for the verified address. Only future updates are
eligible; cancelled mail stays cancelled. Existing hard-bounce recovery and
complaint review requirements still apply.

Owners can open **Notification email → View notification opt-out history**, or
read the same scoped evidence with the private API/CLI:

```bash
gregale product email opt-outs --app exports --tenant CUSTOMER_UUID
gregale product email opt-outs --app exports --tenant CUSTOMER_UUID --json
```

Omit `--tenant` to inspect the owned product; follow only the returned `--cursor`
for older pages. Each page has at most 25 historical confirmations. The receipt
names opaque customer, subscription and source-email references, old/new consent
versions, recipient generation and confirmation time. It exposes no address,
capability or message content. There is no owner or CLI resume action. History
cascades with source delivery retention and is bounded by the existing delivery
cap; an opt-out is never rejected merely because a separate receipt quota is full.

Everything remains gated by `FAAS_PRODUCT_CONTROL_ENABLED=1`; its default remains
off. While Product Control is enabled, unsubscribe works even if email mode is
disabled, Auth is down or a sealing key has been retired. Mail sending still has
its independent existing switches. Real provider/mailbox qualification includes
DKIM signing of the two unsubscribe headers, link/tracking behavior, proxy log
redaction and the exact deployed API origin. This implementation does not enable
real flags or require provider credentials. Design and concurrency details are in
[ADR-1724](../adr/1724-product-control-notification-unsubscribe.md).

### Customer workspace activity

The portal's **Updates → Workspace activity** brings work outcomes, confirmed
self-service plan changes, finalized statements, credits and the latest help
updates into one read-only timeline. Use **Activity type** to filter and
**Refresh activity** to return to the newest page. **Older activity** follows the
returned cursor and replaces the page. The timeline loads when connecting to a
workspace; it does not poll. The unread Updates inbox above it keeps its existing
30-day window and shared read/unread choices.

Links open the permitted run/result, exact plan confirmation, billing month or
help request. Inspecting a historical plan confirmation preserves a pending plan
review. Work details may expire while their verified outcome remains. A help
request contributes only its latest retained update, and attention exists only
while a retained unexpired run still needs reconciliation. Missing history does
not establish failure, payment or a refund. There is no full audit-history claim.

The internal customer API is
`GET /v1/product-control/customer/{slug}/activity?filter=all&cursor=CURSOR`.
Supported filters are `all`, `work`, `plans`, `billing` and `help`. Use the live
customer session and `X-Product-Organization`; tenant/role/source query selectors
are rejected. Each page has at most 25 events under a 20-second request budget
and 64 KiB response cap. Cursors bind the selected workspace, current Auth realm,
role, effective permissions and filter; start at the newest page after changing
any of those. Concurrent source changes can alter subsequent pages.

View work permits work events; Manage plan permits plan confirmations; View
billing permits statements/credits and their amounts. Help follows its existing
context permissions. Filtering happens before page limits. Current membership,
tenant status, realm and mapping are rechecked on every request. The API exposes
no help text, internal notes, actor identities, signed reviews, owner URLs,
waiver reasons, handoff references, work prices or billing snapshots. Help and
plan history additionally check the recorded organization; work/billing follow
the currently mapped tenant's existing reader permissions.

Everything remains behind the default-off `FAAS_PRODUCT_CONTROL_ENABLED` flag.
No provider credentials, deployment, migration, extra worker or email enablement
are required. Owners retain their existing console activity view and
`gregale product customers activity --app exports --tenant CUSTOMER_UUID`.
See [ADR-1725](../adr/1725-product-control-workspace-activity.md).

## Customer run details

[ADR-1726](../adr/1726-product-control-customer-run-details.md) adds
`GET /v1/product-control/customer/{slug}/operations/{operation}/details` and a
Run details panel in Recent work. Work entries in Updates → Workspace activity
open the same panel. Both the read API and `portal-run.js` require the existing
`FAAS_PRODUCT_CONTROL_ENABLED` switch, which remains off by default.

Use the live customer session and `X-Product-Organization` workspace selection.
The API accepts no query selectors. View work is mandatory; View billing alone
does not grant run access. Prices, charge amounts, waivers, credit counts and
statement references are entirely absent without View billing. The source
boundary rechecks the current role policy, tenant status, Auth app and mapping.
Pausing new runs still permits authorized history reads.

The panel explains the original receipt's plan and admission month, one consumed
start and any retained allowance before/after admission. These counts are
historical; current plan changes do not rewrite them. Older receipts without
allowance evidence explicitly say it was not retained. Failed/cancelled work and
waivers do not replenish starts. Receipt outcomes survive result expiry. Missing
or expired unresolved work does not establish failure or a zero charge; request
help before starting a replacement. A waiver changes the charge, not the outcome.

A success charge is provisional until its admission month is finalized. The
panel shows a minimal finalized-statement reference and the signed sum/count of
credit notes for the exact run. Partial and full credits reduce the net charge.
A statement does not confirm payment; a credit note does not confirm a refund.
The billing-month link opens the existing authorized statement/document reader.
Result/files and Get help use their existing scoped readers and context.

Standalone result links close an unrelated explanation; late result payloads
and old download buttons cannot restore an earlier selection.

Details load explicitly and show the observation time; Refresh run details
checks for later evidence. The projection has no extra polling or storage in the
browser. Selection changes clear the previous result. Closing details, signing
out, workspace/role changes and access denial fence late responses and old
buttons. PostgreSQL shares a read-only repeatable-read snapshot across live
mapping/policy and receipt/document reads; memory returns detached values.
JSON is limited to 16 KiB and the service read budget is 20 seconds. No migration, worker, billing
mutation or external provider is needed. Source history scans are not claimed
to have constant cost. Deployed Auth and native execution qualification remain
outside this local implementation evidence.

## Customer work history

[ADR-1727](../adr/1727-product-control-customer-work-history.md) adds **Work
history** under Recent work. Search explicitly by exact run reference, original
feature, recorded/current outcome, or inclusive UTC start dates. Filters combine
with AND; **Clear filters** searches all retained receipts. **Next page** and
**First page** navigate bounded pages. Feature names may refer to features
removed from today's catalog. Original plans remain attached to their runs.

`GET /v1/product-control/customer/{slug}/work-history` requires the existing
customer cookie and selected `X-Product-Organization`, live Application Auth
membership and View work. Billing-only and plan-manager roles cannot read it.
No owner bearer token substitutes for that customer session. Both stores recheck
current account/app/tenant/Auth app/organization/role authority. Pausing new work
does not revoke an otherwise authorized read. Missing or foreign exact references
return an empty scoped page, with no indication of another workspace's records.

Optional query keys are `operation_id` (exact canonical UUID), `feature` (original
feature key), `state`, `from`, `through`, and `cursor`. State defaults to `all`;
other values are `accepted`, `running`, `succeeded`, `failed`, `cancelled`,
`requires_reconciliation`, `unavailable`. Dates are real positive-year
`YYYY-MM-DD` UTC dates, inclusive on both ends, for the original admission time.
A reversed range, invalid state/reference/date, duplicate key, unknown selector
or invalid/mismatched cursor returns 400. The response echoes the normalized
filter and current observation time.

Pages contain up to 25 entries, newest admission first, with UUID descending as
the tie break at microsecond precision. Continue using the returned `next_cursor`
and exactly the same filters and current workspace/access. Cursors are bound to
scope, role, effective permissions and all filters, limited to 2048 bytes, and
never replace authorization. Filter or permission changes require a fresh search.
JSON is bounded to 32 KiB; service reads have a 20-second budget. PostgreSQL uses
SQLC and a read-only repeatable-read transaction; memory uses a bounded collector
and detached projections. Source scan cost still depends on retained history.

History contains original run metadata and outcome/result-availability flags;
it contains no input/result payloads, charge/price fields, allowance counts,
execution diagnostics or private billing evidence. **View run details** opens
the existing [Run details](#customer-run-details) panel, including separately
permitted result/files, billing-month and contextual help actions. Receipt
settlement preserves completed/failed/cancelled outcomes after results expire.
Active work retains existing Operations behavior; stopped details expire.
Unresolved work without retained detail is **Outcome unavailable**, not failed.

This is retained receipt history, not a complete audit journal or a snapshot
across pages. Outcomes and availability are observed on every read. A changing
outcome may enter/leave a filtered result; search again from the first page for
current matches. No total count or new retention promise is added. Current
membership in the currently mapped tenant is the authority; receipts do not
invent historical person/Auth-app provenance.

Search adds no polling or browser persistence. Editing filters clears old rows,
selected details/results and continuation, and fences pending responses and old
buttons. Workspace switching, sign-out, access denial and role changes clear
history. Browsing starts/retries no work, changes no plan, acknowledges no inbox
item and modifies no email consent. The endpoint and `portal-history.js` remain
private, no-store and behind the default-off Product Control flag. No credentials,
external email, deployment or real workload is needed for local verification.
