# Team access to applications

Applications created through `POST /v1/orgs/{slug}/apps` retain their
organization. Members use the existing application URLs and SDK methods;
the application identifies its organization. An `X-Active-Org` header or `org`
query parameter cannot change that ownership.

| Operation | Owner | Admin | Developer | Viewer | Billing |
|---|---|---|---|---|---|
| App/configuration metadata and deployment/build reads | yes | yes | yes | yes | no |
| Live and archived logs | yes | yes | yes | yes | no |
| Configuration, deployments, park/wake | yes | yes | yes | no | no |
| Secret writes/rotation | yes | yes | yes | no | no |
| Security policy and trusted publishers | yes | yes | no | no | no |
| Delete/restore an app | yes | yes | no | no | no |
| Alert rules, log drains, webhooks and outbound bindings: read | yes | yes | yes | yes | no |
| Alert rules, log drains and webhooks: create, change, delete; unbind an outbound integration | yes | yes | no | no | no |

Secret reads return metadata, never values. Billing members can use the safe
organization app inventory and existing billing surfaces.

A company app's alert rules, log drains and webhooks send company data out of
Gregale, so owners and admins govern them, whoever created the app; each one
records the member who created it. The app creator's own account-wide alert
rules and notification channels stay the creator's: other members neither
see them through the app nor bind them to its rules. Binding an outbound
integration stays with the app's creator, whose provider credential it uses.
See ADR-20261026151103.

Use an organization-bound API key when working on a teammate's app. Its scope
must also permit the operation: a read key cannot deploy, even for an admin.
Interactive sessions use the user's current membership. The app's original
creator also obeys their current role. Removed members using a personal session
receive 404 on subsequent resource requests; their revoked organization keys
receive 401. Insufficient roles receive 403. A legacy unbound account key
attempting to access a teammate's app receives 409 `org_api_key_requires_org`.

The first shared surface covers app detail/configuration, env and secret
management, security/publishers, image or multipart deployment creation,
deployment list/latest/summary/cancellation/retry, deployment/build evidence,
and live/archive logs. The exact reviewed routes are in
[`routes.go`](../pkg/apid/appaccess/routes.go). Additional operations require a
permission review before teammates can use them. Account-wide inventories remain
creator-scoped and filter apps the caller can no longer read; select the
organization's app inventory for team discovery.

Application admission uses the organization's existing plan and one shared
budget across all creators. Production, developer and PR-preview apps have
separate pools. Deleted apps release slots; restoring an app must fit the same
pool as creation. Company apps do not consume their creator's personal app slots.
An organization plan downgrade blocks further admission when the pool is full;
it does not delete existing applications.

Core app resources keep stable storage UUIDs and sealed bytes. After organization
billing adoption, the organization supplies runtime entitlements, abuse controls,
request budgets and future usage billing. Human verification, audit attribution
and credential revocation continue to use the actual caller. Organizations do
not become login accounts.

Free organizations own resources immediately. Existing paid organizations adopt
ownership when their own provider-qualified subscription is confirmed; a customer
ID or cached subscription handle alone does not activate it. Until then, existing
billing remains in place and creator erasure returns 409. Personal provider IDs
and historical usage are not reassigned. Adoption remains permanent after a
subscription ends.

After adoption, removing and erasing a creator preserves core company apps,
deployments, builds, configuration, sealed secrets and runtime identities.
Personal resources are erased. Adopted company projects and their core
environment/release records also survive (ADR-202610171905 below). Data services, GitHub
installation bindings and other unconverted references still block erasure atomically. A sole
company owner must transfer ownership or delete the company organization, even
when there are no other members.
Personal exports exclude company application resources after departure.
See [ADR-202610171904](adr/202610171904-organization-resource-lifecycle.md) for the exact boundary.

Organization financial APIs are scoped by URL:

- `GET /v1/orgs/{slug}/billing/status` and `GET /v1/orgs/{slug}/usage` require organization view permission.
- `GET /v1/orgs/{slug}/invoices` requires billing permission.
- `GET /v1/orgs/{slug}/billing/portal` and `PATCH /v1/orgs/{slug}/plan` require the owner, admin credential scope and the existing verification/step-up posture.

The Go client exposes `GetOrgBillingStatus`, `GetOrgUsage`, `ListOrgInvoices`,
`GetOrgBillingPortal` and `ChangeOrgPlan`. The OpenAPI contract and generated Node
and Python clients include these operations. Paid plan changes remain pending
until the provider confirms them.

## Organization projects and environments

Use `/v1/orgs/{organization}/projects` for company projects. Ownership comes from
`projects.org_id`; a creator's storage UUID, active-org header or query parameter
cannot select another company. Project summaries expose `org_id`. Existing IDs,
sealed configuration and storage paths remain stable after creator erasure.

