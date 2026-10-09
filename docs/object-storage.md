# Customer object storage preview

Gregale can manage object buckets on interchangeable managed providers
without operating storage nodes. Compute remains stateless: these are not VM
volumes. The dashboard's Storage page keeps object buckets separate from
snapshot/image-layer usage.
Architecture and launch boundaries: [ADR-151](adr/151-provider-neutral-object-storage.md).
Large-upload protocol: [ADR-158](adr/158-provider-neutral-multipart-uploads.md).

## Enable a qualified backend

1. Apply database migrations through the normal Gregale deployment process.
2. Copy the [S3 example](../deploy/object-storage.example.json) or
   [GCS example](../deploy/object-storage.gcs.example.json) to an operator-owned
   config path, e.g. `/etc/faas/object-storage.json`. Set the real namespace,
   provider placement, region, and exact browser origins. Use a dedicated
   upstream account/project, not one containing unrelated infrastructure buckets.
3. For `s3`, supply the named access/secret environment variables only to
   **apid, gatewayd-public, and s3-gatewayd** through the deployment's secret
   mechanism. For `gcs`, prefer a dedicated storage service account and set
   `gcs_impersonate_service_account: true`. Give each daemon's ADC identity
   `roles/iam.serviceAccountTokenCreator` on that account and give only the
   storage account the required GCS roles. Direct attached-service-account ADC
   remains supported by leaving the setting false. Do not create a downloaded key. Never put
   credentials in JSON, app envs, source control, URLs, or logs. Optional S3
   `session_token_env` supports temporary credentials; restart/rotate before
   their expiration.
4. Set `FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage.json` for apid,
   gatewayd-public, and s3-gatewayd, then restart every replica with identical
   settings. Set
   `public_endpoint` to `https://s3.gregale.dev` and `public_region` to
   `us-east-1`; those customer-facing values are independent of the upstream
   provider endpoint and signing region. Loading the configuration does not
   enable provisioning or the data plane. Missing config disables object
   storage in apid and prevents s3-gatewayd from starting. Invalid config or
   missing credentials fails startup. Provider config and credentials still
   require a restart; the enable flag does not.
5. Run the qualification checks below before admitting customers.

### Deploy the branded gateway

The production Ansible path keeps this optional until a backend is qualified.
Set the following inventory values on the control-plane host:

```yaml
faas_object_storage_gateway_managed: true
faas_object_storage_gateway_enabled: true
faas_object_storage_config_src: /operator/config/object-storage.json
# S3/R2/OVH only; GCS with attached ADC does not need this file.
faas_object_storage_provider_env_src: /operator/secrets/object-storage.env
```

The `s3_gateway_service` role installs the hardened systemd unit, bounded spool
directory, provider registry, optional root-only provider environment file,
the matching apid drop-in, and an isolated Caddy site for `s3.gregale.dev`.
It validates that the public endpoint and signing region remain
`https://s3.gregale.dev` and `us-east-1`, starts the daemon, and requires its
database-aware readiness endpoint on `127.0.0.1:9096` to pass. Prometheus
scrapes `/metrics` only when this role is enabled. Normal releases restart and
health-gate an enabled gateway after switching `/opt/faas/current`.

Before running the role, create a **proxied** Cloudflare record for
`s3.gregale.dev` pointing at the public Caddy edge. The beta profile deliberately
caps `max_single_put_bytes` and `max_part_bytes` at 64 MiB, below Cloudflare's
Free/Pro request-body ceiling. Configure a dedicated cache-bypass rule for this
hostname and disable URL normalization, redirects, response transforms, and
interactive bot challenges on the S3 API path. Treat Cloudflare's write/read
timeouts as part of the beta endpoint contract. A future large-object profile
must use a separate direct-upload origin or a provider-native signed-upload path
instead of silently raising this proxied limit. `max_upload_bytes` is the total
object ceiling; multipart can exceed 64 MiB while every part stays within the
edge ceiling. The examples allow 100 MiB total with 64 MiB requests.

The daemon being healthy does not enable customer storage. Keep the global
`s3_enabled` runtime configuration false until provider qualification passes,
then enable it and run the cleanup-safe branded smoke:

```sh
FAAS_TOKEN=... \
GREGALE_APP_SLUG=storage-smoke \
make object-storage-gateway-smoke
```

When enabled, Prometheus scrapes `s3-gatewayd` on its loopback control listener
(`127.0.0.1:9096`). The daemon exposes readiness, OTLP exporter health, and
bounded request status-class/latency metrics; request labels do not contain
bucket names, object keys, credentials, or raw provider status codes.

The smoke creates a uniquely named bucket and credential, exercises HEAD,
PUT, GET, LIST and DELETE through `s3.gregale.dev`, verifies revocation, then
deletes the credential and bucket. Its exit trap repeats cleanup after a
failure and prints the exact bucket name if provider cleanup still needs
operator attention.

### Publish assets on an app hostname

Create a bucket with `public: true` and a stable `serve_at` path, for example
`{"name":"assets","public":true,"serve_at":"/assets"}`. Once the bucket
is ready, `GET` and `HEAD` requests to
`https://<app>.<apps-domain>/assets/<key>` are served directly by
`gatewayd-public`; a route hit never wakes or proxies to the app. Responses
use `Cache-Control: public, max-age=31536000, immutable` and do not require a
Gregale or S3 signed URL. The mount path is immutable after creation; choose a
new bucket if an app needs a different public path. Public reads still pass
through the configured object-storage accounting policy, and successful bytes
are recorded in the per-bucket request ledger; provider-authoritative egress
reports then appear in `usage/storage`. Set `public: false` (and omit `serve_at`) for the default
private bucket behavior.

### Run the live provider qualification

The repository includes an opt-in qualification test that exercises the
provider-neutral contract against the registry's configured default backend:
bucket creation/deletion, signed single-object PUT/GET with standard metadata
and tags, tag replacement/deletion, delimiter listing, COPY and REPLACE copy
semantics, multipart initiation and recovery, paginated part listing,
completion, and idempotent abort. It performs real upstream writes and
deletes, so use a dedicated provider project/account and a temporary
configuration whose `defaults` points at the backend being qualified.

```sh
FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage-qualification.json \
FAAS_OBJECT_STORAGE_LIVE_TEST=1 \
make object-storage-qualify
```

Set `FAAS_OBJECT_STORAGE_TEST_REGION` when qualifying a non-default configured
region. Credentials remain in the environment variables named by the config;
they are never written to the test command, JSON, logs, or test output. A
successful run is evidence that the selected provider meets Gregale's data
contract, not evidence of billing, residency, lifecycle, or account-isolation
behavior. Those launch gates still require their own provider-specific checks.

New signed URLs also require an explicit `accounting` policy, a complete
inventory baseline, and fresh authoritative provider reports. See below;
loading the registry and enabling the flag alone is no longer sufficient.
The customer capability, bucket catalog `enabled` field, and dashboard remain
disabled until the policy and a `usage_reports_path` for every advertised
default backend are configured. New bucket creation then fails closed with
`object_storage_usage_stale` if that configuration is incomplete; retries and
cleanup of existing buckets remain available. This configuration check does
not certify that reports are fresh: check `GET /v1/account/object-storage-usage`
and complete provider/import qualification before enabling customer traffic.

## Hot enable / disable

The single global runtime-config key `s3_enabled` defaults to **false**, even
when a provider registry is loaded. Through the existing authenticated operator
configuration API, use `PATCH /v1/admin/config/s3_enabled` with
`{"value":true,"reason":"enable qualified storage backend"}`; set `value` to
`false` to disable. Use `expected_version` for optimistic concurrency as with
other runtime settings. Existing operator authorization, audit, and rollback
rules apply. No additional environment enable flag or account allowlist exists.

The existing database notification subscriber propagates changes across API
and S3 gateway replicas, with a five-second repair poll for missed notifications
while the DB is reachable. This is not a synchronous global revocation barrier.
In-flight operations can finish, and already-issued internal provider requests
remain usable only inside the gateway until their short expiration.
Disabling blocks new bucket provisioning, GET/PUT URL issuance, multipart
initiation and part-URL issuance, and pauses background provisioning. Bucket
metadata, object listing, object deletion, empty-bucket deletion, multipart
completion/abort, and expired-upload cleanup remain available under their
existing authorization rules. Background deletion also continues. Keep the
provider config/credentials loaded for cleanup. The bucket-list endpoint reports
`enabled: false` while still returning metadata and configured limits. Enabling
without a loaded registry does not make storage usable. On the branded S3
endpoint, authenticated DeleteObject, DeleteObjects, and AbortMultipartUpload
also bypass new-work budgets and the disable flag. Reads, listing, and multipart
completion on that endpoint remain blocked while disabled.

Rollout: apply the recovery migration, then update every apid replica before
relying on this flag. Older binaries treat a loaded registry as enabled and do
not honor `s3_enabled`; keep customer storage traffic disabled during a mixed-
version rollout. Before rollback, disable signing and the branded endpoint,
abort or finish every live
multipart session, wait out issued URLs, restore `max_upload_bytes` to at most
5 GiB, and verify no capacity grant exceeds 5 GiB. Then stop recovery workers
before rolling the schema back; the down migration deliberately refuses to
discard a larger safety reservation silently.

## Branded S3 endpoint

Gregale-issued S3 credentials are bucket-scoped and use the stable customer
contract below, regardless of whether the bucket is placed on OVH, R2, GCS, or
a future Gregale-owned storage cluster:

- endpoint: `https://s3.gregale.dev`
- signing region: `us-east-1`
- addressing: path-style only (`https://s3.gregale.dev/{bucket}/{key}`)

Path-style is intentional. The available `*.gregale.dev` certificate covers
`s3.gregale.dev`, but it does not cover bucket hosts such as
`assets.s3.gregale.dev`. The beta endpoint is intentionally Cloudflare-proxied
and therefore advertises a 64 MiB single-request/part limit. Caddy terminates
origin TLS and forwards this hostname to s3-gatewayd on
`127.0.0.1:8084`; preserve the original Host header. Do not share the
`api.gregale.dev` reverse-proxy route, request-body limits, or auth middleware.

Create a credential with
`POST /v1/apps/{slug}/buckets/{bucket-id}/s3-credentials` and a body such as
`{"label":"laptop","permission":"read_write"}`. The response contains the
access key ID, secret access key, endpoint, region, and addressing style. The
secret is returned once. List active credentials with `GET` on the same path
and revoke one with `DELETE .../s3-credentials/{credential-id}`. Revocation is
checked from Gregale's database on every new request.

For an AWS CLI profile, store the returned credentials through the CLI's normal
credential mechanism, then set:

```sh
aws configure set profile.gregale.region us-east-1
aws configure set profile.gregale.s3.addressing_style path
aws --profile gregale --endpoint-url https://s3.gregale.dev \
  s3api list-objects-v2 --bucket assets
```

### Bind storage to a compute workload

The App Platform-style CLI path creates or reuses a bucket and injects its
sealed S3 settings in one operation:

```sh
gregale add bucket assets --app my-api --env production
gregale add bucket assets --app my-api --env production --permission read_write
```

The command is idempotent for the app, scope, and bucket name. It prints the
bucket and injected secret names only; access keys and secret values are never
written to stdout, JSON output, or logs. Manage an existing compute binding
from the CLI with:

```sh
gregale bindings object-storage list my-api assets
gregale bindings object-storage rotate my-api assets BINDING_ID --wait
gregale bindings object-storage revoke my-api assets BINDING_ID
```

The bucket argument accepts its name or ID. The list command prints the
binding ID needed by rotate and revoke; output includes rotation status but
omits access-key IDs and sealed secret names. Use the API operations below for
automation that needs direct access to the resource endpoints.

Rotation returns after the new key is issued. Add `--wait` to poll until the
previous key is retired and `rotation_pending` clears. The wait defaults to
five minutes with one-second polling; use `--wait-timeout` and
`--poll-interval` to adjust those limits. If the timeout expires, the command
prints the latest binding status and exits with status 1.

Use `POST /v1/apps/{slug}/buckets/{bucket-id}/compute-bindings` when the
workload should use the branded S3 endpoint without carrying credentials in
deployment manifests. The request accepts the same `permission` values as a
standalone credential and an optional uppercase `prefix`. Gregale creates one
bucket-scoped credential and writes six sealed app secrets under that prefix:
`ENDPOINT`, `REGION`, `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, and
`ADDRESSING_STYLE`. Credential and secret creation commit together, so a
conflicting secret key leaves no active credential or partial binding. The
same commit marks existing app snapshots stale and records a runtime-config
change. The workload receives the values through the existing secret staging
path on its next deploy/wake; values are never returned by the binding API or
stored in plaintext.

List bindings with `GET .../compute-bindings`, rotate in place with
`POST .../compute-bindings/{binding-id}/rotate`, and revoke with
`DELETE .../compute-bindings/{binding-id}`. Rotation keeps the binding ID and
secret names stable. It updates the new key and its two sealed app secrets
atomically. For a live app, the response sets `rotation_pending: true` while
the previous key remains valid through a rolling runtime refresh. Gregale
retires that key after old instances drain; `GET .../compute-bindings` shows
`rotation_pending` until retirement. Retrying the rotate request while
pending requeues the same refresh without creating another key. For an app
with no live deployment or resident instances, rotation retires the previous
key immediately. Revocation commits both-key invalidation, managed-secret
removal, a runtime-config change stamp, and snapshot invalidation together.
The next wake therefore cannot restore a snapshot containing the revoked
binding. Ordinary secret PUT/DELETE calls cannot overwrite or remove a managed
binding secret.

### Declare storage bindings in `gregale.yaml`

For source-based deployments, keep the bucket binding beside the application
code. Deployment resolves an existing ready bucket in the selected app and
environment, then reuses or creates the managed compute binding idempotently:

```yaml
buckets:
  - bucket: assets       # logical bucket name or ID
    scope: production    # defaults to default; --environment may select it
    permission: read_write
    prefix: GREGALE_S3_ASSETS
