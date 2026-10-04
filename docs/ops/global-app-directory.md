# Global app directory and verified regional bindings

ADR-476 introduces a gated directory identity API. A global app has one UUID
and `all_selected_regions` as its serving policy. This slice attaches existing
regional apps and resolves their identities. It does not provision deployments
or publish ingress routes. Global deployment orchestration, shared org/auth,
runtime quota allocation, data/secret replication, and independent native KVM
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

Shared organization attachment and image releases remain unavailable until the
regional organization grant and billing protocol is integrated. PostgreSQL
also rejects shared target bindings. Personal regional operation remains
available. Rollback of ADR-483 refuses retained shared directory roots.

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

Each regional fleet uses ADR-475's immutable cell UUID and region. Follow
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
can be attached. Shared organization apps, non-personal org keys, read-only
keys, revoked keys, and scoped deploy tokens cannot establish personal ownership.
Matching email addresses or app names never establish cross-cell identity.
The first successful attachment fixes the directory account's regional account
mapping; other directory accounts cannot claim that regional account. A cell's
regional app UUID can be claimed by only one global app at a time.

Resolution returns an API origin plus regional app UUID and slug for an
**explicit region**. Authenticate calls to that API with regional credentials.
No global key is forwarded, and the resolver never selects an implicit primary.
All selected, healthy and eligible regions will serve when global ingress is
implemented. Current bindings report identity verification only.

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

ADR-477 adds `POST /v1/cells/{cell_id}/apps/{app_id}/deployments` to regional
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

Use ADR-478's receipt endpoint for coordinator submissions of a prebuilt image:

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

ADR-479 adds an optional generation fence to the regional receipt PUT:

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

## Global image releases (ADR-480, ADR-482)

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

## Regional release delegation (ADR-481, ADR-482, ADR-484)

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

Shared grant management is available; shared ownership proof, directory target
attachment and image release admission still require organization identity,
quota and billing integration. Creating a shared grant allocates no capacity and
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