| Project operation | Owner | Admin | Developer | Viewer | Billing |
|---|---|---|---|---|---|
| Project, environment, configuration and release reads | yes | yes | yes | yes | no |
| Source upload, deployment, promotion and rollback | yes | yes | yes | no | no |
| Empty environment creation and non-secret configuration | yes | yes | yes | no | no |
| Source settings, environment protection and security policies | yes | yes | no | no | no |
| Approving a protected environment plan | yes | yes | no | no | no |
| Project/environment deletion | yes | yes | no | no | no |

API-key scopes and human MFA/verification still apply. App-scoped deploy tokens
cannot administer an entire project. A company key cannot access personal
projects or another company's project, even when its human belongs to both.
The API rechecks current membership before replaying responses; promotion and
rollback operation keys are partitioned by actor and credential.

For a new company project, upload the existing multipart body (`source`,
`project_slug`, and deployment options) to `POST /v1/orgs/{organization}/projects`;
preview it at `POST /v1/orgs/{organization}/projects/scan`. An adopted company's
new project uses the company's resource namespace. Existing paid organizations
awaiting billing adoption retain the compatibility namespace and erasure guard.

For an existing project, use `POST /v1/orgs/{organization}/projects/{slug}/scan`
and `/deploy`. These URLs also work for migrated projects that retain the old
creator UUID as their storage namespace. The uploaded `project_slug` must match
the path. Personal collection uploads remain personal and cannot deploy a company
project through `/v1/projects`.
After billing adoption, deployment-rate admission shares the company's budget
across these resource namespaces. Existing personal deployment windows stay with
the individual account.

The remaining reviewed project/environment paths mirror the existing APIs below
`/v1/orgs/{organization}/projects/{slug}`: environment inventories and creation,
configuration/diffs, release sets, qualifications, promotion previews, approvals,
promotions, rollbacks, source settings and deletion. The exact permission list is
[`projectaccess/routes.go`](../pkg/apid/projectaccess/routes.go). Legacy project
URLs and dashboard lists also enforce departure and role changes.

GitHub/GitOps administration, async-route manifest bindings, resource/environment
cloning, browser approval exchange and other unreviewed integrations remain
outside the reviewed project surface. Shared uploads reject async-route bindings
before resolving personal services. Managed PostgreSQL bindings use the company
ownership rules below. Other data services and GitHub/GitOps references still
prevent creator erasure.

Go clients provide organization project methods such as `GetOrgProject`,
`CreateOrgProjectEnvironment`, `DeployOrgProject`, `PromoteOrgProjectEnvironment`
and `RollbackOrgProjectEnvironmentPromotion`. Generated Node/Python project
clients include the organization routes. Personal clients retain their URLs.
See [ADR-202610171905](adr/202610171905-organization-project-ownership.md) for migration and
acceptance requirements.

## Managed PostgreSQL

Use `/v1/orgs/{organization}/postgres` for company databases. The reviewed paths
cover database inventory, capabilities, creation, reads, deletion, restore,
resize/compute policy and operation status; database bindings support app and
environment scopes, reads, deletion and rotation. Aggregate usage is available
at `GET /v1/orgs/{organization}/postgres/usage`.

| Operation | Owner | Admin | Developer | Viewer | Billing |
|---|---|---|---|---|---|
| Database and binding metadata | yes | yes | yes | yes | no |
| Create, restore, configure or delete databases | yes | yes | no | no | no |
| Attach/detach app and environment bindings | yes | yes | yes | no | no |
| Rotate database binding credentials | yes | yes | no | no | no |
| Aggregate PostgreSQL usage | yes | yes | yes | yes | yes |

The app and database must have the same persisted organization owner. A
company database can bind to an older company app whose storage namespace still
contains the creator UUID. The API returns metadata; it delivers credentials
through the existing sealed app-secret pipeline. Existing database, binding,
provider and credential identities survive removal and erasure of the creator.
Shared project manifests resolve database names/IDs only within that company.
API keys that attach these bindings through a source manifest need
`postgres:manage` or `admin` in addition to deployment permission.
Delete live managed databases before deleting their organization.

New organization database collections require completed billing adoption.
The organization's plan, database quota and usage totals then apply across all
its storage namespaces. Company cost summaries include retained database usage
history; the underlying ledger subjects and invoices keep their identities.
Personal API collections, dashboard overviews, usage
and exports exclude adopted company databases. Historical audit references to
company databases and bindings are also excluded from personal exports.

Current membership and credential scope are checked before idempotency replay.
Database intent transactions serialize against offboarding, including writes
through the independent PostgreSQL catalogue. Existing cutover and full clone
operations remain outside the shared HTTP permission list.

Go clients expose methods including `ListOrgManagedPostgresDatabases`,
`CreateOrgManagedPostgresBinding`, `RestoreOrgManagedPostgresDatabase` and
`GetOrgManagedPostgresUsage`. Generated Node/Python clients expose the same
organization routes. See [ADR-202610171906](adr/202610171906-organization-managed-postgres-ownership.md)
for backfill, rollout and acceptance requirements.

## Removing a member

`DELETE /v1/orgs/{slug}/members/{user_id}` removes the member and revokes their
organization API keys (including rotation grace keys), deploy tokens for the
organization's apps, and outstanding invitations to their email in the org, in
one transaction. Personal sessions/credentials and other organizations' keys
remain valid. Company applications and already accepted workloads keep running.