```

The optional `app` field scopes a declaration to one app; a single-app deploy
ignores entries for other apps. The optional `label` is used only when a
binding is first created. Credentials and provider details stay out of the
manifest; the API seals the six runtime settings into the app environment.
`gregale deploy` never creates a bucket from this declaration. Create one
explicitly first with `gregale add bucket`, then commit the manifest so local
and CI deploys share the same binding intent.

All database and bucket declarations for one deployment must use one scope.
An explicit `--environment staging` selects `staging` for declarations that
omit `scope` and rejects declarations that explicitly name another scope.

Compute remains stateless: this binding supplies S3 SDK configuration, not a
persistent filesystem mount. The app must still have outbound access to
`s3.gregale.dev` under its egress policy.

### Release gates and staging smoke

Run the read-only release preflight before promoting a release. It checks that
the provider registry is loaded and enabled, limits/regions are non-zero, and
the compute-binding routes are present in the deployed binary:

```sh
FAAS_TOKEN=... \
GREGALE_APP_SLUG=disposable-storage-smoke \
GREGALE_API_URL=https://api.gregale.dev \
make object-storage-release-preflight
```

After the preflight passes, run the mutating qualification against the same
disposable app. It creates a uniquely named bucket, exercises direct S3
object I/O, creates and rotates a compute binding, verifies that the managed
secret names remain stable while the access key changes, revokes both
credentials, and deletes the bucket on every exit path:

```sh
FAAS_TOKEN=... \
GREGALE_APP_SLUG=disposable-storage-smoke \
GREGALE_API_URL=https://api.gregale.dev \
make object-storage-gateway-smoke
```

The smoke test requires `aws`, `curl`, and `jq`. It never prints credential
material or signed URLs. Use a disposable app and do not run it against a
customer bucket; the cleanup trap removes the temporary object, bindings,
credential, and bucket even when a check fails.

This endpoint supports ListBuckets for the credential's one bucket,
HeadBucket, GetBucketLocation, ListObjectsV2 with delimiter/common-prefix
listing, `start-after`, URL encoding, ETags, and zero-key pages, GetObject/HeadObject/PutObject/DeleteObject, multi-object
`DeleteObjects` (up to 1,000 keys), and the standard multipart
initiate/list-parts/upload-part/part-copy/complete/abort operations, plus CopyObject with
COPY/REPLACE metadata and tagging directives. S3 backends also support
ListObjectVersions, GET/HEAD with a customer versionId, and selected-version
CopyObject/UploadPartCopy (see below). It validates AWS
Signature V4 in both the `Authorization` header and presigned query form.
Presigned GET, HEAD, PUT, and DELETE capabilities are limited to seven days and
remain subject to credential revocation when a request arrives. Uploads
validate SHA-256, Content-MD5, and the S3 CRC32, CRC32C, CRC64NVME, SHA-1, and
SHA-256 checksum headers before writing upstream. SigV4 `aws-chunked` PUT and
UploadPart bodies support signed payload chunks, signed checksum trailers, and
the unsigned-payload checksum trailer form used by current AWS SDKs. The
gateway verifies every frame, decoded length, trailer signature, and checksum
before allowing a complete object or part to reach the provider. Ordinary PUT
and multipart initiation accept the standard HTTP metadata fields,
`x-amz-meta-*`, and URL-encoded `x-amz-tagging`; multipart sessions persist this
metadata so provider recovery and eventual completion retain it. GET/HEAD
returns customer metadata using the branded `x-amz-meta-*` names. `?tagging`
supports GET, PUT, and DELETE with up to ten tags per object. S3 backends use
native tags; GCS stores the tag set in a reserved provider-private metadata
field (including direct signed and multipart uploads) that is hidden from
customers. It emits
Gregale-owned S3 XML errors and filters provider response headers, URLs, bucket
names, and credentials. By default four uploads per gateway may run concurrently;
`transfer.max_concurrent_uploads` configures this limit from 1 to 64. Single PUTs
reserve their complete decoded size against `transfer.max_spool_bytes` and
`transfer.min_spool_free_bytes` before reading the body;
additional authenticated uploads receive S3 `SlowDown` without consuming more
spool disk. Multipart parts are streamed through the gateway to the selected
provider and are limited by the configured per-part upload ceiling. Use
`s3api put-object` for simple uploads; the high-level `aws s3 cp` command can
automatically select multipart uploads.

Current SDK `x-amz-checksum-mode: ENABLED` reads return provider checksum headers
when available; Gregale does not synthesize checksums for older objects or
providers without that capability. Ordinary PUTs and multipart completion preserve
`If-Match` and `If-None-Match: *` atomically on S3 backends. Conditions are mutually
exclusive; If-Match is limited to 256 bytes and rejects control characters.
GCS conditional single PUTs support create-only `If-None-Match: *` and replacement `If-Match` through signed native generation fences (ADR-843). GCS conditional multipart completion returns 501 explicitly. GCS tracked copies
support source conditions using a captured generation and metageneration.
S3 copies support `x-amz-copy-source-if-match` and
`x-amz-copy-source-if-none-match`, each with one strong ETag or `*`. Copy source,
range and condition headers must be signed. S3 copies also support signed
If-Modified-Since and If-Unmodified-Since predicates when the measured source
has an immutable native version and the bucket has an all-version accounting
baseline. A customer If-Match combined only with If-Unmodified-Since can use
a mutable source because S3 gives that ETag predicate precedence. Other
independently restrictive dates on mutable/null sources return 501. See
[ADR-541](adr/541-immutable-s3-copy-sources-and-date-conditions.md).
Cross-bucket copies use an explicit copy-only source grant on the destination
S3 credential. Both buckets must belong to the same account and be ready on the
same native S3 placement. Source bucket UUIDs remain unambiguous across apps
with equal bucket names. Other placements/providers return an unsupported
response. UUID selectors take precedence over names. If a bucket's name is
itself a canonical UUID, use its bucket ID for a same-bucket copy. Other
same-bucket names keep their current behavior.

```sh
gregale bucket copy-sources grant <destination-app> <destination-bucket-id> <credential-id> <source-bucket-id> 'allowed/'
gregale bucket copy-sources list <destination-app> <destination-bucket-id> <credential-id>
gregale bucket copy-sources revoke <destination-app> <destination-bucket-id> <credential-id> <source-bucket-id>
```

Use `CopySource: "<source-bucket-id>/allowed/object"` in CopyObject or
UploadPartCopy; URL-encode the object key and append an owned public
`?versionId=<source-version-id>` when selecting a version. This source grant
permits copy operations only. The destination credential still cannot GET,
HEAD, list or modify the source. The destination needs write permission;
same-bucket copies continue to require read and write permission.

The control API exposes GET `/s3-credentials/{credential}/copy-sources` and
PUT/DELETE `/s3-credentials/{credential}/copy-sources/{source}` under the owned
destination bucket route. PUT accepts `{ "prefix": "allowed/" }`; an empty
prefix allows all source keys for copy. Grants are limited to 32 source buckets
per credential, with literal prefixes at most 1024 UTF-8 bytes. Creating or
changing a grant requires `storage:manage`, destination write and source read
bucket authority. Listing and revocation require destination write authority;
cleanup remains possible for revoked credentials and disabled S3 ingress.
Go, Node and Python clients expose the corresponding typed methods.

Identical updates preserve the grant. Changing a prefix or revoking and
recreating a grant invalidates prepared copies. Grant and credential authority
are checked atomically with ordinary copy admission and again at dispatch.
Multipart copy checks the captured grant while atomically admitting copied
bytes and claiming the single native part attempt. Already dispatched work
retains completion/recovery authority after revocation. Managed credential
rotation resolves the parent's current grants; signed URL credentials and
rotation stages cannot independently acquire grants.

Copies retain exact source version/ETag and metadata semantics. A versioned
source requires its verified all-version inventory baseline; destination quota
uses the destination's own inventory mode. Ordinary copies capture destination
default encryption at admission, and parts retain session encryption. Source
HEAD requests are metered to the source, and native copies to the destination.
Recovery probes destination completion proof and never repeats an uncertain
copy or source read. On an unversioned destination, loss of completion proof
through overwrite/deletion retains the existing conservative pending contract.

Multipart listing uses standard key/upload
markers and excludes completed history. Unsupported listing options return 501.

Branded CORS preflights accept explicit configured backend origins and SigV4,
metadata, tagging, checksum, and conditional headers. Subsequent requests require
normal SigV4 authentication. Existing provider buckets still need CORS updates
when provider CORS configuration changes.

Every streamed part reserves capacity and a transfer token durably before its
provider request. Retries preserve the largest admitted size for each part;
concurrent writes to the same part return 409. Different parts remain concurrent.
Successful verified transfers settle their token. Failed or uncertain transfers
block finalization for a conservative 36-minute window (30-minute transfer
context, 60-second internal URL, five-minute grace). Expiry alone releases no
capacity. Completion checks the part revision and absence of in-flight writes,
then atomically persists its size, exact part list, and final-object grant.

Conditional completion also persists its immutable condition in a distinct
`completing_conditional` state. Every retry must send the same condition and
parts. Recovery replays that intent without listing parts or admitting capacity
again. Reserved session metadata plus the exact object size can prove success
after a lost response; unavailable proof keeps the intent pending. A definitive
condition failure returns 412 `PreconditionFailed`; a conflicting write returns
409 `ConditionalRequestConflict` and requires a new upload with new parts.
A missing If-Match destination returns 404 `NoSuchKey`. These rejections persist
their outcome and enter verified abort cleanup; identical retries return the same
error even after cleanup. The management API exposes `completion_error_code`.

Abort fences new parts immediately. A 204 response can mean cleanup is accepted
while the upload remains `aborting` and its capacity remains charged. The existing
recovery worker repeats aborts and verifies an empty provider ListParts response
(or NoSuchUpload) after transfer fences settle or expire. Only that verification
releases newly tracked part grants. Cleanup remains available while storage is
disabled or budgets are exhausted. Older untracked reservations and management
API object grants remain conservative.

Pause branded multipart writes and drain all nonterminal branded sessions before
applying either part-ledger or transfer-fencing migrations; both refuse unsafe
upgrades. Deploy every gateway and API replica before reopening writes. Rollback
refuses to discard unsettled transfer tokens. Keep abort and ListParts provider
permissions available. See [ADR-530](adr/530-s3-compatibility-and-multipart-capacity.md)
and [ADR-531](adr/531-s3-multipart-transfer-fencing-and-cleanup.md).
For conditional completion, apply its additive migration first, upgrade every
API replica before gateways, and retain HEAD permissions for lost-response
proof. Older workers cannot replay a conditional intent unconditionally.
Rollback requires conditional completion or cleanup to be terminal. See
[ADR-532](adr/532-conditional-s3-multipart-completion.md).

Conditional GET/HEAD requests forward the standard validators and preserve S3
`304 Not Modified` and `412 Precondition Failed` outcomes without exposing a
provider response body. SigV4A/ECDSA authentication,
ACLs, and bucket create/delete through the S3 protocol remain explicit
`NotImplemented` gaps. Bucket provisioning and deletion use the authenticated
Gregale API so a customer credential cannot escape its assigned logical bucket.
Ordinary deletion and mutable null-version deletion use durable receipts and
marker admission; see the deletion recovery limitations below.

## Lifecycle rules and discovery

S3-capable buckets expose `PutBucketLifecycleConfiguration`,
`GetBucketLifecycleConfiguration` and `DeleteBucketLifecycle` on
`/{bucket}?lifecycle`. Rules are stored in Gregale; provider lifecycle
configuration is not changed. Supported actions are current expiration by days
or UTC midnight date, noncurrent expiration with optional newer-version
retention, expired delete marker cleanup, and prefix-filtered abandoned
multipart cleanup. Abort rules do not extend the existing 24-hour session
expiry. Enabled/disabled rules and prefix/tag conjunctions are
supported. Transitions, object size predicates and other unsupported directives
are rejected. Tag filters cannot select multipart or expired marker actions.

A PUT replaces the complete policy with one to one thousand rules. Missing rule
IDs receive stable generated IDs. XML bodies are limited to 5 MiB, duplicate
fields and unknown fields are rejected, and signed hashes/checksums are checked
before policy mutation. An absent S3 policy returns
`NoSuchLifecycleConfiguration`; DELETE is idempotent and returns 204. S3 reads
require the bucket read permission; replacement and removal require write.

The management API requires storage manage scope and a bucket write grant:

- `GET|PUT|DELETE /v1/apps/{app}/buckets/{bucket-id}/lifecycle`
- `POST /v1/apps/{app}/buckets/{bucket-id}/lifecycle/scans`
- `GET /v1/apps/{app}/buckets/{bucket-id}/lifecycle/scans/{scan-id}`

GET and DELETE return the durable policy, including revision and normalized
rules. An absent management policy has revision zero and empty rules. PUT takes
`{"rules":[...]}`; use DELETE to clear it. Configuration reads/removal and scan
reads remain available while ingress is disabled or provider placement is
unavailable. PUT and starting discovery require enabled ingress. A live scan
lease temporarily blocks replacement/removal with 409; retry after release.

```sh
gregale bucket lifecycle set demo BUCKET_ID lifecycle.json
gregale bucket lifecycle get demo BUCKET_ID
gregale bucket lifecycle scan demo BUCKET_ID
gregale bucket lifecycle status demo BUCKET_ID SCAN_ID
gregale bucket lifecycle clear demo BUCKET_ID
```

`set` also accepts `-` for stdin. The file contains the management request, for
example:

```json
{"rules":[{"id":"temporary","status":"Enabled","filter":{"prefix":"tmp/"},"abort_incomplete_multipart_days":7}]}
```

Go, Node and Python SDKs expose configuration and scan methods. POST creates a
due scan or resumes existing discovery; it does not bypass the hourly schedule.
The worker uses bounded discovery and durable mutation journals. Day-based
eligibility uses conservative UTC day boundaries. Completed scans report
**discovery** completion, not successful cleanup or reclaimed capacity. Use
deletion receipts and multipart session status for admitted work. Removing rules
cancels unclaimed discovery while admitted cleanup continues through restart and
disabled ingress. Legacy reservations and inventory baselines remain conservative;
verified capacity reconciliation reclaims them. See
[ADR-550](adr/550-durable-object-lifecycle.md).

## Reading retained S3 versions

S3-backed buckets expose ListObjectVersions through `GET /{bucket}?versions`.
It supports prefix, one-character delimiter, key-marker, version-id-marker,
encoding-type=url and max-keys=0..1000. Use both next markers to resume a page.
Versions and delete markers carry durable Gregale UUIDs, scoped to the logical
bucket and object key. The special S3 `null` ID remains mutable. Native provider
version IDs are private and are not accepted as customer IDs.

The Gregale control API exposes the same public references with
`GET /v1/apps/{slug}/buckets/{bucket}/objects/versions`. Query parameters are
`prefix`, `delimiter`, `limit` (1–1000), `key_marker` and `version_id_marker`.
The JSON response includes versions, delete markers, common prefixes and paired
continuation markers. Resume using both returned markers. Listing requires a
bucket read grant and request-budget admission.

```sh
gregale bucket versions list <app> <bucket-id> --prefix 'reports/' --limit 100 --json
gregale bucket download <app> <bucket-id> 'reports/report.txt' ./old-report.txt --version-id <public-version-id>
```

Upload JSON includes `version_id` when a version is acknowledged. Download
`--version-id` accepts an owned immutable public UUID; the mutable S3 `null` ID
is excluded. GET/HEAD signed URL requests accept the same `version_id`. Stored
URL authority, query signatures and the gateway all enforce that selector.
Missing, foreign or deleted versions never fall back to the current object. A
CLI download verifies the acknowledged public version before publishing the
complete local file. See [ADR-687](adr/687-object-version-cli-and-bound-downloads.md).

Use those IDs with standard SDK GetObject/HeadObject `VersionId` parameters,
or AWS CLI `s3api get-object --bucket assets --key hello.txt --version-id ID
output.txt`. Read permissions and credential revocation still apply on every
request. Exact-version reads preserve ranges, conditions, metadata and available
checksums. A current delete marker returns 404; requesting that marker by ID
returns 405 with `x-amz-delete-marker: true` and Last-Modified. Successful ordinary
PUT, CopyObject, CompleteMultipartUpload and current GET/HEAD also return customer
x-amz-version-id when the provider supplies a native ID. Multipart completion
returns the actual provider ETag and durably stores its public version ID;
repeating the same completion returns that result even after later overwrites
without another provider request. Version-specific tagging is described below.

Observing a non-null native version or any delete marker activates the retained
version accounting fence. Reads and listings continue under ordinary read
accounting. New writes wait for capacity reconciliation to establish an inventory
of all retained data versions and delete markers. A current-only inventory cannot
refund this history. Listing does not change provider configuration or create
markers. Configuration and immutable version deletion are described below.
See [ADR-542](adr/542-customer-s3-version-identities-and-reads.md).

Copy a retained version with the standard SDK `CopySource` value
`assets/hello.txt?versionId=ID`. URL-encode the key and query value separately;
an encoded `?` in a key is not a version selector. The ID must belong to that
source key and the credential's bucket. Unknown or foreign IDs fail before a
provider request. Copies require both read and write permission, a verified
all-version accounting baseline for retained sources, and copied-byte quota.
The source HEAD and provider copy both select the resolved native version.
Metadata/tagging directives, source predicates and multipart ranges still apply.
`x-amz-copy-source-version-id` reports the customer source ID; ordinary copies
also report the destination's new customer version ID when supplied by the
provider. Native IDs remain private.

Restore an older version by copying it back to the same key. This creates a new
current version when provider versioning is enabled, preserving existing history
and charging the new retained entry. It does not remove a delete marker or refund
quota for an older version. Explicit `null` copies keep `versionId=null` on both
provider requests and retain the measured ETag fence; `null` is mutable and is
not an immutable restore point. Selected delete markers cannot be copied.
Uncertain responses retain write receipts or part transfer fences and do not
automatically replay the copy. See [ADR-543](adr/543-customer-selected-s3-copy-sources.md).

```sh
aws --endpoint-url "$S3_ENDPOINT" s3api copy-object \
  --bucket assets --key hello.txt \
  --copy-source "assets/hello.txt?versionId=$VERSION_ID"
