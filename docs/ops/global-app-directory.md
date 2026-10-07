# Global app directory and verified regional bindings

ADR-602 introduces a gated directory identity API. A global app has one UUID
and `all_selected_regions` as its serving policy. This slice attaches existing
regional apps and resolves their identities. It does not provision deployments
or publish live ingress routes. ADR-656 adds an opt-in signed route consumer;
the directory-owned live publisher is still pending. Regional provisioning, shared runtime admission, global
runtime quota allocation, ingress health, data/secret replication, and independent native KVM
recovery acceptance remain required for the complete multi-region feature.

## Organization directory identities

An organization-bound API key selects its own workspace for global app
registration, listing and lookup. Browser sessions can select one with
`X-Active-Org: ORG_SLUG` or `?org=ORG_SLUG`. A key cannot switch to another
organization. Current members may read; owners, admins and developers may
register identities. Inactive or pending-deletion organizations are denied.

Responses include an `owner` object with a stable root UUID and either
`account_id` or `org_id`. Personal roots retain the account UUID. Shared roots
have independent UUIDs and survive the creator leaving or deleting their
account. Registration uses the organization's plan app-count limit, with live
membership and plan checks during allocation. This limit counts directory
identities; it does not allocate runtime capacity or change billing.

Shared organizations can attach existing regional organization apps with current
owner/admin consent (ADR-611). Image releases remain unavailable until global
runtime quotas and regional organization billing admission are integrated.
PostgreSQL also refuses shared image release allocation. Personal regional operation remains
available. Rollback of ADR-609 refuses retained shared directory roots.

## Configure the directory and regional cells

Provision a separate directory PostgreSQL database and `apid` endpoint using
the normal account authentication and operator session configuration. Keep the
directory database **unbound**: do not initialize `control_plane_cells` there.
Enable `global_app_directory_enabled = true` in its `apid.toml`, or set
`daemons.apid.global_app_directory_enabled: true` in its fleet manifest.
The default is false. Startup refuses a bound regional database. SQL guards
reject directory writes in regional databases and prevent a populated directory
database from becoming a regional cell. Unbound identity does not by itself
prove physical host independence; the operator provisions the separate database.

Each regional fleet uses ADR-601's immutable cell UUID and region. Follow
[regional cell initialization](regional-control-plane-cells.md) before exposing
the proof endpoint. Regional APIs keep their independent credentials and quotas.

Authenticate the operator CLI to the **directory API** with a fresh MFA step-up
session. Register each cell's exact UUID/region and HTTPS API origin:

```sh
gregalectl cells set --cell-id CELL_UUID --region eu-fsn \
  --api-base https://api.fsn.example.com --enabled=true
gregalectl cells set --cell-id OTHER_CELL_UUID --region eu-hel \
  --api-base https://api.hel.example.com --enabled=true
gregalectl cells list
```

The API origin has no path, trailing slash, credentials, query or fragment.
Cell UUID and region are immutable. An endpoint or enabled-policy change
increments the registry generation; an unchanged retry does not. Disabling
a registry entry blocks new attachments and resolution, without changing or
deleting regional workloads. Re-enabling requires ownership verification again.

## Register and attach a customer's app

Authenticate `gregale` to the directory API with the directory account's key.
Create the global identity, then prove ownership of each existing regional app
using its separate personal account-admin key:

```sh
gregale global-app create global-shop
gregale global-app attach global-shop --region eu-fsn \
  --regional-app shop-fsn --regional-token-env GREGALE_FSN_ACCOUNT_KEY
gregale global-app attach global-shop --region eu-hel \
  --regional-app shop-hel --regional-token-env GREGALE_HEL_ACCOUNT_KEY
gregale global-app show global-shop
gregale global-app resolve global-shop --region eu-fsn
```

Populate the named environment variables through your credential workflow;
do not put keys in command-line arguments. The global key authenticates only
the directory request. The regional key is sent only to the registered HTTPS
origin for `GET /v1/cell/apps/{slug}/binding`; redirects are refused. The Go, Node and Python SDK
attachment transports also refuse directory redirects. Neither the directory
nor its audit events retain the key. Proof responses are bounded
to 4 KiB with a maximum 10-second request timeout. Request bodies are bounded
to 4 KiB. Canonical slugs are 3–40 bytes; origins are at most 2048 bytes.
Bounds live in `pkg/api/limits.go`; directory registrations use the account
plan's existing `DeployedApps` count and create no runtime resource entitlement.