The same transaction stops what the member set up on the company's apps
(ADR-20261026151103): their webhook alert rules, log drains and app webhooks
are paused; their alert rules that roll back, demote or promote keep doing so
without their webhook; and their notification channels are unbound from the
company apps' rules. An integration that predates authorship counts as the app
creator's. Drains an application standard manages are left alone. The removal's
activity entry says how many were paused, and an owner or admin re-points and
re-enables them. Their account-scope release webhooks stop receiving the
company's deployment, rollout and GitOps events.

An in-flight shared-app or organization mutation must still hold its original
membership revision when its database transaction begins. A change that already
holds that lock finishes before removal; if removal commits first, the stale
mutation returns 403. Earlier commits in a multi-transaction request remain valid.

Active live/archive logs and app debugger/task connections recheck access once
per second. Removal, role changes, credential revocation/expiry, or a failed
authorization lookup cancel the connection and its upstream work. Closure is
eventual through these checks; database writes use transaction locks rather than
the polling interval. Reinstating a member does not revive a previous revision
or revoked org key.

The filtered app, deployment, build, domain, cron, instance, secret, invocation
and app-metrics inventories hide company resources after departure. Pagination
keeps older personal resources reachable and does not expose hidden company
resource IDs or secret names in its cursors. Domain, cron, edge-rule,
invocation, delayed-task, workflow-run, release-freeze, managed operation,
commit-source, preview-status and deleted-app ID URLs also enforce current
membership, while their legacy operations still require the creator to be
owner/admin. So do routes that name a company app in the request body or query
string, such as creating a cron, custom domain, release freeze, CORS preset,
dev bridge or managed PostgreSQL cutover. An open dev bridge keeps enforcing
it: the relay rechecks its creator's organization access on every request and
on open connections, so leaving the company ends the bridge at once. Trigger
URLs enforce membership too;
legacy `/v1/triggers` administration covers personal apps only, as described
in [organization queues](organization-queues.md).

Account-owned jobs, object storage, queues, GitHub/GitOps bindings and unreviewed
project integrations still need separate organization coverage. Offboarding
here covers the reviewed company control plane; it does not change end-user
authentication inside a deployed application.

See [ADR-202610171901](adr/202610171901-organization-application-authorization.md) for the
migration boundary, and [ADR-202610171902](adr/202610171902-organization-offboarding-fences.md)
for offboarding and its acceptance contract.

## Object storage

Company buckets stay attached to their existing app and environment. Use
`/v1/orgs/{organization}/apps/{slug}/buckets` for inventory, creation and deletion;
its bucket paths provide object listing, API-key grants, user S3 keys and compute
bindings. The existing app URLs enforce the same company permissions. Aggregate
usage is at `GET /v1/orgs/{organization}/object-storage-usage`.

| Storage operation | Owner | Admin | Developer | Viewer | Billing |
|---|---|---|---|---|---|
| Bucket, object listing and binding metadata | yes | yes | yes | yes | no |
| Create/delete buckets and manage user S3 keys/API-key grants | yes | yes | no | no | no |
| Attach/detach application bindings | yes | yes | yes | no | no |
| Rotate application binding credentials | yes | yes | no | no | no |
| Aggregate storage usage | yes | yes | yes | yes | yes |

Credential scopes and MFA still apply. A storage read key needs a bucket read
grant to list objects. Grants target current members' keys in the same company;
company keys cannot reach personal storage. Company storage mutations require
billing adoption. Unreviewed shared settings, signed-URL control APIs, upload-route
administration, source-manifest creation and environment cloning remain closed.
Existing app credentials and upload routes keep their durable application authority.

Removing a member or changing their role revokes their human S3 keys. A request
that authenticated earlier must pass the membership fence before committing a
storage intent. Rejoining does not revive revoked keys. Application bindings
continue working after the creator leaves or is erased: bucket and provider IDs,
object paths, versions, sealed secrets and binding IDs stay the same. A remaining
owner/admin can rotate the binding. Company storage is excluded from personal
cleanup and exports. Delete company buckets before deleting the organization.

After billing adoption, capacity, request, egress and authorization budgets
aggregate the company's buckets across their original storage namespaces. Current
usage heads are invalidated at adoption and must be replaced with freshly allocated
provider evidence. Reports observed before that cutover cannot restore those heads.
Historical reports, finalized invoices and delivery receipts retain their original
financial subject. A finalized current-month charge must be reconciled before
adoption. Provider exports allocate older months to the original namespace and
the cutover month onward to the company.

Go clients expose methods including `ListOrgObjectBuckets`,
`CreateOrgObjectStorageComputeBinding`, `CreateOrgObjectS3Credential`,
`RotateOrgObjectStorageComputeBinding` and `GetOrgObjectStorageUsage`. Generated
Node and Python storage clients cover the same reviewed routes. See
[ADR-202610171907](adr/202610171907-organization-object-storage-ownership.md) for the migration
and rollout requirements.