```

## Recovery and operator attention

Each production apid runs a recovery sweep at startup and every 15 seconds after
the preceding sweep completes. Each batch selects at most 20 due operations;
replicas atomically claim the persisted two-minute leases before upstream I/O.
Bucket operations have a 45-second deadline and multipart operations a 90-second
deadline. The worker only retries catalogued bucket/upload intents, never
discovers or deletes an unknown bucket or an upload without a durable Gregale
session.

Failed requests and background attempts persist `attempt_count`, `retry_at`, and
a bounded `last_error_code`. Transient failures back off from 30 seconds to 15
minutes; invalid requests and credential/configuration failures retry once per
hour. Request retries respect the same cooldown (409 while busy/not due).
Cleanup may replace a failed provisioning intent once its active lease ends.
Successful completion resets retry metadata. Nonempty deletion returns the
bucket to `ready` rather than repeatedly trying to delete customer data.

After a process crash, recovery waits for the lease to expire and repeats the
same operation against the same physical bucket. Missing buckets on deletion
are success; successful creation followed by a lost response must be safe to
repeat on the qualified provider. A stale lease owner cannot commit a newer
worker's outcome. Provider qualification remains necessary for external effects.

Inspect `faas_object_storage_recovery_attempts_total{operation,outcome}` for
worker progress (`success`, `not_empty`, `deferred`). Structured logs contain
bucket/backend IDs, operation, attempt, retry interval, and sanitized error code;
`needs_attention=true` marks configuration/invalid failures or five consecutive
attempts. No raw upstream errors, credentials, or signed URLs are recorded.
The durable retry fields are available for operator diagnosis in the bucket
catalog; they are not added to the customer API. A failed sweep emits a warning.

For configuration failures, restore the original backend identity and valid
credentials on all replicas; restart for provider-config changes and wait for
the persisted retry time. Do not bypass placement fencing or mutate lease tokens
to force recovery. The first release does not include a manual force-retry API,
ready-bucket inventory/orphan reconciliation, or automatic data migration.

The identity needs bucket creation/deletion and CORS configuration; object
list/get/put/delete; and multipart list/create/upload-part/complete/abort/head for
Gregale buckets. GCS additionally requires the IAM Service Account Credentials
API plus `iam.serviceAccounts.signBlob` on `gcs_service_account`; grant that
permission to the ADC principal without exporting a private key. Restrict the
identity to `gregale-*` where supported; otherwise isolate the upstream project.
Configure the provider's abort-incomplete-multipart
lifecycle rule as a backup with a window longer than Gregale's 24-hour session
TTL. Enable provider/account public-access blocking where available. The driver
creates buckets without public ACLs, but does not manage provider-specific
account policies, lifecycle rules, encryption keys, residency controls,
replication. Enable versioning through the managed cutover below. Bucket Object
Lock configuration is available through the enrolled API/SDK/CLI capability
described below. The UI does not manage historical versions or retention locks.

ADR-554 adds an internal native S3 encryption foundation. An operator can
declare backend `encryption.algorithms` (`AES256`, `aws:kms`, `aws:kms:dsse`)
and enroll `encryption.keys` with a Gregale key UUID, owning account UUID and
same-region native key ARN. Native key resources must have one unambiguous
Gregale owner across the registry. Enrollment maps to an owned
`arn:gregale:kms:<public-region>:<account>:key/<key-id>` reference; it does not
create a KMS key or manage its policies. Optional `encryption.kms_endpoint`
selects the operator's KMS origin and follows the registry's HTTPS policy.
KMS identity/type validation uses DescribeKey; actual S3 operations enforce
GenerateDataKey/Decrypt permission. Bounds and strict context validation live
in `pkg/api/limits.go`. ADR-556 enables explicit customer encryption on the branded S3 gateway with
durable admission, metering and owned response mapping. Native encrypted URLs
remain private. Bucket defaults and encrypted control API upload URLs remain
in progress.
See [ADR-554](adr/554-owned-key-bindings-and-native-s3-encryption.md) for the
implemented provider contract and completion boundary.

Bucket names in Gregale are logical and app/scope-local. Physical names are
UUID-based to avoid leaking customer identifiers or colliding across providers.
Only configured region defaults appear in the creation catalog.

## Accounting and safety budgets

The `accounting` object in the same provider-registry JSON sets uniform
operator limits. It does not add an enable flag or account allowlist. Missing
or null policy keeps metadata/cleanup usable but blocks new signed URLs.
Policy changes require restarting API replicas with identical config.

In `gateway_safety_v1`, customer native calls reserve the shared request budget
before dispatch. Each multipart part-list page (including CLI resume), bucket
configuration or version-protection probe, and tag request counts separately,
even when the provider returns an error. Exhausted budgets return
`object_storage_budget_reached`; unqualified accounting returns
`object_storage_usage_stale`, before contacting the provider. Multipart session
status/listing remain available without a native request. A denied resume keeps
its checkpoint for retry after accounting recovers. Pending bucket configuration
inspection may return persisted progress when its live probe is denied. S3
object and part listings make one provider attempt; an explicit retry requires
a new reservation. Accepted recovery,
maintenance and cleanup keep recording attempts and can finish at the ceiling.

Copy-source grants have a separate fixed ceiling of 32 source buckets per
destination credential. The API reports `copy_sources_per_credential` with
`limit` and `observed` values when an additional grant exceeds that ceiling.
Removing a grant frees one slot; narrowing or replacing its prefix uses the
existing slot. Each literal UTF-8 prefix is limited to 1024 bytes.

Example safety values **only**, not approved pricing or plan allowances:

```json
"accounting": {
  "max_account_bytes": 10737418240,
  "max_bucket_bytes": 5368709120,
  "max_account_keys": 100000,
  "max_monthly_cost_millicents": 500000,
  "max_monthly_requests": 1000000,
  "max_monthly_egress_bytes": 10737418240,
  "max_monthly_authorizations": 100000,
  "max_report_age_seconds": 7200
}
```

Costs use EUR millicents: 1000 millicents = 1 cent. These are ceilings on
reported upstream cost, not customer invoice rates. Every limit must be
positive; zero is not an unlimited setting. Report freshness must be 60–86400
seconds and the key ceiling at most one million. OVH access-log-backed
reporting requires at least 7200 seconds to allow for the provider's normal
one-hour delivery lag and the five-minute export interval.

An optional `pricing` object can add a provider-neutral customer rate card
without changing the safety policy:

```json
"pricing": {
  "currency": "EUR",
  "storage_millicents_per_gib_month": 15000,
  "requests_millicents_per_million": 500,
  "egress_millicents_per_gib": 90000
}
```

`GET /v1/account/object-storage-usage` then includes `charges` with the
storage, request, egress and total estimate for the current UTC month, plus
`billing_mode` (`off`, `shadow`, or `live`) and the optional UTC-month
`billing_from` boundary. Storage uses a 730-hour month; request and egress
units are rounded up independently to one millicent. Omit `pricing` while
qualifying providers to keep charges disabled. The rate card is intentionally
separate from upstream `cost_millicents`.

Polar billing is independently default-off. In `shadow`, Gregale finalizes the
completed UTC month and records the would-be charge without sending an event.
In `live`, it sends one idempotent event per immutable billing record. The
event quantity is the exact total customer charge in millicents; detailed
storage, request, egress, and upstream-cost values remain in metadata and the
local ledger. A durable provider receipt records pre-activation, shadow, or
live handling; accounts that are not on a paid plan receive a permanent
zero-quantity ineligible receipt. Changing modes, plans, or restarting cannot
retroactively charge an older period. Configure the Polar meter to sum `charge_millicents` at EUR
`0.001` cents per unit, and follow the billing provider switch runbook for the
required environment variables and catalog checks. Polar bills the month-close
event in the provider cycle in which it is received; its metadata preserves the
UTC usage month rather than implying a retroactive invoice adjustment.

Before issuing PUT, an account-serialized transaction reserves the maximum
authorized size for its bucket/key and one key slot. Reissuing the same size
or a smaller size does not reserve bytes again. The reservation is committed
before signing and is not refunded on signer errors or lost HTTP responses.
GET and PUT both consume a separate monthly authorization count, used only
for issuance abuse protection—not as a count of actual upstream requests.

Control multipart creation commits its fixed session and declared full-object
capacity in one transaction. Session-limit or quota failures leave neither a
session nor a grant. Creation retries reuse the accepted session without
spending another authorization. Version-accounted buckets reserve each new
version separately, and completion reuses that reservation. Capacity remains
charged through lost acknowledgments and restart recovery; verified abort and
fenced provider inventory are required before reclamation. Existing legacy
sessions retain their original accounting contract. See
[ADR-560](adr/560-fixed-multipart-admission.md).

Capacity is **conservative**, not a bill: the first inventory baseline plus
per-key grants, or the latest observed bytes/keys, whichever is larger. An
overwrite of a pre-existing baseline key may reserve its size again. Deleting
an object, letting a URL expire, or observing an empty bucket does not reclaim
granted capacity: an accepted in-flight PUT may finish later. Confirmed bucket
deletion releases capacity. Qualified tracked writes can also reclaim grants
through a complete fenced capacity reconciliation, described below. Historical
or untracked grants remain conservative. Do not manually edit counters.

Every apid runs a bounded inventory worker at startup and once a minute after
the previous sweep. It claims up to ten ready buckets, with two-minute leases,
a 45-second scan deadline and at most 1000 pages of 1000 keys. Buckets are due
every five minutes. Only complete scans publish a durable observation/sample;
failed/partial/cyclic scans preserve the last observation. Inventory older
than 15 minutes blocks new URLs. Large inventories that cannot complete inside
these bounds fail closed and need a qualified inventory adapter before launch.

The data protocol cannot provide portable request/egress billing. Configure
each backend's optional `usage_reports_path` to an **absolute, operator-owned
regular JSON file**, readable by apid but not writable by customer workloads.
A provider-specific exporter must atomically replace this file with an array
of `ObjectStorageUsageReport` records from actual provider data. Apid imports
it each accounting sweep. Files are capped at 4 MiB / 10,000 reports. Keep
exports limited to the latest cumulative report per account/month/backend.
Publish the same feed to all API replicas, or configure a designated importer.

Each record includes `account_id`, `backend_id`, `backend_fingerprint`, a stable
`source`, UTC `period_start` (first of month), `observed_at` (provider coverage
time, **not export time**), `stored_byte_hours`, actual `request_count`, actual
`egress_bytes`, and `cost_millicents`. Attribute costs using the catalog's
physical bucket/account mapping. The source must cover storage, requests,
egress, and applicable provider charges in the declared EUR cost convention;
do not import an account-total into each tenant or treat delayed/missing data
as zero. Neither Gregale's compute MB-seconds nor inventory samples substitute
for these billing quantities.

The repository includes an OVH Public Cloud adapter in
`pkg/objectstorage/ovh_usage.go`. It reads the provider's signed usage-history
API and normalizes bucket storage byte-hours, outgoing bandwidth, and provider
costs when the provider response is denominated in EUR. A non-EUR project is
rejected until an explicit operator FX/conversion policy exists. OVH's public
usage response does not currently include a request count, so the branded
gateway records every outbound provider request attempt in the durable
`object_storage_request_metrics` ledger. For GA, the OVH runner reads OVH
Server Access Logging from the configured operator bucket per physical bucket
and month, which also covers direct provider URLs; it fails closed when a log
object is missing, malformed, partial, or unknown. It never substitutes
signed-URL issuance counts or an explicit zero for unavailable data. The
catalog and request source are injected through narrow interfaces so a
qualified R2, AWS, GCS, or storage-node adapter can replace OVH without
changing this report contract.

Provider adapters may implement the `objectstorage.UsageReportExporter` seam
and publish through `objectstorage.ExportUsageReports`. The helper validates
that a batch belongs to one backend and UTC period, rejects duplicate account
rows, and atomically replaces the configured report file with owner-only
permissions. This keeps provider credentials, billing APIs, and attribution
logic outside the data drivers; the adapter remains responsible for
obtaining authoritative data and mapping each physical Gregale bucket to one
account.

For an OVH backend, set `usage.driver` to `ovh`, provide the three OVH API
credential environment-variable names, and configure `request_log_bucket` (and
optionally `request_log_prefix`) for a dedicated same-region bucket receiving
[OVH Server Access Logging](https://docs.ovhcloud.com/en/guides/storage-and-backup/object-storage/s3-server-access-logging)
from every managed physical bucket. Keep
`usage_reports_path` in the operator-owned registry. `s3-gatewayd` runs the
exporter immediately and every five minutes, atomically replaces that file, and
exposes `s3_gateway_usage_exports_total` plus the last-success timestamp on its
control-plane metrics endpoint. OVH access logs are delivered asynchronously
(normally about one hour later), so the report's `observed_at` is intentionally
one hour behind the wall clock. Missing credentials, an unreadable/malformed
log object, or a failed export does not publish a partial report; `apid`
therefore continues to enforce the configured freshness window and fails closed
when the previous report ages out. The durable request ledger remains useful
for gateway diagnostics, but access logs are authoritative because API-issued
direct S3 URLs bypass the gateway.

All fields are required, including explicit zero measurements. Reports must
match catalogued backend placement. Identical repeats are harmless;
conflicting duplicates, future observations, decreasing counters/costs, or
changed source identity within a month are rejected. Older monthly evidence
is retained. Corrections reducing totals need a future adjustment workflow.
For manual import, `POST /v1/admin/object-storage/usage-reports` accepts one
record using the existing operator session, recent step-up, allowlist and
Idempotency-Key policy. Normal customer or operator bearer keys cannot import.

`GET /v1/account/object-storage-usage` requires usage-read scope and returns
observed bytes, reserved capacity, cumulative reported usage/cost, authorization
count, policy, and `fresh`. It does not expose credentials or backend placement.
Do not interpret zero counters with `fresh: false` as measured zero usage.
At month rollover, a fresh new-month report is required rather than silently
resetting to an unknown zero. Deleted buckets' monthly costs remain counted.

New URLs return 503 `object_storage_usage_stale` when policy or observations
are unavailable, 402 `object_storage_budget_reached` at cost/request/egress or
authorization ceilings, and PUT returns 409 `object_storage_capacity_reserved`
when a capacity reservation would exceed a limit. Limit errors include
`limit`, `observed`, and a documentation link. Cleanup remains authorized and
available when budgets or the global flag block new URLs.

Monitor `faas_object_storage_inventory_scans_total{outcome="success|failed"}`,
inventory-sweep/provider-import warnings, usage freshness and admission errors.
Logs omit provider error strings, credentials, signed URLs and object keys.

**These are delayed cutoffs, not a hard money cap.** Report latency, sweep
cadence, up to 15 minutes of URL validity, and already-started transfers permit
overshoot; there is no bounded monetary overshoot. Stored data continues to
accrue cost, and permitted cleanup can incur requests after cutoff. Configure
provider budgets/alerts as an additional layer; do not promise that disabling
signing stops the provider bill.

Rollout: keep `s3_enabled=false`, apply migrations, deploy every API replica,
and ensure no old unaccounted URLs or in-flight writes remain before building
the initial baseline. Disabling alone does not end an in-flight transfer;
use the qualified provider's quiescence procedure. Configure/verify the real
usage exporter and explicit limits, confirm fresh observations, then enable.
Rollback must disable signing first; old binaries bypass these guards.
Do not drop accounting tables while serving customer storage.

No plan allowances are introduced by the accounting surface; compute billing
is unchanged. `apid` closes the prior UTC month once per deployment period and
stores an immutable per-account billing snapshot. Polar can publish that
snapshot idempotently when its independent rollout gate is live; other billing
providers retain the internal ledger only. A qualified provider usage exporter
and live month-close verification remain required for paid launch; see
[ADR-156](adr/156-object-storage-accounting.md). The future provider-neutral
customer-billing contract keeps direct transfers and requires qualified
evidence for each charged dimension; it is not enabled by the current report
format. See [ADR-237](adr/237-provider-neutral-object-storage-billing.md).

An additive v2 customer-usage report ledger is available to provider adapters
for shadow evidence. It stores cumulative stored byte-hours, read and write
operations, and nullable egress bytes without any provider-cost field. Its
coverage interval, observation time, source, and evidence digest are recorded
per account, backend, and UTC month. The v2 ledger is **not** yet an admission
or month-close input: the existing v1 report, including its required provider
cost, continues to fail closed for signed URLs and billing. Importing v2 data
alone does not enable object storage or paid billing.

## Provider configuration

Use `driver: "s3"` for S3-compatible services and `driver: "gcs"` for native
Google Cloud Storage. `region` is Gregale's product region. `s3_region` is only
an S3 signing/location setting; `gcs_location` is the GCS bucket placement. A
matching product-region name is not evidence of physical colocation.

| Backend | Endpoint / signing region | Qualification notes |
| --- | --- | --- |
| Google Cloud Storage | Native endpoint, e.g. `EUROPE-WEST3` bucket location | Uses ADC/OAuth for control operations and IAM `signBlob` for V4 URLs; no HMAC or downloaded service-account key. XML multipart preserves the common 5 TiB/10,000-part contract. |
| OVH US Virginia | `https://s3.us-east-va.io.cloud.ovh.us`, `us-east-va` | Example targets the US S3 service, not the legacy Swift endpoint. Verify project availability and selected storage class. |
| AWS S3 Northern Virginia | `https://s3.us-east-1.amazonaws.com`, `us-east-1` | The driver omits CreateBucket LocationConstraint in this region. Use dedicated IAM permissions and account-level public-access blocking. |
| Cloudflare R2 | `https://<account-id>.r2.cloudflarestorage.com`, `auto` | Set `path_style: true`; account token must permit bucket management. R2's placement is not an AWS-style us-east-1 residency guarantee. |
| Your Ceph RGW / other S3 cluster | Your externally reachable HTTPS endpoint and configured zonegroup region | Set `path_style` to match the deployment. Verify TLS, privacy, CORS, signatures and failure behavior. Gregale does not provision the cluster. |