Legacy personal apps and apps owned by the caller's active personal organization
can be attached. Shared organization apps require a regional organization-bound admin key from a
current owner or administrator. They return an additional organization UUID;
app account UUIDs remain creator metadata. Read-only keys, revoked keys and
ordinary scoped deploy tokens cannot establish ownership.
Matching email addresses or app names never establish cross-cell identity.
The first successful attachment fixes the directory tenant's regional account or
organization mapping. Other directory roots cannot claim the same regional
tenant in that cell. Shared mappings use the organization UUID, allowing apps
with different creators and surviving creator departure. Both directory and
regional sides require current owner/admin consent for shared attachment. A cell's
regional app UUID can be claimed by only one global app at a time.

Resolution returns an API origin plus regional app UUID and slug for an
**explicit region**. Authenticate calls to that API with regional credentials.
No global key is forwarded, and the resolver never selects an implicit primary.
All selected, healthy and eligible regions will serve when global ingress is
implemented. Current bindings report identity verification only.

## Runtime quota reservations

See [durable quota delivery](#durable-quota-delivery) for restart recovery after
reserving shares and setting up original regional runtime consent.

Reserve a positive concurrency share for every selected, verified region using
`PUT /v1/global-apps/{slug}/runtime-quotas/{allocation_id}`. The allocation UUID
and shares are immutable; keep the UUID across retries. The current account or
organization plan supplies the total steady and peak concurrency budget. The
rollout grant belongs to one selected region, defaulting to the first region in
sorted order. All outstanding allocations, including earlier generations, count
against both budgets. `global_runtime_quota_limit` (409) includes `limit`,
`observed` and a documentation URL.

```sh
gregale global-app reserve-quota shop --allocation <uuid> \
  --share eu-fsn=2 --share eu-hel=3 --rollout-region eu-fsn
gregale global-app show-quota shop --allocation <uuid>
gregale global-app retire-quota shop --allocation <uuid>
```

Reads expose the immutable regional snapshot and current accounting state.
`reserved` records a reservation; it does not establish scheduler admission or
runtime readiness. Retirement releases unissued shares immediately. An issued
share remains charged while `retiring`, until the internal verifier confirms a
durable regional admission fence and zero counted instances for the original
identity. `retired` means every share has been released. The tombstone remains
readable and a matching reservation retry never revives it. Detach, registry
changes, expiry and regional outages do not release issued capacity. Customers
cannot submit a drain acknowledgement. Shared reservation and retirement require
current owner/admin consent. Regional admission and organization billing remain
pending, and the shared image release gate remains in place.

## Runtime delivery consent

ADR-646 retains purpose-specific personal consent for quota delivery. Configure
the directory's runtime quota signing identity and the regional receiver's trusted
directory keys and canonical HTTPS origin first. Consent does not enable regional
runtime admission; the heads remain frozen pending the remaining native gates.

Use an explicit new UUID for first setup or renewal. Reuse that UUID and the exact
expiry for every retry, including after a lost response:

```sh
gregale global-app setup-runtime-consent shop --consent CONSENT_UUID \
  --region eu-fsn --expires-at EXPIRY_RFC3339 \
  --regional-token-env GREGALE_FSN_ACCOUNT_KEY
gregale global-app show-runtime-consent shop --consent CONSENT_UUID
gregale global-app revoke-runtime-consent shop --consent CONSENT_UUID \
  --regional-token-env GREGALE_FSN_ACCOUNT_KEY
```

Choose a future expiry within six days, with at most microsecond precision. The
directory stores the public consent scope and pending intent before requesting
regional approval. It never retains the admin key. The original UUID, scope and
expiry cannot be replaced. Setup rechecks current owner eligibility and the exact
selected target. There are at most 32 pending, active or revoking unexpired records
per global app. Inspection can report `pending`, `active`, `revoking`, `revoked`
or `expired`; an active record still requires current owner and target checks
before dispatch.

Revocation blocks directory dispatch before contacting the original regional
endpoint. Its original admin key is required even after detachment. Lost replies
remain retryable. Expiry or revocation never releases issued global quota, and
signed retirement remains independent of consent.

## Durable quota delivery

ADR-647 adds a retained delivery journal and an optional directory apid worker.
Enable it only on the unbound directory with its existing configured runtime quota
signing identity and trust bundle:

```toml
[runtime_quota_transport]
directory_issuer_id = "DIRECTORY_UUID"
private_key_path = "/etc/faas/runtime-quota/private-key"
trust_bundle_path = "/etc/faas/runtime-quota/trust.json"
delivery_worker = true
```

Regional receivers reject `delivery_worker`; their runtime heads remain frozen.
Keep old directory public verification anchors during signing-key rotation so
outstanding jobs and original regional receipts can still be verified.

After reserving shares and obtaining active runtime consent for each region,
queue a grant with a stable new delivery UUID per allocation and region:

```sh
gregale global-app queue-quota-delivery shop --allocation ALLOCATION_UUID \
  --delivery DELIVERY_UUID --region eu-fsn --runtime-consent CONSENT_UUID
gregale global-app show-quota-delivery shop --allocation ALLOCATION_UUID \
  --delivery DELIVERY_UUID
gregale global-app retry-quota-delivery shop --allocation ALLOCATION_UUID \
  --delivery DELIVERY_UUID
```

Issuance and signed-job storage commit together before transport. Repeat the
same queue UUID and input after a lost response; queueing never changes its
envelope or consent. The worker carries no regional admin key. It checks current
owner, plan, selected binding and original consent before grant dispatch. Pending
work retries with a 30-second to five-minute backoff; blocked work needs explicit
retry after resolving its reported `owner`, `binding`, `consent`, `retired` or
`trust` cause. A leased job cannot be manually retried until its worker finishes
or expiry recovery reclaims it. At most two jobs, one per owner, are claimed per
batch with fresh two-minute leases. At most 1024 jobs per app may be pending or
leased, including explicit retries.

`accepted` means the exact signed regional receipt was verified and stored.
It does not enable execution or release capacity. Cancellation leaves the lease
recoverable. Expired/replaced workers cannot finish, and lost replies retry with
the same original body. Inspection contains public scope and status only.

For an already retired allocation, queue a separate delivery UUID with
`--purpose retirement --region REGION`, omitting `--runtime-consent`. Signed
retirement uses the original endpoint and remains independent of consent and
current target/owner eligibility. Its receipt still does not prove native drain.
Existing runtime quota retention continues to protect app collection.

A quota target issued before durable queueing cannot be adopted by this journal.
An old job cannot switch to renewed consent. In either case retire the old
allocation, obtain independent verified drain, then reserve a new allocation and
queue using its explicit consent. Registry changes, detachment, consent expiry,
and delivery failure never release outstanding issued quota.

## Invalidation and recovery

`global_app_target_reverification_required` (503) means a registry entry is
disabled or its generation changed. Enable the correct endpoint and repeat the
same attachment with a current regional key. Registry changes during a proof
are rejected atomically; no partial account mapping or target is committed.
Repeating an attachment always performs a fresh ownership probe, even with a
reused `Idempotency-Key` header. The response is never replay-cached.

`global_app_target_verification_failed` (502) covers unreachable cells, rejected
credentials, mismatched cell identity and invalid proof responses. Diagnostics
omit remote bodies and transport errors because they could echo the key.
`global_app_directory_unavailable` (503) indicates the gate is disabled or a
regional proof endpoint lacks a configured cell identity. Ownership failures
hide foreign global apps with 404. Identity replacement returns 409.

`gregale global-app detach global-shop --region eu-fsn` removes directory
metadata only. It does not delete the regional app or the immutable account
mapping. Explicit detachment releases the app UUID claim; a subsequent
attachment must prove ownership again. Directory migrations refuse downgrade
while registrations or registry entries remain populated.

The portable acceptance tests exercise two independently authenticated TLS
regional APIs and a separate directory API. Private PostgreSQL tests verify
quota races, ownership uniqueness, generation fencing in both lock orders,
SQL identity/scope guards and startup refusal. They do not qualify native VM
recovery or end-to-end ingress failover.

## Deployment intent for a resolved target

ADR-603 adds `POST /v1/cells/{cell_id}/apps/{app_id}/deployments` to regional
apid. Use the immutable `cell_id` and `regional_app_id` from resolution, plus
a personal-account API key issued by that cell with admin or deploy-write scope.
The endpoint accepts the existing digest-pinned image JSON and source multipart
forms. It applies the normal MFA, verified-email, image and plan checks. Sessions,
shared organization keys, app-scoped keys and platform-tenant principals are not
accepted by this initial personal-account path.

The cell/app pair is checked before idempotent replay. Reusing a key on another
app UUID creates a distinct operation. A deleted identity returns 404 even when
an earlier success was cached, and reuse of its slug cannot select a new app.
Renaming an app preserves its UUID. An unconfigured cell returns 503, a foreign
cell/app or owner returns 404, and malformed/noncanonical UUIDs return 400.

Use a stable retry key for each regional target's release:

```go
regional := api.NewClient(resolved.APIBase, regionalKey)
ctx := api.ContextWithIdempotencyKey(ctx, releaseKey)
deployment, err := regional.DeployCellApp(ctx, resolved.CellID, resolved.RegionalAppID, request)
```

Go source uploads use `DeployCellAppMultipart`. Node image deployments use
`DeploymentsService.deployCellApp`; source uploads use `deployCellAppMultipart`
with a `CellDeploymentSourceForm`. Python uses `deploy_cell_app.sync_detailed`
or `asyncio_detailed`, with `CreateDeploymentRequest` or
`DeployCellAppFilesBody`. All three transports refuse redirects for this route;
Python and Node source transports let the HTTP encoder supply the multipart
boundary.

An accepted response is regional deployment intent. Wait for that regional
pipeline's readiness before publication. Global quota reservations, durable
multi-target coordination and ingress publication remain pending. The global
routing policy remains `all_selected_regions`; this endpoint alone does not
publish a global route.

## Durable image release acceptance

Use ADR-604's receipt endpoint for coordinator submissions of a prebuilt image:

```go
receipt, err := regional.PutCellImageRelease(ctx, resolved.CellID, resolved.RegionalAppID,
    releaseUUID, api.PutCellImageReleaseRequest{Image: pinnedImage})
```

Choose the release UUID once and retain it across every retry. The endpoint uses
regional app configuration and accepts only a digest-pinned image; it does not
copy source, settings or secrets. It applies the same regional authorization,
image validation and admission pipeline as ordinary image deployment. PUT needs
admin or deploy-write scope; GET also permits apps-read. Personal regional
account keys, verified email and required MFA apply to both methods.

A matching PUT returns the original deployment UUID, including after an uncertain
response or a newer deployment. It bypasses the ordinary 24-hour replay cache.
A different image returns `regional_image_release_conflict` (409). Use a new
release UUID for a changed image, configuration or retry of a failed deployment.
GET at the same URL returns the receipt and current regional deployment status.
A collected deployment has a null `deployment_id` and `expired` status; matching
PUT returns `regional_image_release_expired` (410) and never creates another row.
Deleted apps and previous owners' receipts are hidden. App renames preserve the
original UUID; reusing the slug cannot redirect release intent.

Go clients use `PutCellImageRelease` and `GetCellImageRelease`, Node clients use
`DeploymentsService.putCellImageRelease` and `getCellImageRelease`, and Python
clients use `put_cell_image_release` and `get_cell_image_release` in the deployments
package. All refuse redirects, including custom HTTP clients. The region commits
its receipt, deployment intent and activity event atomically. Receipts are retained
until hard app deletion; database guards refuse destructive downgrade while any
receipt remains. Keep them in regional database backups.

This is the recovery primitive for the upcoming multi-target coordinator. It
proves regional acceptance; health checks, quota allocations and ingress
publication must establish serving eligibility separately. The default global
policy remains `all_selected_regions`.

## Ordered global image intent

ADR-605 adds an optional generation fence to the regional receipt PUT:

```go
request := api.PutCellImageReleaseRequest{
    Image: pinnedImage,
    Ordering: &api.CellImageReleaseOrdering{GlobalAppID: globalAppUUID, Generation: generation},
}
receipt, err := regional.PutCellImageRelease(ctx, cellUUID, regionalAppUUID, releaseUUID, request)
```

The coordinator must allocate the generation once under the global app lock and
persist it with the immutable release before dispatching to every selected region.
Do not invent a larger generation when retrying old work. Every global submission
must supply ordering. Matching retries reuse the release UUID, image, global app
UUID and generation. GET and accepted PUT report ordering when present.

The first ordered acceptance binds the regional app to that global app stream and
current regional owner. Only a higher generation can create new ordered intent.
A delayed older request returns `regional_image_release_order_conflict` (409),
creating no deployment or activity. A matching old receipt can still replay after
a newer generation, without replacing it. Changed ordering on the same release
UUID returns `regional_image_release_conflict` (409). Generations are positive
integers through 9007199254740991, preserving exact JSON precision in every SDK.

Keep generation heads and receipts in regional backups. Deployment collection
does not reset the fence; hard app deletion removes it. Destructive downgrade is
refused while ordered receipts remain. Ownership transfer needs an explicit
global stream adoption protocol before ordered intent can resume under another
owner. Local unordered deployment remains a separate regional intent path.

The fence orders regional acceptance. Global coordination, health, quota and
ingress publication still establish which accepted deployment can serve, using
the `all_selected_regions` default policy.

<a id="global-image-releases"></a>

## Global image releases (ADR-606, ADR-608)

Submit `PUT /v1/global-apps/{slug}/image-releases/{release_id}` with a digest-pinned
`image` and optional `regional_api_keys` object indexed by region. The directory
persists every selected verified target and one increasing generation before
network I/O. A missing/stale target set rejects allocation atomically. Use the
same UUID and image after a timeout or directory restart. Supply regional
account-admin keys to establish or renew consent; the directory never stores
these keys. Subsequent releases and receipt recovery reuse an active scoped
delegation. GET reads the durable journal without regional calls.

`pending` is retried by the recovery worker while scoped consent is usable, `accepted` records durable intent in every
snapshotted region, `expired` means a regional receipt survived deployment
collection, and `blocked` means a snapshot no longer matches the selected binding
or registry. A newer release reports older work as `superseded`. Target changes
apply to future releases; retrying never silently changes an existing snapshot.
Requests cap at 32 targets, 16 KiB and 90 seconds with four concurrent regions;
regional calls cap at ten seconds and redirects are refused. These are protocol
bounds, not global capacity grants.

Unattended recovery uses the sealed regional delegation described below.
Global runtime quota, shared-org identity,
health/ingress publication, managed data and secret placement and native KVM
recovery acceptance remain separate completion gates.

Release limit errors include `limit`, `observed`, and `docs_url`. The observed
value comes from the allocation transaction: the selected target count or the
next requested generation. A rejected allocation writes no release or target.

<a id="regional-delegation"></a>

## Regional release delegation (ADR-607, ADR-608, ADR-610)

A caller mints a `fp_release_` token and fixed grant UUID before contacting the
cell. It must seal the token and bound identity before registering its SHA-256
through `PUT /v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}` with a
regional account-admin key. The body contains `global_app_id`, `token_sha256`
and `expires_at`. No token is sent in the body or returned in grant metadata.
Identical PUT retries never renew or revive a grant. Expiry uses UTC microsecond
precision and is bounded to seven days; at most 32 grants per app are active and
unexpired. API limit errors include their bound, observed count and docs URL.

The delegated token can access ordered image receipt PUT/GET for this app and
global stream, this app's ownership proof, its own grant GET, and its own grant
DELETE. It cannot access ordinary deployment or account/app mutation APIs, mint
a grant, or inspect/revoke another grant. DELETE is terminal; a revoked or expired
token cannot authenticate. An account-admin key can inspect and repeat revocation.
The deployment transaction checks the grant again after authentication, with the
app locked, before accepting intent. Ownership changes invalidate old grants.

For a shared regional app, an organization-bound admin-scoped key from a current
owner or administrator can register, inspect and revoke grants. Consent pins
`issuer_account_id` separately from the app's account and organization anchors.
The scoped bearer uses the issuer's account and rechecks current owner/admin
membership, verified email, account activity and organization status. Another
current administrator can revoke consent after the issuer leaves. Issuer account
deletion retains grant identity; revocation remains terminal after role changes.
Header and query organization hints cannot switch a bound key's tenant.

Shared grant management, ownership proof and directory target attachment are
available. Shared image release admission still requires global runtime quota
and regional organization billing integration. Creating a shared grant allocates no capacity and
cannot bypass those deployment gates.

Directory apid seals the scoped token and complete immutable binding with its
fleet age identity before registration. Set `FAAS_FLEET_AGE_IDENTITY_PATH` (or the
existing host identity fallback) when enabling the global directory. Startup
refuses missing sealing identities. Current and previous identities support
rotation; retain an identity until all delegations sealed with it expire.
Directory consent lasts six days and requires a new request-admin key to renew.
A missing or unknown key fails closed and leaves the durable journal intact.

The work queue claims at most two apps for a two-minute lease. Each app has a
90-second deadline, at most four regional release requests in parallel, and a
separate pool of four revocation workers. A durable cursor visits at most 32
retained delegations per app per pass. Failed work retries after 30 seconds;
new release or retirement wakes are preserved when an old claim finishes.

Detach, binding re-verification and global-app/account deletion retain a
revocation outbox. Only DELETE uses the immutable old regional origin. A matching
terminal response or expiry erases ciphertext; a 401 or lost response keeps work
until expiry. Temporary owner/backend lookup failures prevent dispatch and retain
consent. A confirmed inactive owner retires consent. Regional authentication
backend failures return 503. Regional receipt transactions remain the final
revocation and expiry authority for already-dispatched requests.

A directory app retains at most 1024 live, unexpired delegations, including
revocation work. `global_delegation_limit` (409) includes `limit`, `observed`, and
`docs_url`. This history survives owner deletion so unreachable regions cannot
silently discard outstanding revocation.

## Cached global ingress consumer (ADR-656)

This consumer is available for integration and diagnostics. A live APID publisher
with proven regional health, serving eligibility, admission and billing gates is
still required. Do not enable it by treating attached directory bindings as
healthy eligible runtimes. No production routes or admission were activated.

Configure a separate unbound frontend's `gatewayd-internal` with all four fields:

```toml
global_ingress_directory_id = "DIRECTORY_UUID"
global_ingress_apps_domain = "global.example.com"
global_ingress_manifest_path = "/run/faas/global-ingress/routes.json"
global_ingress_trust_bundle_path = "/etc/faas/global-ingress/trust.json"
```

These same flat keys are supported under `daemons.gatewayd_internal` in a fleet
manifest. Empty fields disable the consumer; partial configurations, regional
database scopes and overlap with `apps_domain` are refused. The frontend keeps
the existing public/internal gateway pair and its normal private listeners.

Use persistent directory Ed25519 public anchors in the existing runtime-quota
trust-bundle format. The reader needs no private signing key. The producer signs
`runtimequota.IngressSnapshot` with the distinct `global-ingress` kind. App URLs
are `<global-app-uuid>.global.example.com`; each target names an exact regional
app HTTPS origin as well as its regional binding. Management `api_base` is not
an app origin. Publish the complete canonical signed envelope by atomic rename
to a regular file readable by the gateway, with no group/other write permission
and no symlink/hardlink. Refresh the generation and at most 30-second lease.

Startup requires a verified current snapshot. A failed refresh quarantines routes
immediately; expired projections or zero usable selected targets return 503. All
selected healthy eligible targets participate in round-robin routing. A connection
failure cools down its exact target for five seconds for subsequent requests;
the failed request is not replayed in another region. HTTP application failures
do not by themselves declare the whole regional fleet unhealthy.

A snapshot is a routing observation, not native runtime authorization or capacity
release evidence. Shared global authentication/billing, regional sessions, redirects
and cookie domains, trusted original client-IP forwarding, regional provisioning
and replicated data/secrets remain separate integration work. The current proxy
uses the regional host and credentials and derives forwarding from its direct
connection. Validate those application contracts before advertising transparent
global app URLs. Dedicated native KVM recovery acceptance remains mandatory.