The example endpoint follows [OVH's endpoint guide](https://support.us.ovhcloud.com/hc/en-us/articles/10667991081107-Object-Storage-Endpoints-and-geoavailability).
R2 supports this subset, including CreateBucket, PutBucketCors, ListObjectsV2 and
PutObject Content-MD5, with `auto` as its signing region; see
[R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).
These are configuration targets, not a claim of completed live-provider testing.
Other providers may require another factory while preserving the same Gregale
API. Do not equate a common object-transfer protocol with identical IAM,
retention or billing APIs.

Only explicit HTTPS origins are accepted for CORS. For isolated local development,
`allow_http: true` permits HTTP endpoints/origins. Production presigned URLs must
be reachable from customers' browsers and app networks; an internal-only endpoint
does not work. Configure the Gregale app's egress rules for its chosen upstream
as necessary. CORS is not authorization: a signed URL grants access to its holder.
Origins are applied during bucket provisioning, not continuously reconciled.
Changing the console origin requires updating existing buckets' CORS through
the operator/provider tools as well as this configuration.

## API quick reference

All paths are under `/v1/apps/{slug}/buckets`, authenticated using existing
Gregale sessions/API keys and MFA policy. Bucket lifecycle and grant management
require `storage:manage`; object reads require `storage:read`; object writes
require `storage:write`. `admin` and dashboard sessions retain full access.

### Policy-controlled uploads

For application uploads that should not wake or proxy through the customer's
runtime, declare an edge route with `POST /v1/apps/{slug}/upload-routes`:

```json
{
  "name": "avatar",
  "bucket_id": "…",
  "key_prefix": "avatars",
  "max_bytes": 5242880,
  "allowed_content_types": ["image/*"]
}
```

The public app hostname accepts `POST /uploads/avatar`. Gregale requires a
Bearer API key belonging to an active app account, with `storage:write` or
`admin` scope and a matching app binding when present. It requires
`Content-Length`, checks the route byte and content-type policy, and streams the
bounded body to the selected provider. Without an idempotency key, the generated
key is `key_prefix/{api-key-id}/{uuid}`. Successful uploads return the object
reference and completion ID.

Clients that retry should send an `Idempotency-Key` (up to 128 bytes), scoped to
the route and authenticated API-key subject. S3 routes commit a durable receipt
and tracked quota reservation together before one provider write attempt.
Concurrent retries spend no additional quota or authorization. Completed retries
return the same response; different request metadata or pending outcomes return
`409 Conflict`. A pending response includes `Retry-After: 30`. The fingerprint
covers route, subject, byte count, normalized content type and optional
`Content-MD5` or `Digest` headers. Those headers participate in request matching;
the route does not validate them against the streamed body.

`X-Gregale-Upload-ID` identifies an admitted S3 upload even after an error.
Use the same API key to read `GET /uploads/avatar/receipts/{id}` for its object
reference and `pending`, `completed` or `failed` status. Another API-key subject
receives 404. Receipt reads remain available when uploads or the route are
disabled. Definitive provider rejections become failed receipts and replay as
502. Lost responses, 5xx, timeouts and missing acknowledgments remain pending;
retry with the same idempotency key or poll the receipt instead of starting a
new upload.

The recovery worker confirms an S3 upload only when HEAD shows its internal
receipt marker, exact size and an ETag. It never resends the body. Confirmation
and quota settlement commit together, making the grant eligible for capacity
reconciliation. A missing object or elapsed time cannot prove settlement; an
object deleted or overwritten before confirmation may remain pending. Prepared
intents that were never dispatched close after one minute, fencing late callers.
The worker processes up to ten intents per sweep with one-minute leases,
ten-second probes and thirty-second retry intervals; receipt settlement has a
five-second deadline. It runs with uploads disabled or budgets exhausted.
Route deletion preserves recovery records until the owning bucket is removed.
See [ADR-534](adr/534-recoverable-application-object-uploads.md).

GCS supports tracked receipts and generation-based historical confirmation.
Third-party writers without the tracked capability retain conservative
admissions and the previous failure-receipt behavior. Historical/direct signed
uploads, copies and uncertain writes still cannot be force-refunded.

The route policy is provider-neutral. It uses the registry's optional streaming
writer, so switching an immutable bucket placement between OVH, R2, AWS, GCS,
or another compatible backend does not change the customer endpoint. The
first slice intentionally does not run application-specific business logic;
applications needing checks beyond account/key policy should keep using a
signed URL plus an application endpoint.

For a non-admin data key, a scope is necessary but not sufficient: the key must
also have an explicit grant on the target bucket. A `read` grant permits object
listing and GET URL issuance, `write` permits object deletion and PUT URL
issuance, and `read_write` permits both when the key carries both scopes.
`storage:manage` does not imply data access. A storage read/write key sees only
its granted buckets in the bucket list; a management principal sees all buckets.

- `GET /`: bucket list and configured limits/regions, across environment scopes.
- `POST /`: `{ "name": "assets", "scope": "default", "region": "us-east-1" }`.
  Omit scope/region for defaults. Retry the same name/scope after failed setup.
- `DELETE /{bucket-id}`: empty-only deletion. Nonempty returns 409; success 204;
  already deleted returns 404. Delete all buckets before deleting the app.
- `GET /{bucket-id}/access-grants`: list the bucket's API-key grants.
- `PUT /{bucket-id}/access-grants/{key-id}`: create or replace a grant with
  `{ "permission": "read" }`, `write`, or `read_write`. The target key must
  carry the corresponding storage scope(s). Admin keys do not need grants.
- `DELETE /{bucket-id}/access-grants/{key-id}`: revoke the grant. Already-issued
  branded URLs check the current grant; older native URLs retain their expiry.
- `GET /{bucket-id}/objects?prefix=folder%2F&limit=100&cursor=...`: one page;
  pass `next_cursor` without interpreting it, keeping the same prefix.
- `DELETE /{bucket-id}/objects?key=...`: URL-encode the entire exact key.
- `POST /{bucket-id}/signed-url`: `{ "method": "PUT", "key": "hello.txt",
  "size_bytes": 5, "content_type": "text/plain", "metadata": {"owner":"platform"},
  "tags": {"env":"prod"}, "expires_in": 300 }`. The returned headers must
  be preserved exactly for the upload.
  Use returned `url`, `method`, `headers` with the exact five-byte body. For
  download request `{ "method": "GET", "key": "hello.txt" }` (read scope).
- `POST /{bucket-id}/multipart-uploads`: create or recover one upload for a key
  with `{ "key": "large.bin", "size_bytes": 73400320,
  "content_type": "application/octet-stream" }`. The response gives Gregale's
  opaque upload `id`, exact `part_size_bytes`, `part_count`, and 24-hour expiry.
- `POST /{bucket-id}/multipart-uploads/{upload-id}/parts/{part}/signed-url`:
  `{ "expires_in": 300 }`. Upload that numbered part using the returned headers
  and record the provider's response `ETag`. Parts may be retried and uploaded
  concurrently for different part numbers. A settled part may be replaced;
  overlapping or uncertain attempts for the same part return 409. Every non-final part has the advertised fixed size; the final
  part may be smaller.
- `POST /{bucket-id}/multipart-uploads/{upload-id}/complete`: send every ETag in
  ascending order as `{ "parts": [{"part_number":1,"etag":"..."}] }`.
  `GET /{bucket-id}/multipart-uploads/{upload-id}` recovers session state after a
  lost API response. Complete/get/list responses include the stored final `etag`
  and public `version_id` when available. `DELETE` on that path aborts an
  unfinished session.

Use ordinary fetch/HTTP for signed URLs, **not** the authenticated Gregale client.
Never forward Gregale Authorization/cookies. Browsers set Content-Length from
the File body; preserve other returned headers, including Content-MD5 for empty
uploads. Object GET/HEAD/PUT URLs now use the branded S3 gateway. They default
to five minutes and allow at most fifteen. A PUT URL names one durable
`upload_id`: its first dispatch can replace the key, and later successful
retries return the saved ETag/version without writing again. A pending or failed
receipt returns 409; inspect the bucket write receipt before issuing a new URL.
At most 1024 object URL capabilities may be active per bucket, independently
of the ordinary credential quota. The stored request descriptor is bounded
to 32 KiB. Capacity is reserved at issuance. Expiry without dispatch fails the prepared
receipt so capacity reconciliation can safely release the reservation. Issuer deletion/expiry, bucket grant removal,
credential revocation and the ingress flag prevent new dispatch. An attempt
already dispatched may finish and settle. Do not log or persist URLs. Avoid automatic retries of URL issuance, which
would create another capability and reservation; the Go SDK enforces this even
when generic retries are enabled.

Include PUT-only `encryption` with `algorithm`, an enrolled `key_id` for KMS,
optional `bucket_key_enabled` and optional `context`. The gateway captures the
owned selection before issuing the URL and returns owned encryption headers.
GCS supports enrolled AES256 and confirms the stored receipt, native generation
and encryption intent before acknowledging it. CMEK and other encryption modes
remain unsupported. Lost acknowledgments retain their receipt for exact current
or historical proof. Fixed multipart part URLs use the branded endpoint too. The
URL binds the owned session, exact part and length; it stops admitting writes
when the session starts completion or abort. Native upload IDs stay private.
URLs issued before ADR-557/558 retain their native provider expiry; changing
permissions cannot revoke those older capabilities.

Control multipart creation accepts the same optional owned `encryption`
selection. Initiation captures it in the immutable multipart journal, parts
return owned cipher acknowledgment headers, and complete/get/list responses
include the captured selection. Send initiation encryption parameters only in
the creation request. Part requests use exactly their returned headers.

Multipart sessions reserve the declared final size before upstream initiation
and use the same `storage:write` scope plus bucket write grant as ordinary PUT.
Only one live session exists per bucket/key; retry creation with the same key,
size, content type and encryption selection to recover its Gregale ID. A different shape conflicts.
Gregale never exposes the provider upload ID. Part ETags and completion predicates
are durably stored before the upstream call. The actual final ETag and public
version ID are committed atomically with successful completion. An uncertain
S3 completion can recover an exact session receipt from retained versions after
an overwrite or delete marker, with bounded pages and persisted continuation.
A missing upload or history proof keeps its reservations; it cannot establish
failure. See [ADR-544](adr/544-durable-s3-multipart-completion-identities.md).
Sessions expire after
24 hours; the recovery worker aborts expired upstream parts even while new
object-storage operations are disabled. The upstream lifecycle rule is still
required as a defense against control-plane outages.

Abort can return 409 while a native part attempt is still unsafe, older direct
part URLs remain within their cleanup delay, or the provider reports parts. An
unused branded URL creates no extra drain delay. The session remains `aborting`; retrieve
it with GET and let the recovery worker retry, including after a server restart.
Signing rechecks the active session before returning a URL, so an abort or
completion admitted during signing prevents publication. Cleanup drains actual
transfers and legacy URL deadlines, then checks a bounded part listing before reporting
`aborted`. Elapsed time and provider acknowledgment alone do not refund legacy
key reservations or change the inventory baseline. See
[ADR-550](adr/550-durable-object-lifecycle.md).

Key rotation copies bucket grants to the successor so applications can switch
credentials during the normal grace window. For compute workloads, prefer the
compute-binding API above so Gregale owns the sealed credential lifecycle;
standalone credentials remain available for laptops, CI, and external clients.
Gregale never gives a workload the operator's upstream provider credential.

## Switch providers without rewriting the product

Add a second backend with a new immutable `id` and `namespace`, then change
`defaults.us-east-1` to that ID. New buckets use it. Existing buckets retain
their recorded backend; keep its configuration and credentials available.
Endpoint, provider placement, driver or namespace changes fence existing buckets
with 503 instead of redirecting them. Rotate keys within the same namespace by
changing secret values and restarting, not by changing the backend identity.

There is intentionally **no automatic existing-bucket migration**. A future
migration worker must stop new writes/signing, wait out issued URLs, copy and
verify objects/metadata, explicitly update placement, and retain rollback data.
The registry and durable placement remove the API/UI rewrite, not the cost or
operational risk of moving bytes. Native customer S3/HMAC credentials require a
separate tenant IAM adapter; never hand out the operator-wide credential.

## Qualification and launch checklist

- Create a bucket; retry creation and verify only one upstream bucket exists.
- Check unauthenticated list/get/put are denied. Check another Gregale account
  cannot list, sign, delete, or guess access to this bucket.
- Upload/download a Unicode/spaced key, an empty object, and a file near the
  configured limit from the actual console origin. Verify exact contents.
- Upload a multipart object larger than 64 MiB with concurrent parts. Retry a
  part, resume by Gregale upload ID, interrupt apid before and after upstream
  completion, verify exact bytes/metadata, abort a session, and verify an expired
  session is removed upstream while `s3_enabled=false`.
- Tamper with a nonempty signed upload's Content-Length and an empty upload's
  body: both must fail. Verify wrong key/method and expired URLs fail.
- Test CORS preflight, pagination, deletion of objects, nonempty bucket rejection,
  empty deletion, upstream outage, and automatic recovery after apid interruption
  before/after upstream success and before/after catalog completion. Verify
  cooldowns survive restart and multiple replicas do not duplicate active work.
- Add a second backend and switch the default. Old/new buckets must use their
  respective backends; removing the old config must fail closed.
- Keep operator monitoring/budgets in place. Defaults are 10 buckets/app and
  100 MiB/object, configurable up to 100 buckets and 5 TiB objects. Omitted
  `max_single_put_bytes` retains the previous ceiling (at most 5 GiB), except
  an explicit proxied profile defaults to at most 64 MiB. Omitted
  `max_part_bytes` defaults to at most 64 MiB. Proxied profiles reject either
  request limit above 64 MiB. A single signed PUT remains capped at 5 GiB; larger objects use multipart. These alone do **not** cap total
  bytes or costs; configure and qualify the accounting controls above. Presign
  counts cannot meter actual usage. The optional rate card is only an estimate;
  plan allowances and invoice lines do not ship here.
- Before paid/general availability, qualify the real provider usage exporter,
  pricing/margin policy and budget cutoffs, tenant native storage keys if needed,
  and a coordinated account-deletion workflow. Active buckets block account
  hard-deletion; confirmed-deleted bucket metadata is purged with the account.
  Do not bypass these guards and orphan customer data.

Deferred: the S3 compatibility gaps listed above, production edge/service
activation, unsupported lifecycle/version behavior, untracked object-capacity rebasing,
historical untracked multipart reclamation, and automatic migrations.

## Recoverable branded PUTs

Native S3 metadata and error responses are capped at 32 MiB before SDK
deserialization. Successful object downloads continue streaming independently
of that budget; HEAD Content-Length describes the object size. Oversized
mutation acknowledgments leave their write or multipart receipt uncertain.
Provider and gateway PUT acknowledgments reject duplicate or invalid ETag,
version and delete-marker headers before settlement.

New PUT requests through `s3.gregale.dev` on S3 backends persist a receipt with
the quota reservation and use one provider attempt. `X-Gregale-Upload-ID`
identifies the admitted request. Every client PUT remains an independent S3
write; sharing a key does not deduplicate requests. A pending receipt exclusively
owns its key until settlement: another PUT/copy/route authorization or multipart
completion receives a conflict, while independent keys and reads continue.
This also keeps current-object recovery proof from being overwritten by later
Gregale admissions. Conditional writes and
customer metadata/tags retain their existing behavior.

After a lost provider acknowledgment or gateway restart, the shared upload
worker verifies the exact private receipt marker, size and ETag through HEAD.
Only positive proof settles the write and allows fenced capacity reconciliation
to proceed. Recovery does not replay the staged body. Missing/overwritten
objects remain pending; older hash-only admissions and providers without the
capability cannot gain recovery proof retroactively.

A missing or invalid ETag on a provider 2xx returns `ServiceUnavailable`, and
HTTP 408 remains uncertain. Known pre-dispatch failures and definitive service
rejections close the receipt and journal together. Recovery continues when
signing is disabled or monthly budgets are spent, without resetting monthly
authorizations or billing. The shared limits are ten receipts per sweep, a
one-minute preparation timeout and recovery lease, ten-second probes,
thirty-second retries, five-second detached settlement, and thirty-minute
transfers. Gateway-owned provider PUT URLs expire after one minute and stay
private. See [ADR-535](adr/535-recoverable-s3-gateway-puts.md).

## Recoverable branded copies

Same-bucket `CopyObject` through the branded gateway now tracks a durable
destination receipt on S3 backends. A source HEAD captures the size, ETag and
private native version. A native source requires a verified `all_versions`
capacity baseline and is copied from exactly the inspected version, even if
the current object is later overwritten. Mutable sources use an atomic ETag
condition; a change returns 412 `PreconditionFailed`. Missing source size/ETag,
weak ETags, native sources without that baseline and copies exceeding the
existing 5 GiB single-write limit fail closed.

Metadata COPY preserves the captured source HTTP/customer metadata and Expires
while replacing private markers with a fresh receipt. REPLACE uses customer
metadata; tag COPY/REPLACE stays independent. Metadata-only source changes
that preserve its ETag do not change the captured metadata snapshot. Customer
copy ETag conditions are checked against the source snapshot and preserved by
the atomic provider copy. A failed predicate returns 412 before destination
admission. Providers without the conditional tracked-copy capability return
501 for these headers. Native sources also support signed copy-source
If-Modified-Since and If-Unmodified-Since dates. The provider evaluates dates
on the inspected immutable version. Gregale omits its internal If-Match when
the customer did not supply one, preserving standalone date semantics. Date
headers must contain one valid HTTP date of at most 128 bytes. Independently
restrictive dates on absent/null native versions remain unsupported; a customer
If-Match can accompany If-Unmodified-Since using S3's ETag precedence. See
[ADR-541](adr/541-immutable-s3-copy-sources-and-date-conditions.md).

The source probe passes read admission before HEAD. A successful tracked copy
consumes two monthly safety authorizations (probe and destination admission);
an ETag predicate rejected by the probe consumes only the probe authorization
and reserves no destination capacity. Provider-side date rejection occurs after
destination admission, spends both authorizations and keeps conservative quota
until verified reconciliation. Spent budgets or stale usage block the probe itself.

Admitted copies return `X-Gregale-Upload-ID`. Each request uses one provider
copy attempt. Lost, truncated or invalid acknowledgments, HTTP 408/5xx and
embedded errors inside HTTP 200 remain pending. Recovery confirms only the
destination receipt, size and ETag; it never repeats the copy. After confirmed
settlement, deletion and fenced inventory can reclaim capacity without
refunding monthly authorizations or billing. Existing upload recovery and
transfer limits apply. GCS tracked copies use the same owned receipts and source
grant admission. Older copies, environment-clone copies
and providers without the capability remain conservative.
Apply the additive migration before upgrading gateways and API workers.
See [ADR-536](adr/536-recoverable-s3-gateway-copies.md).

## Multipart server-side copy

GCS uses a generation- and metageneration-fenced native GET streamed into a
part PUT. This consumes one extra provider request and reserves the copied
source length against gateway safety egress before reading it. Range responses
must match the requested interval exactly. Interrupted copy reservations remain
conservative. Native multipart completion confirms the stored session receipt
and generation, including retained history after a later replacement.

## CLI file transfers

```sh
gregale bucket upload <app> <bucket-id> <key> <file> --content-type text/plain
gregale bucket upload <app> <bucket-id> <key> <file> --resume <upload-id>
gregale bucket download <app> <bucket-id> <key> <file>
gregale bucket download <app> <bucket-id> <key> <file> --force
gregale bucket uploads list <app> <bucket-id>
gregale bucket uploads status <app> <bucket-id> <upload-id>
gregale bucket uploads parts <app> <bucket-id> <upload-id>
gregale usage object-storage
```

Transfers stream regular files with a default thirty-minute deadline. Uploads
automatically use multipart above the server's single PUT limit. An uncertain
transfer returns its pending receipt or session ID for inspection; it does not
automatically abort or repeat an admitted write. Downloads publish atomically
after success, preserve existing files on failure, and require `--force` to
replace a destination. JSON output includes the key, file, size, status and
upload ID. Usage output marks unavailable meters as unknown.

Multipart uploads save a private local checkpoint before issuing part URLs.
Use `--resume <upload-id>` with the same API endpoint, app, bucket, key and file
contents. The source may move to another path, but its size and SHA-256 must
match. An explicit `--content-type` must also match; otherwise resume preserves
the session's original type. The CLI validates all part-list pages and skips
only parts whose native listing matches the checkpoint's saved ETag. A part
with a lost acknowledgment is resent from verified staged bytes. Changed or
unexpected parts fail without completing a mixed object.

Checkpoints live in `gregale/object-uploads` under `XDG_STATE_HOME` when set,
otherwise the user's configuration directory. They contain fingerprints and
public session identity, never credentials or signed URLs. Keep this directory
for later recovery. Completed records remain available for status-based replay;
they can be removed when recovery is no longer needed. Each record is bounded
at 4 MiB, with at most 10,000 parts. Processes sharing a checkpoint cannot resume
it concurrently. Each missing or unacknowledged part needs temporary disk space
up to the session's configured part size, bounded by 5 GiB. The staged part is
removed after its attempt; a killed process leaves one private staged file,
which the next resume removes under the session lock. This extra local I/O prevents an in-place source
change from altering bytes after verification.

Before completion, the CLI persists the ordered part manifest. If the completion
response is lost, resume checks the durable session first: an already completed
session returns its saved result without issuing another completion. A pending
completion reuses exactly that manifest through the server's recovery journal.
Expired, aborted, foreign, or uncheckpointed sessions are rejected. A lost create
response before the first checkpoint still needs session inspection; this CLI
path does not automatically create a replacement. Single PUTs continue to use
their existing write-receipt inspection path. See [ADR-688](adr/688-resumable-cli-object-uploads.md).

GCS supports tracked PUT/copy recovery, native version controls and reads, copy
grants and enrolled AES256. The GCS example enrolls AES256 explicitly. Adding
enrollment to an existing backend changes its immutable placement fingerprint;
preserve existing placement configuration and use the normal adoption process.
Native generations remain private, and ordinary deletion does not manufacture
S3 delete markers. GCS Object Lock, CMEK and conditional multipart completion remain
unsupported. See [ADR-628](adr/628-gcs-tracked-writes-and-native-generations.md).

## Multipart copy admission

S3 backends implement `UploadPartCopy` within the credential's logical bucket.
The credential needs both read and write permissions. Initiate the destination
multipart upload normally, then copy a whole source or an inclusive
`x-amz-copy-source-range: bytes=first-last` into a numbered part. Complete the
upload with the returned part ETags. Listing and aborting use the same Gregale
upload ID as ordinary uploaded parts. Metadata and tags come from initiation;
part-copy metadata/tag directives are rejected.

```sh
aws --endpoint-url https://s3.gregale.dev s3api upload-part-copy \
  --bucket assets --key archive.bin --upload-id "$GREGALE_UPLOAD_ID" \
  --part-number 1 --copy-source assets/source.bin \
  --copy-source-range bytes=0-10485759
```

The part must fit `max_part_bytes` and the total upload must fit
`max_upload_bytes`. Range copies can read a source larger than 5 GiB without
copying its entire body through the gateway; their source may be at most the
existing 5 TiB total-object ceiling. A range source must exceed 5 MiB. Open-ended,
suffix and multiple ranges are invalid. Every completed part except the last
must meet the 5 MiB multipart minimum. Native source versions use the same
immutable-source and `all_versions` baseline requirements as CopyObject, with
the same source date predicates. Append `?versionId=ID` to the URL-encoded
`--copy-source` value to select a customer version ID (ADR-543).

Gregale measures the source, reserves only the copied bytes and atomically
requires the measured ETag at the provider. Customer source ETag conditions
also apply. A source change returns 412. One provider request attempts the
copy; a valid full `CopyPartResult` acknowledges it. An uncertain response
retains the transfer fence and reserved capacity, preventing immediate
overwrite/completion/abort races. It does not automatically replay the copy or
refund capacity. Definitive rejections settle the fence; verified abort cleanup
reclaims tracked part grants. Providers without the optional part-copy
capability return 501. See
[ADR-538](adr/538-s3-multipart-copy-and-source-etag-conditions.md) and the
[remaining implementation scope](s3-implementation-gaps.md).

## Inspect write receipts

An admitted branded S3 PUT or CopyObject returns `X-Gregale-Upload-ID`, including
on an uncertain error response. Poll its durable receipt with a **SigV4-signed**
GET on the original object path:

```text
GET https://s3.gregale.dev/assets/path/to/key?gregale-upload-id=<receipt-id>
```

This Gregale extension returns JSON with `pending`, `completed` or `failed`
status, operation, key, bytes, content type, ETag, creation time and an optional
bounded error code. Use the credential that issued the write; another credential
or a different key receives 404. Revoked credentials cannot poll. A pending
response includes `Retry-After: 30`; all receipts use `Cache-Control: no-store`.
Polling remains available with storage disabled, spent budgets or unavailable
provider placement, and makes no upstream request or new quota admission.

Use the management API or CLI to inspect bucket writes, including application
upload routes and writes whose original credential has been revoked:

```sh
gregale bucket writes list <app> <bucket-id>
gregale bucket writes list <app> <bucket-id> --status=all --limit=50
gregale bucket writes status <app> <bucket-id> <receipt-id>
gregale bucket writes wait <app> <bucket-id> <receipt-id> --timeout=5m
```

The API paths are `GET /v1/apps/{slug}/buckets/{bucket}/write-receipts` and the
same path followed by `/{receipt}`. Both require storage write scope and the
bucket write grant under the existing MFA policy. List defaults to pending and
accepts `status=pending|completed|failed|all`, `limit=1..100` (default 50) and
`cursor` from the previous `next_cursor`. Results are newest first. Reuse the
same bucket and filter for subsequent pages; pending pages may change as
recovery settles writes. JSON CLI output uses the same public fields. `wait`
polls every five seconds (configurable, minimum one second), exits nonzero for
a failed write or timeout, and prints the last pending receipt on timeout.

Completed proves that this attempt committed; the key may since have changed.
Pending has no confirmed outcome: do not treat it as failure or blindly resend
the write. When native S3 history retains the private receipt, recovery can
confirm an older version after overwrite or a delete marker. Each probe lists
at most ten entries, inspects exact versions and persists pagination progress
across worker restarts. Missing history still leaves the receipt pending.
This read-only recovery does not enable bucket versioning or retain proof on
unversioned backends. See [ADR-539](adr/539-historical-s3-write-receipt-recovery.md).
These reads cannot force completion or refund capacity. Legacy/untracked writes,
direct signed uploads and multipart sessions are outside this list; use the
multipart status API for multipart uploads. Pending-write lists help diagnose
`waiting/unsettled_writes` capacity jobs. Apply the listing index migration
before rollout. See [ADR-537](adr/537-customer-object-write-receipts.md).

## Reclaim reserved capacity

For buckets using tracked branded S3 PUTs/copies, S3 application upload routes or public
multipart completion, request an inventory and capacity reconciliation after deleting or shrinking objects:

```sh
gregale bucket reconcile start <app> <bucket-id>
gregale bucket reconcile status <app> <bucket-id> <job-id>
gregale bucket reconcile cancel <app> <bucket-id> <job-id>
```

The API equivalents are `POST /v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations`
and `GET`/`DELETE` on that path followed by `/{reconciliation}`. All require
storage write scope and the bucket's write grant. POST returns 202; GET and
cancellation return the job with reserved/reclaimed bytes and keys, pending-write
count, state, timestamps and a bounded error code. Repeated POST returns the
active job. Finish or abort live multipart sessions before starting a job.

An active job pauses new uploads for its bucket. Reads remain available.
Current-object cleanup remains available for buckets using current-object accounting. A complete inventory under a fenced lease atomically rebases quota;
partial scans, cancelled jobs and stale workers cannot refund capacity. Billing
usage and monthly authorization counts stay intact. Cleanup works with storage
disabled or a spent budget. The worker processes up to 10 due jobs per sweep,
uses a two-minute lease, scans for up to 45 seconds and 1,000 pages, retries after
30 seconds, and stops waiting after one hour. Cancellation releases the write
pause immediately without changing reservations.

`blocked/untracked_writes` means legacy, direct signed uploads, untracked native
uploads or untracked copies have conservative grants. `waiting/unsettled_writes` means a tracked request has
no confirmed outcome. An expired URL or elapsed deadline cannot settle an
uncertain write. Those cases retain capacity; this release does not offer a force
refund. Use dedicated managed buckets with ordered complete listings and no
independent provider writers or replication introducing objects. Versioned
buckets require the verified all-version accounting cutover described below.
When recovery or an acknowledged tracked write detects retained native S3
versions, reconciliation selects `inventory_scope=all_versions`. It counts all
retained data versions and delete markers, including null versions. A marker
uses its key's UTF-8 byte length; the key quota counts each retained entry.
The API reports `scanned_pages`, `scanned_bytes` and `scanned_versions`; the CLI
shows that progress without native IDs. Every page commits durably and resumes
after a restart. Native scans process at most ten pages per sweep within the
same 1,000-page/job limit. Failed, duplicate or cyclic pages do not rebase quota.
Periodic refreshes use the same native journal.

New writes pause after native detection until a complete native baseline exists.
Afterward each tracked PUT/application upload/copy or multipart completion reserves
its full size and one additional entry even when overwriting the same key.
Direct signed PUTs and legacy untracked writes/completion return a conflict or
NotImplemented in this mode until their replay admission is implemented.
Current-object single/bulk DELETE and mutable null deletion use durable intents
with marker admission (ADR-547). Public bucket versioning configuration and
immutable version deletion are implemented. Customer IDs, version listing,
exact reads and restoration by same-key selected-version copy are implemented
(ADRs 542–543).
See [ADR-540](adr/540-native-s3-version-capacity-inventory.md).
See [ADR-533](adr/533-safe-object-capacity-reconciliation.md) for recovery and
rolling-upgrade guarantees.


## Bucket versioning configuration

Configure a capable S3 backend using the standard AWS SDK `GetBucketVersioning`
and `PutBucketVersioning` operations, or the control API:

- `GET /v1/apps/{slug}/buckets/{bucket}/versioning`
- `PUT /v1/apps/{slug}/buckets/{bucket}/versioning` with `{"status":"Enabled"}` or `{"status":"Suspended"}`

The control PUT returns 202 with durable progress. `desired_status` records the
request and `observed_status` records provider truth. The state progresses through
`waiting`, `propagating`, `inventory`, then `ready`. New writes and bucket deletion
remain fenced until existing work drains, propagation finishes (at least fifteen
minutes), all versions are inventoried and the provider status is verified again.
The returned `capacity_job_id` exposes the existing inventory progress API.
Cancelling that inventory retains the configuration fence and queues replacement
work. Repeated requests for the active target are safe; opposite targets conflict
until readiness. An uncertain provider acknowledgment can return an S3 503 while
the durable worker continues; inspect control progress or retry the same target.
S3 PUT returns 200 once the desired provider status is observed, before object
writes reopen. Suspension continues to account for retained versions.

```sh
gregale bucket versioning enable <app> <bucket-id>
gregale bucket versioning status <app> <bucket-id>
gregale bucket versioning suspend <app> <bucket-id>
```

Control requests require storage manage scope and bucket write access; S3 PUT
requires a bucket write credential. Discovery of provider versioning also fences
an empty bucket until adoption is verified. Unresolved legacy direct-write grants
block configuration; URL expiry alone cannot make them safe. GCS maps enabled
versioning to Enabled and its disabled Boolean to Suspended; it preserves native
generations without creating S3 delete markers. MFA Delete changes and
unsupported provider endpoints return NotImplemented.
The first GCS versioning observation also requires the existing fifteen-minute
adoption window and a complete generation inventory, including when disabled.
See [ADR-545](adr/545-durable-bucket-versioning-configuration.md). Delete-marker
admission and mutable null deletion are implemented in
[ADR-547](adr/547-durable-s3-mutable-deletion.md). Replay-safe direct writes and
recovery proof for uncertain mutable deletions remain gaps.


## Tagging current and retained versions

S3 `GetObjectTagging`, `PutObjectTagging` and `DeleteObjectTagging` accept an
owned public `VersionId`, including `null`. Omit the selector for the current
object. Non-null IDs address the same immutable data version after a restart;
`null` and the current object retain standard mutable S3 semantics. Successful
responses translate provider version headers into public IDs. Delete markers
cannot be tagged. Unsupported providers reject selected-version requests;
existing GCS and other legacy tagging capabilities continue to support current
objects.

The control API provides GET, PUT and DELETE at
`/v1/apps/{slug}/buckets/{bucket}/objects/tags?key=KEY&version_id=ID`.
Omit `version_id` for current-object operations. PUT takes
`{"tags":{"team":"archive"}}`; all three methods return the acknowledged
`tags` map and an optional public `version_id`. DELETE returns an empty map.
GET requires storage read scope and bucket read access; PUT and DELETE require
storage write scope and bucket write access. Tag cleanup remains available when
storage ingress is disabled. Typed Go, Node and Python clients expose all three
methods, and the CLI provides:

```sh
gregale bucket tags get <app> <bucket-id> hello.txt [version-id|null]
gregale bucket tags set <app> <bucket-id> hello.txt 'team=archive' [version-id|null]
gregale bucket tags clear <app> <bucket-id> hello.txt [version-id|null]
```

Tag replacement changes tags in place and preserves data, version history and
private completion metadata. It creates no data version, storage reservation or
capacity refund; provider requests still count toward usage and must pass the
customer safety budget. Observed version IDs activate the existing all-version
accounting fence. Tagging accepts at most ten tags, with portable UTF-8 byte
limits of 128 for keys and 256 for values. Empty values and empty tag sets are
valid; malformed, duplicate or oversized XML and ASCII control characters are
rejected.
XML and control JSON bodies are bounded to 16 KiB.

S3 sends one provider attempt per request. An interrupted acknowledgment reports
an error without replaying the mutation. Read the selected version to inspect
its tags, or explicitly repeat the desired replacement/clear. Concurrent tag
requests follow provider last-writer behavior; selectors do not serialize tag
changes. Local SDK/gateway/control/PostgreSQL tests cover selector ownership,
acknowledgment loss, store reconstruction, permissions and conservative capacity.
See [ADR-549](adr/549-version-specific-object-tagging.md).

## Permanently deleting retained versions

Automated developer-session and environment-clone cleanup first claims the
bucket deletion fence and waits for pending tracked writes to settle. Native
versioned (including Suspended), protected or unreadable buckets defer without
deleting current objects, preserving recovery evidence and the durable owner.
Coordinated retained/protected-version and account cleanup remains follow-up
work; account grace deletion preserves active bucket ownership meanwhile.
Legacy provider-native signed writers cannot be retroactively revoked by this
fence.

Use an owned public version UUID from ListObjectVersions or a write response:

```sh
aws --endpoint-url "$GREGALE_S3_ENDPOINT" s3api delete-object \
  --bucket assets --key hello.txt --version-id <public-version-id>
gregale bucket version-delete <app> <bucket-id> hello.txt <public-version-id>
```

The control endpoint is
`DELETE /v1/apps/{slug}/buckets/{bucket}/objects/versions?key=KEY&version_id=ID`.
URL-encode both values. It returns `version_id` and `delete_marker`. Go, Node and
Python typed clients expose `DeleteObjectBucketVersion` / `deleteObjectBucketVersion`
/ `delete_object_bucket_version`. Storage write scope and a matching bucket write
grant are required. Cleanup remains available with object data ingress disabled.

This permanently removes the selected immutable data version or marker.
Removing a marker can reveal an older data version. The public selector survives
deletion, so retrying after a lost acknowledgment or restart addresses the same
version. Repeating the same receipt ID preserves its marker flag; a distinct request after removal can return false. Missing or unowned
public selectors fail before provider contact. Private provider IDs are never
accepted or returned.

DeleteObjects supports these selectors with per-entry success/error results;
quiet mode suppresses successes and retains errors. An unsettled entry fences later entries for that bucket, including immutable
versions, until completion is proven. Conditional
DELETE, MFA and retention-bypass directives fail explicitly. Mutable `null`
deletion and ordinary DELETE creating a marker use the durable
admission/recovery implementation, including coordination with versioning changes.

Acknowledged deletion leaves capacity reserved. Run `gregale bucket reconcile
start <app> <bucket-id>` to reclaim it through a verified all-version inventory.
See [ADR-546](adr/546-immutable-s3-version-deletion.md) and
[ADR-548](adr/548-immutable-deletion-inventory-coordination.md).

Durable ordinary, null and immutable deletion uses `POST
/v1/apps/{slug}/buckets/{bucket}/objects/deletions` with an `id` UUID, `key`, and
optional `version_id` set to `"null"` or an owned public version UUID. Reuse the same ID and payload for retries; read
progress with `GET .../objects/deletions/{id}`. The CLI exposes
`gregale bucket deletions start <app> <bucket-id> <key> <request-id> [version-id|null]`
and `gregale bucket deletions status <app> <bucket-id> <request-id>`.
S3 provider credentials must allow versioning discovery and deletion, plus
version listing for Enabled marker preparation and recovery. See the
[versioning API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketVersioning.html)
and [version listing permissions](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html).

S3 clients can sign `X-Gregale-Delete-Id` for retry identity. Signed AWS SDK
invocation IDs are also used. Responses return `X-Gregale-Delete-Id`; ordinary
Enabled deletion returns an owned marker version ID. A dispatched uncertain
intent fences bucket writes, versioning, bucket deletion and inventories. Permanent
immutable deletes use the same journal. Recovery can safely resend their exact
owned private selector and settle only a valid acknowledgment; a rejection of a
recovery attempt cannot prove that the earlier request finished. An initial
known rejection releases the fence. Read the receipt rather than submitting a
new identity while the bucket is fenced.
Enabled marker creation can recover from its persisted complete version baseline.
Preparation and recovery scan at most eight pages and 4096 versions/markers;
all owned permanent deletes are excluded throughout the baseline scan. An
incomplete or oversized history fails before mutation and releases the marker
reservation. Reduce history with permanent deletion, then use a new request ID.
Uncertain null/Suspended/unversioned deletion remains pending until a stronger
completion-proof mechanism is available. A pending receipt never expires into
success or a quota refund. Each ordinary versioned delete reserves one entry and
the UTF-8 key bytes; verified all-version inventory reclaims removed capacity.
A parsed provider AccessDenied response with HTTP 403 records a failed
`provider_rejected` receipt and releases its fence/reservation. Unidentified
errors and timeouts retain the uncertain intent. A failed receipt cannot resend
the mutation; use a new ID after correcting provider permissions.
For bulk deletion, the response header identifies the batch. Per-entry
receipt IDs are UUIDv5 values using that UUID as namespace and `bulk-entry:N`
as name, where N is the zero-based entry position. Retry the unchanged batch
with the signed batch ID; the identity binds each position to its key/selector.

## Object mutation events

Confirmed tracked PUT, CopyObject, upload route and multipart completion emit
`object.created` from the reserved `gregale.storage` source. Deletions emit
`object.removed`; an ordinary versioned DELETE that creates a marker instead
emits `object.delete_marker.created`. Lifecycle expiration uses `cause: lifecycle`.
A completion describes that receipt's mutation, even if the key is overwritten
before delivery. Its `version_id`, when present, is an owned public selector.
Native provider identities and placement never appear in the event.

Declare an `event_triggers` subscription in the target application's deployment
manifest, as described in [internal event subscriptions](event-driven.md#internal-event-subscriptions):

```yaml
event_triggers:
  - source: gregale.storage
    type: object.*
    filter: '{"data":{"bucket_id":"YOUR_BUCKET_UUID","key":{"$prefix":"images/","$suffix":".jpg"}}}'
```

Subscriptions follow the existing account-scoped deployment authorization.
Filters select events within that account; they are not a separate bucket
permission boundary. This JSON filter requires both a prefix and suffix:

```json
{
  "data": {
    "bucket_id": "YOUR_BUCKET_UUID",
    "key": {"$prefix": "images/", "$suffix": ".jpg"}
  }
}
```

Matching functions receive the canonical CloudEvent as an async POST to `/`.
The event's `id` and the `x-gregale-event-id` header are stable on recovery.
The data contains `receipt_id`, `bucket_id`, `app_id`, `key`, `operation`, `cause`
and optional `version_id`/`delete_marker`; creation also includes `etag` and
`size_bytes`, including zero-byte objects. A removal acknowledgment does not
refund quota; verified complete inventory remains authoritative.

Publication and journal completion are atomic. Pending/uncertain or rejected
writes, multipart parts, aborts and direct URL settlement do not emit success
events. The durable router snapshots subscriptions at acceptance and retries
with stable invocation IDs after interruption. Delivery remains at least once;
use the event identity to make external effects idempotent. Distinct mutations
have no delivery-order guarantee. Failure/replay inspection uses the existing
event delivery and fanout surfaces. Historical completions are not replayed.

The owned S3 notification and queue profile is described below. Notifications
for direct URL or provider-external writes still require authoritative proof.


## S3 notification configuration

Configure Gregale-owned destinations through signed S3 GET/PUT `?notification`,
or `GET/PUT/DELETE /v1/apps/{slug}/buckets/{bucket}/notifications`. The control
API requires storage manage, MFA and the bucket write grant. S3 GET requires
bucket read and PUT requires bucket write. Bucket-write delegation includes
selecting eligible destinations in the same account and bucket region.

Function destinations use `arn:gregale:lambda:REGION:ACCOUNT_UUID:function:APP_UUID`.
Queue destinations use `arn:gregale:sqs:REGION:ACCOUNT_UUID:APP_UUID/QUEUE_NAME` and
require an existing enabled Gregale queue binding. AWS ARNs, SNS, EventBridge
and skip-destination-validation are unsupported. Validation checks owned state
and plan eligibility; it does not probe a function or send an AWS test message.

```json
{
  "rules": [
    {
      "id": "new-images",
      "destination": "arn:gregale:sqs:us-east-1:11111111-1111-4111-8111-111111111111:22222222-2222-4222-8222-222222222222/storage",
      "events": ["s3:ObjectCreated:*"],
      "prefix": "images/",
      "suffix": ".jpg"
    }
  ]
}
```

Replace the example account, application, region and queue with your owned
values. Control JSON filters are decoded strings. S3 XML `FilterRule` values
use form URL encoding: `+` means space and `%2B` means a literal plus.
Overlapping filters for intersecting event types are rejected. Rules are
limited to 1,000, IDs to 255 Unicode characters, key filters to 1,024 UTF-8
bytes, and configuration bodies to 1 MiB.

```sh
gregale bucket notifications set APP BUCKET_UUID notifications.json
gregale bucket notifications get APP BUCKET_UUID
gregale bucket notifications clear APP BUCKET_UUID
```

SDK methods are `GetObjectBucketNotifications`, `PutObjectBucketNotifications`
and `DeleteObjectBucketNotifications` in Go; `StorageService` exposes their
camel-case equivalents in Node; Python provides the corresponding storage
API functions and typed `ObjectBucketNotificationsRequest`/`ObjectNotificationRule`.
An empty rule array or empty S3 `NotificationConfiguration` clears intent.
Reads and clearing stay available without provider access when ingress is disabled.

Supported events are `s3:ObjectCreated:Put`, `Copy`, `CompleteMultipartUpload`;
`s3:ObjectRemoved:Delete`, `DeleteMarkerCreated`; and
`s3:LifecycleExpiration:Delete`, `DeleteMarkerCreated`, plus each group `:*`.
Lifecycle expiration does not match customer `ObjectRemoved` events. POST
uploads, tagging, restoration, replication and transition notifications are
not implemented by this profile.

Matching functions receive an async POST and queues receive a named queue
message containing `{"Records":[...]}`. Records use event schema 2.1, an event
name without `s3:`, logical bucket name, configuration ID, form-encoded key,
confirmed size/ETag when known and the owned public version selector. Queue
messages use the binding retry configuration captured with the mutation.
Gregale event headers provide stable identity for deduplicating external effects.
There is no provider sequencer, request/actor provenance or ordering promise.
The bucket ARN uses the Gregale partition; private placement and provider
version IDs are never included.

Configuration replacement/removal cannot redirect accepted events. A queue
binding removed and recreated with the same name is a different destination
for an accepted receipt. Full queues retry through durable fanout; exhausted
retries and removed destinations appear in the existing event failure and
operator replay surfaces. A lost delivery checkpoint reuses the committed
invocation without taking another queue slot. Application execution retains
normal retries, queue consumers, account concurrency and dead-letter handling.
Direct URL and provider-external mutations still have no authoritative event
proof; no historical mutation backfill occurs. See
[ADR-552](adr/552-owned-s3-notification-destinations.md).

Roll out notification-capable scheduler consumers before updating mutation
producers and exposing configuration to customers. Older scheduler consumers
do not understand the captured queue-delivery metadata. Downgrade is blocked
while active configuration or notification snapshots remain in the durable
fanout ledger, including retained delivered receipts.

## Bounded production transfers

The operator registry accepts a `transfer` policy. `profile: proxied` enforces
the service's 64 MiB single-PUT/part ceiling. `profile: direct` supports requests
and parts up to 5 GiB and multipart objects up to the independent 5 TiB total
limit, subject to provider and accounting capabilities. An omitted profile
preserves existing byte limits using direct semantics. Deployed examples
declare their profile explicitly.

`timeout_seconds` defaults to 1800 and accepts 1–86400. One request deadline
covers authentication, staging and forwarding, with socket deadlines that
interrupt stalled bodies. A timeout after provider dispatch keeps its durable
receipt and reserved usage pending; it never proves failure or permits replay.
Streaming provider reads and writes use the remaining caller budget. Native GCS reads and
writes also use this bound through their shared OAuth transport; metadata calls
retain the shorter 20-second timeout without mutating the shared client.

`max_concurrent_uploads` defaults to 4 (1–64). `max_spool_bytes` defaults to the
smaller of concurrency times the single-PUT limit and 5 GiB; it must fit one
single PUT and cannot exceed 5 GiB. `min_spool_free_bytes` defaults to 1 GiB
(1 byte–5 GiB). Unwritten concurrent reservations count against free space.
These are per-process limits; replicas need separate spool capacity. Multipart
parts stream through upload slots and durable admission without whole-part
staging. Admission returns `SlowDown`; HTTP/1 connections with unread uploads
close after the error. Clients using `Expect: 100-continue` can avoid sending a
rejected body.

The bucket catalog advertises `max_single_put_bytes`, `max_part_bytes`,
`max_upload_bytes`, `transfer_timeout_seconds` and `upload_profile`, including
in Go/Node/Python SDKs. The Ansible role requires its edge mode to match the
registry. For direct mode, route `s3.gregale.dev` by DNS-only to the Caddy TLS
origin, use the direct registry example and configure spool/account budgets.
The role preserves signed Host/path/query bytes and derives origin transport
timeouts from the registry; it does not change DNS. See
[the deployment role](../deploy/ansible/roles/s3_gateway_service/README.md) and
[ADR-553](adr/553-bounded-production-object-transfers.md).

Local AWS SDK → gateway → S3 adapter fixtures qualify a 65 MiB streamed PUT/GET,
multipart part/list/completion/read without whole-part staging,
aggregate-spool overload, byte hashes and exact accounting with memory and
PostgreSQL stores. Additional tests cover stalled-body cleanup, shared deadlines
and conservative receipt persistence after an accepted write times out. Both
Caddy profiles are rendered and validated locally. This does not establish
live provider or production network qualification.

### Durable encryption journals

ADR-555 persists each private encryption selection with the tracked write or
multipart session. The snapshot captures canonical owned identity, native key
resource and enrollment fingerprint, and is bounded to 16 KiB. Cipher-aware
multipart claims use bounded 128-byte lease tokens. State and database guards
prevent snapshot changes, ordinary legacy dispatch/claims and completion without
matching verified encryption. Native KMS and encrypted multipart initialization
requests contribute to provider request accounting. Recovery uses captured proof
after owner and provider reconstruction and can confirm an already written object
without a new enabled-key probe. Enrollment removal or remapping defers recovery.

### Explicit encryption through the S3 gateway

Use `gregale bucket encryption-keys <app> <bucket-id>` or
`GET /v1/apps/{slug}/buckets/{bucket}/encryption-capabilities` to discover the
placement's algorithms and your enrolled key references. Discovery requires
storage write scope and a bucket write grant. It reports enrollment and makes
no provider requests; each new KMS write validates enabled key identity and the
native operation enforces its effective permissions.

Set the SDK's `ServerSideEncryption` to `AES256`, `aws:kms` or `aws:kms:dsse`.
For KMS, set `SSEKMSKeyId` to the returned `arn:gregale:kms:...` reference.
Ordinary PUT, supported copies and multipart initialization capture this
selection before dispatch. KMS bucket keys default explicitly to false; a KMS
request may explicitly enable them. DSSE rejects bucket-key directives. Optional
`SSEKMSEncryptionContext` accepts canonical base64 of a bounded JSON object of
unique string keys and string values. The context is metadata, not secret
storage. Each supplied encryption header must be covered by SigV4.

GET/HEAD, successful writes and multipart completion return Gregale key
references. Provider key ARNs and private receipt/session/encryption proof never
enter public responses. Unknown native key identities and ambiguous provider
acknowledgments fail closed. An uncertain write retains its journal for proof
recovery and is never repaired by replaying its body. An already created
multipart upload can recover its identity without a new enabled-key probe.

Do not send cipher directives on reads, individual parts or completion. SSE-C,
native key references and encryption query directives are unsupported. GCS and
cross-bucket encrypted copy remain acceptance work in
`docs/s3-implementation-gaps.md`. See [ADR-556](adr/556-customer-s3-encryption.md)
and [ADR-557](adr/557-branded-object-url-capabilities.md).

### Bucket default encryption

Use `gregale bucket encryption status <app> <bucket-id>` to read the desired
and verified default. Configure AES256 with
`gregale bucket encryption AES256 <app> <bucket-id>`, or KMS with
`gregale bucket encryption aws:kms <app> <bucket-id> <owned-key-ref> [true|false]`.
`aws:kms:dsse` accepts an owned key reference without a bucket-key option.
`gregale bucket encryption clear <app> <bucket-id>` removes the owned override.
The control API exposes GET, PUT and DELETE at
`/v1/apps/{slug}/buckets/{bucket}/encryption`, with typed Go, Node and Python
clients. Management requires `storage:manage` and matching bucket write
authority. Discovery reports `bucket_defaults` for compatible placements.

PUT and DELETE return durable progress with HTTP 202. Wait for `state: ready`
before relying on a changed default. Reconciliation verifies the native
configuration and recovers an accepted mutation after a lost response.
Standard S3 SDK GetBucketEncryption, PutBucketEncryption and
DeleteBucketEncryption are also supported; mutations return success only after
verification. KMS IDs remain owned Gregale references. Bucket policies cannot
include per-object encryption contexts.

Every new implicit PUT, copy, signed PUT URL, multipart session and application
route write captures the verified default atomically with admission. Explicit
write or route selections override it. Accepted URLs, receipts and multipart
sessions retain their original selection through policy changes and retries.
Pending configuration blocks new implicit writes. Existing writes still finish.
GET and DELETE configuration and background recovery remain available when
new ingress is disabled. Clearing preserves unrelated native encryption blocking
settings and permits the provider's baseline encryption; it does not request
plaintext storage. See [ADR-561](adr/561-bucket-default-encryption.md).

### Encryption on application upload routes

Route creation/update accepts an optional `encryption` object with the same
owned selection as control multipart creation. Non-admin API keys need
`storage:manage`, `storage:write` and a bucket write grant for route management.
The route must use a ready owned
bucket and a tracked encryption-capable provider. Encrypted route size limits
also respect the configured single-PUT ceiling. Route listing returns the
public selection; private native key identity stays internal.

An authenticated `POST /uploads/{route}` uses the route policy. Cipher headers
on the upload are rejected. Each accepted write freezes the current selection
in its durable receipt; a policy change affects later admissions. Omitting
`encryption` on a full route update clears the explicit policy. A stale edge
cannot admit a write using the previous selection.

Each new KMS write checks the enrolled enabled key before object dispatch.
Provider request accounting includes the key probe, even on rejection. A lost
or mismatched encryption acknowledgment leaves the receipt pending for exact
proof recovery. Recovery can confirm the original write with KMS disabled and
never resends its body. Idempotent retries return the original receipt after
route policy changes without another key check or write. Successful upload and
receipt responses include the owned `encryption` selection. Go, Node and Python
clients expose the route policy and bucket write receipt selection. See
[ADR-559](adr/559-owned-encryption-on-upload-routes.md).

## Bucket Object Lock configuration

ADR-564 adds permanent bucket enablement and native retention defaults. The
operator must explicitly enroll an S3 backend with
`"object_lock":{"enabled":true,"event_holds":true}`. Event holds are separately
optional; `event_holds:true` requires `enabled:true`. The provider must implement
bucket lock, exact-version lock, versioning, history listing and all-version
inventory. These flags do not change immutable placement. Capability discovery
makes no native request and does not establish native permissions or health.

`GET /v1/apps/{slug}/buckets/{bucket}/object-lock-capabilities` lists the enrolled
bucket configuration and default event hold capabilities.
`GET .../object-lock` returns durable progress, separate desired/native observed
configurations, `observed_known`, a revision and permanent `enabled_required`.
Both reads remain available with ingress disabled. When enrollment is disabled,
the control GET returns existing persisted progress without contacting the
provider. No unenrolled policy is silently rewritten.

`PUT .../object-lock` accepts the following configuration and returns 202:

```json
{
  "configuration": {
    "enabled": true,
    "default_retention": {
      "mode": "COMPLIANCE",
      "days": 7,
      "default_event_hold": {"years": 1}
    }
  }
}
```

Mode is `GOVERNANCE` or `COMPLIANCE`. Specify exactly one fixed `days` or `years`
duration, an enrolled event hold duration, or both fixed and event durations.
Days are 1–36500 and years 1–100. Nulls, duplicate/case-variant keys and unknown
fields are rejected. To clear defaults for future versions, send
`{"configuration":{"enabled":true}}`. Enablement cannot be disabled; existing
version protection is retained. Management requires storage manage scope, the
bucket write grant and MFA where required.

Enablement atomically requires Enabled versioning. New writes are fenced while
accepted writes and multipart sessions drain, versioning propagates, a fresh
all-version inventory commits and native policy is verified. Identical pending
requests are idempotent; a different pending target conflicts. Accepted recovery
continues with ingress or enrollment disabled. A lost native acknowledgment is
recovered by readback rather than repeating a successful PUT. Native calls are
metered. Unknown or incomplete native responses preserve the fence and permanent
history; an empty 200 response is not verified absence.

Standard AWS SDK `GetObjectLockConfiguration` and `PutObjectLockConfiguration`
use the branded path-style S3 endpoint. GET reports fresh native truth or
`ObjectLockConfigurationNotFoundError`. PUT returns 200 only after verification;
pending cutover returns `OperationAborted` with `Retry-After: 30` and durable
intent. Inspect the control GET or retry the identical request. DELETE of the
configuration is unsupported; clear a default with an Enabled-only PUT.

Go, Node and Python clients expose the capability, status and configuration
methods. CLI examples:

```sh
gregale bucket object-lock capabilities <app> <bucket-id>
gregale bucket object-lock enable <app> <bucket-id>
gregale bucket object-lock COMPLIANCE <app> <bucket-id> --days 7 --event-years 1
gregale bucket object-lock status <app> <bucket-id>
gregale bucket object-lock clear-default <app> <bucket-id>
```

[ADR-584](adr/584-durable-object-version-protection.md) adds the per-version
management described below. [ADR-619](adr/619-durable-object-write-protection.md) adds the write snapshots
described below. [ADR-620](adr/620-protection-aware-object-lifecycle.md) adds protected
lifecycle deletion. ADRs 594 and 595 add per-version and creation event holds; governance bypass remains open.
The gateway accepts separately enrolled event-hold creation headers and rejects governance-bypass headers. Object
Lock enrollment remains explicit per backend; deployment defaults stay disabled.
Local tests qualify the implementation; production activation remains deployment work.

## Per-version retention and legal holds

Select an explicit owned public version UUIDv4 from version listing or a write
receipt. The literal `null` is accepted only in an Object Lock bucket with fresh
Enabled native versioning. There is no implicit current selector. Foreign
versions, cross-key references and native delete markers are rejected.

Control routes use `?key=<url-encoded-key>&version_id=<public-version>`:

- `GET`/`PUT .../objects/protection/retention`
- `GET`/`PUT .../objects/protection/legal-hold`
- `GET .../protection-operations/<operation-id>` for durable progress.

The prefix is `/v1/apps/{slug}/buckets/{bucket}`. GET reads native policy and
requires the bucket read grant. PUT requires the write grant, storage manage
scope, existing MFA policy, ingress and explicit backend Object Lock enrollment.
The capability response advertises `version_retention`, `version_legal_hold` and
separately enrolled `version_event_hold`.
Inspection and accepted recovery remain available when enrollment is disabled.
Receipt inspection also works while backend placement is unavailable.

Create a canonical UUIDv4 operation ID and reuse it for retries:

```json
{"id":"<operation-id>","retention":{"mode":"COMPLIANCE","retain_until_date":"2027-01-01T00:00:00Z"}}
```

Legal hold uses `{"id":"<operation-id>","legal_hold":{"status":"ON"}}`
or `OFF`. An explicit empty `retention:{}` clears expired/no fixed retention.
Active retention cannot be shortened or cleared, and active COMPLIANCE cannot
be downgraded. GOVERNANCE does not imply bypass. Dates round upward to native
millisecond precision. Governance bypass and fixed retention changes over
existing event holds remain rejected. Event-hold observations remain readable. Nulls, duplicate keys, unknown fields and bodies over 16 KiB fail.

Event holds use the same retention endpoint on backends enrolled with
`object_lock.event_holds:true`. ON requires one duration. OFF omits duration;
the provider fixes the final retention date from the active hold. For example:

```json
{"id":"<enable-operation-id>","retention":{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":30}}}
{"id":"<release-operation-id>","retention":{"mode":"COMPLIANCE","event_hold":"OFF"}}
```

Supply `retain_until_date` to preserve a requested minimum. Changing a duration
while ON requires readback that preserves the observed retention date. Releasing
without an explicit date requires a fresh ON observation. The worker persists
that policy before dispatch and verifies the provider's final date after release.
Uncertainty retains the operation and bucket fence; recovery never repeats PUT.
The private snapshot does not appear in customer receipts. Event-hold protection
for new writes and bucket default snapshots remains outside the fixed write
protection contract below.

CLI examples:

```bash
gregale bucket protection event-hold APP BUCKET KEY VERSION COMPLIANCE ON days 30 OPERATION_ID
gregale bucket protection event-hold APP BUCKET KEY VERSION COMPLIANCE OFF OPERATION_ID
```

Use `years N` instead of `days N`, and add `--retain-until RFC3339_DATE`
before the operation ID for a minimum date. Standard S3 retention XML uses
`EventHold` and `EventHoldDuration` with the same rules. Legal holds remain
independent.

PUT returns 202 after durable acceptance. The receipt transitions through
`waiting`/`applying` to `ready` or `failed`; it contains the public version and
requested policy, without private provider IDs. Reusing an ID with different
intent conflicts. An identical active request may return the existing operation
ID, so retain the returned ID for status and retries.

Standard S3 SDK `GetObjectRetention`, `PutObjectRetention`, `GetObjectLegalHold`
and `PutObjectLegalHold` use `?retention` or `?legal-hold` plus explicit
`versionId`. A signed `X-Gregale-Protection-Id` is optional; S3 responses identify
accepted work through this header. PUT returns 200 only after readback verifies
the requested policy. Pending/conflicting work returns `OperationAborted`;
uncertain provider outcomes return `ServiceUnavailable`. Inspect the control
receipt or retry identical intent. Each journal sends at most one native PUT.

One active protection operation per bucket temporarily blocks competing writes,
deletion, inventory reclamation, configuration and cleanup. Reads remain
available. Recovery uses two-minute leases, 45-second deadlines, 30-second
retries and at most 50 due operations per sweep. Unknown/mismatched readback
keeps the fence, including after restart. Neither elapsed time nor absence
proves failure. A positively parsed native rejection can end the operation;
otherwise operators must investigate provider truth without resetting dispatch
history. Native calls are metered and do not change object byte/key capacity.

Go/Node/Python clients expose the five typed protection methods. CLI examples:

```sh
gregale bucket protection retention <app> <bucket-id> <key> <version-id>
gregale bucket protection retention <app> <bucket-id> <key> <version-id> COMPLIANCE 2027-01-01T00:00:00Z <operation-id>
gregale bucket protection retention <app> <bucket-id> <key> <version-id> clear <operation-id>
gregale bucket protection legal-hold <app> <bucket-id> <key> <version-id> ON <operation-id>
gregale bucket protection legal-hold <app> <bucket-id> <key> <version-id> OFF <operation-id>
gregale bucket protection status <app> <bucket-id> <operation-id>
```


## Owned bucket and expired-account cleanup

[ADR-571](adr/571-s3-write-proof-custody-and-owned-cleanup.md) connects account
grace to the existing bucket deletion worker. Developer/clone buckets and all
buckets of an expired deletion-pending account remain sealed during cleanup.
Accepted writes, live multipart sessions and configuration/deletion jobs must
drain before the bucket is claimed. Ordinary customer bucket deletion retains
its explicit nonempty-bucket behavior.

Each provider attempt processes at most **100** current objects or exact native
versions (`ObjectOwnedCleanupBatchSize`); an account grace pass visits at most
**20** account-owned buckets (`ObjectOwnedCleanupBucketBatch`), including app
tombstones. Retries list the first page, so removed versions are persistent
progress and crashes do not leave stale cursor gaps. Larger inventories use
`cleanup_pending` with a 15-second retry. Protected data versions require fresh
retention and legal-hold reads. Active fixed/event retention or a legal hold
keeps the bucket/account metadata with `protected` and a one-hour probe. Unknown
policy and provider failures preserve the deletion fence. Cleanup never clears
holds or bypasses governance retention. Null versions and delete markers are
removed through exact selectors in the sealed bucket.

Bucket deletion must return a native 204 acknowledgment or a parsed
`NoSuchBucket`; an empty listing cannot authorize a cascade. Inactive accounts
cannot reserve more buckets, and restoration closes at the existing grace
expiry. Account metadata is removed only after confirmed native cleanup. New
Object Lock writes and protected lifecycle deletion are locally qualified by
ADRs 592 and 593. Backend enrollment and production activation remain explicit.

Key custody does not revoke native URLs issued before tracking, out-of-band
writers or provider lifecycle rules. Missing proof stays pending with its
reservation. These cases, and uncertain ordinary mutable deletions, still need
stronger retained evidence or operator resolution. Do not recreate a physical
bucket name while its cleanup journal is active.


### Protection on newly created S3 versions (ADR-619)

Protected multipart parts share the configured aggregate upload spool and
free-space floor with PUTs. Gregale verifies the incoming part, computes MD5
from its bounded spool and signs the native Content-MD5 header. Insufficient
staging capacity returns `SlowDown` before a native part write.

Owned Object Lock buckets capture the verified fixed or event retention default and any
explicit write protection when admitting each upload or copy. Standard signed
`x-amz-object-lock-mode`, `x-amz-object-lock-retain-until-date` and
`x-amz-object-lock-legal-hold` headers are supported on PUT, CopyObject and
CreateMultipartUpload. Fixed retention requires mode and date together; legal holds use `ON` or
`OFF`. Explicit dates must be in the future. Separately enrolled event holds are
described below. Governance bypass is unsupported, and parts/completion cannot replace initiation protection.

Signed object-upload and multipart-creation APIs accept an optional selection:

```json
{
  "protection": {
    "retention": {
      "mode": "COMPLIANCE",
      "retain_until_date": "2027-01-01T00:00:00Z"
    },
    "legal_hold": {"status": "ON"}
  }
}
```

Omitting `retention` inherits the admitted bucket default. Application upload
routes inherit those defaults as well. Multipart sessions retain their accepted
policy through completion. Gregale waits for exact native version readback to
verify protection before settling a protected write. Unknown outcomes keep the
receipt and capacity reservation; recovery reads the original private proof and
never repeats a PUT or copy. Accepted recovery continues after enrollment is
disabled. Private policy snapshots are bounded to 16 KiB and remain internal.

Object Lock enrollment remains explicit per backend. Local S3 protocol tests
qualify this implementation without requiring a real provider environment.

## Protection-aware lifecycle expiration

Permanent lifecycle deletion in a durably protected bucket checks fresh native
Object Lock configuration, Enabled versioning and the exact data version's
retention/legal hold. Active fixed retention, legal holds and event holds defer
the target with deletion receipt `last_error_code: object_protected`. Scans continue
past held versions, within their existing action limit, and reconsider them on
a later hourly scan. Unknown policy fails closed. Lifecycle never removes a hold
or sends a governance bypass. Current expiration can create a delete marker while
preserving locked data; marker cleanup does not read per-version data protection.

A native DELETE acknowledgment cannot complete a protected permanent deletion.
Gregale verifies complete bounded exact-key version history before clearing its
fence. Recovery settles a missing immutable target without another DELETE, and
rechecks a still-present target's policy before retrying. Qualified existing null
versions require permanent Object Lock and fresh Enabled versioning; ordinary
mutable deletions keep their conservative recovery behavior. Accepted receipt
recovery continues with new ingress or enrollment disabled. Missing historical
classification, malformed history or unknown effects retain custody and capacity.
Only verified all-version inventory changes the quota baseline after deletion.

See [ADR-620](adr/620-protection-aware-object-lifecycle.md).

### Event holds on new versions

Separately enrolled backends advertise `write_event_hold`. Owned signed-upload
and multipart initiation requests can select an event hold through `protection`:

```json
{"protection":{"retention":{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":30}}}}
```

An optional `retain_until_date` is a minimum. ON requires one days or years
duration. OFF on a new version requires a fixed date and no duration; use the
existing-version retention API to release an active hold without a fixed date.
The signed S3 PUT, copy and multipart initiation paths accept the standard
`x-amz-object-lock-event-hold` and duration-days/duration-years headers.

Omitted retention inherits the admitted bucket default, including default event
holds and fixed minima. An explicit retention selection overrides that default.
Upload routes inherit defaults. Parts and completion keep initiation policy.
Accepted receipts preserve these snapshots through disabled enrollment, changing
defaults, restarts and missing acknowledgments. Settlement requires exact-version
readback with the correct status, duration, private receipt and retention bound;
recovery does not resend the body. See [ADR-622](adr/622-event-protection-for-new-object-versions.md).

## Conditional single PUTs

Use `gregale bucket upload APP BUCKET_ID KEY FILE --if-none-match '*'`
to create an object only if no live object exists, or `--if-match '"ETAG"'`
to replace a matching object. The API signed-URL request accepts `if_match` or
`if_none_match`, and the returned condition header must be sent unchanged.
These flags are mutually exclusive and apply to single PUTs. Transfers above
the advertised single-PUT limit are rejected before multipart admission;
combining a condition with `--resume` is rejected.

Use the XML ETag returned by a gateway HEAD or CLI upload/download result for
`--if-match`. The current GCS object-listing ETag comes from JSON metadata and
cannot be used as this content predicate.

GCS resolves a strong XML ETag (or the existence wildcard `*`) with a metered
HEAD and signs the exact content generation for the native PUT. A concurrent
replacement is rejected with 412 even when its ETag is unchanged. GCS weak
ETags and ETag lists are unsupported. Missing destinations return 404; an
ETag mismatch returns 412. Failed observations issue no native PUT, and
uncertain dispatched writes retain their receipt for recovery. Conditional
GCS multipart completion requires a separate durable staging/publication
implementation and still returns 501. See [ADR-843](adr/843-gcs-conditional-put-capabilities.md).
