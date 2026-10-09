# Object-storage durable entity prototype

This trusted operator harness increments a named counter using a private S3 or
native GCS bucket. The operator command and live-provider qualification share
provider selection. Native and live-provider qualification remain pending.
It needs no SQL database or persistent local disk. Each invocation creates a new
execution owner; repeating a request ID and the same delta replays the original
result without incrementing again.

This contains the trusted storage harness and a disabled-by-default invocation
preview from [ADR-712](../../docs/adr/712-object-storage-durable-entities.md).
The Go command runs a trusted callback locally. The `app/` counter receives
transitions through Gregale's normal scheduler and microVM invocation path.
Neither is a generally available production feature.

## Run against a private test bucket

Use an existing **dedicated private test bucket** with reliable conditional writes
and strong read-after-write consistency. Provider compatibility alone does not
qualify a provider. Give the harness private GET/PUT permission, plus LIST/DELETE for
alarm qualification and cleanup, and keep all
customer writes and bucket lifecycle deletion away from the entity prefix.

Configure `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and, when needed,
`AWS_SESSION_TOKEN` through your normal credential mechanism. Do not put secrets
in source files or command arguments.

```bash
export GREGALE_ENTITY_PROVIDER=s3
export GREGALE_ENTITY_BUCKET=gregale-entities-test
export GREGALE_ENTITY_ENDPOINT=https://s3.us-east-1.amazonaws.com
export GREGALE_ENTITY_REGION=us-east-1

go run ./examples/durable-entities -entity customer:456 -request increment-001
go run ./examples/durable-entities -entity customer:456 -request increment-001
go run ./examples/durable-entities -entity customer:456 -request increment-002
```

Omitting `GREGALE_ENTITY_PROVIDER` preserves the original S3 configuration.
Only `s3` and `gcs` are accepted. The command opens an existing bucket; it does
not create a bucket or load the platform's backend registry.

On a fresh entity, these return:

```json
{"value":{"count":1},"version":1,"replayed":false}
{"value":{"count":1},"version":1,"replayed":true}
{"value":{"count":2},"version":2,"replayed":false}
```

`-account` and `-app` select independent logical scopes in this harness. They are
not authentication: the platform endpoint derives account/app scope from its
authenticated operator. Another customer or app using the same
entity key must never share its state or receipts.

Use the same request ID and payload after a timeout or uncertain commit. Reusing
an ID with a different delta fails. A live owner yields a busy error; after a
process dies, another can acquire when its lease expires (default 30 seconds).

## GCS conditional-state provider

The invocation preview also accepts a configured native GCS backend. GCS uses
the existing application-default credential and optional service-account
impersonation path. It does not use the public S3 compatibility gateway or HMAC
credentials for entity state.

Configure a GCS backend using the fields shown in
[the GCS backend example](../../deploy/object-storage.gcs.example.json). Point
`FAAS_DURABLE_ENTITY_BACKEND` and its immutable placement fingerprint at that
backend, and set `FAAS_DURABLE_ENTITY_BUCKET` to an existing, dedicated private
platform test bucket. The same app allowlist and invocation/alarm/maintenance
opt-ins apply. Grant the platform identity object read/create/replace access;
alarm discovery and cleanup need LIST/DELETE. Keep customer access,
automatic lifecycle deletion and public caching away from this bucket.

The trusted operator command also supports GCS. Configure Application Default
Credentials through the normal operator environment (for example, an attached
service account or `GOOGLE_APPLICATION_CREDENTIALS` managed outside this repo):

```bash
export GREGALE_ENTITY_PROVIDER=gcs
export GREGALE_ENTITY_BUCKET=gregale-entities-gcs-test

# Optional: use ADC to impersonate the platform's dedicated storage identity.
# export GREGALE_ENTITY_GCS_IMPERSONATE_SERVICE_ACCOUNT=entities@PROJECT.iam.gserviceaccount.com

go run ./examples/durable-entities -entity customer:456 -request increment-001
go run ./examples/durable-entities -entity customer:456 -inventory
go run ./examples/durable-entities -entity customer:456 -set-storage-limit 1048576
go run ./examples/durable-entities -entity customer:456 -cleanup
```

The impersonation target needs bucket permissions, and the ADC source needs
permission to impersonate it. GCS uses the native Google endpoint; the S3
`GREGALE_ENTITY_ENDPOINT`, `GREGALE_ENTITY_REGION` and AWS credentials do not
select its destination or identity. All operator modes work with either provider.
Keep the complete account/app/environment/customer/namespace/key scope the same
on each operation. For platform entities, supply the verified UUIDs through
`-account`, `-app`, `-environment-id` and, when applicable, `-tenant-id`.
The `GREGALE_ENTITY_*` settings configure this trusted harness only; the
platform invocation preview still requires its `FAAS_*` backend identity,
fingerprint, app allowlist and opt-ins described above.

Content generations are the opaque compare-and-swap tokens. Create uses
`ifGenerationMatch=0`; replacement uses the generation returned with the object
body. HTTP ETags and metagenerations are not ownership tokens. Each write uses
one non-resumable native JSON API upload with SDK retries disabled. A successful
ACK must identify a new positive generation and the expected size; the SDK also
checks the upload checksum. Reads bind content and generation in the same
response, reject encoded/unknown-size bodies and enforce the engine's byte limit.

Only a rejected generation precondition is a definite CAS conflict. A lost
response or any other uncertain upload result must be retried through the entity
request-ID receipt protocol. Do not retry raw writes independently.

The SDK-backed wire conformance tests cover restart restoration, exact original
receipt replay after a lost publication response, competing CAS writers,
identical-content generation changes and stale in-flight owner fencing. These
fixtures do not qualify a live GCS bucket or native microVM behavior. The testing
agent must collect that acceptance evidence before enabling a customer rollout.

## Storage and commit contract

Each entity has a manifest under
`gregale/durable-entities/v1/entities/<scope-hash>/manifest.json` and immutable
snapshots under that entity's `snapshots/` prefix. Identity hashes include
account, app, namespace and entity key, plus the verified environment and optional
customer identity in the invocation preview. Raw identities never form object paths.

1. Acquisition conditionally creates/updates the manifest, advancing its epoch
   and assigning a private claim token.
2. A pure handler computes new state, result and optional alarm deadline.
3. The engine uploads an immutable receipt and the modified index path, then a
   snapshot containing state and receipt roots.
4. It reloads authority and conditionally publishes the snapshot through the
   same manifest. A takeover or competing state commit invalidates publication.
5. Only successful publication acknowledges the transition. A lost response
   yields an uncertain outcome; a retry restores and checks the saved receipt.

The claim epoch fences writes after takeover. Renewal/release also use CAS and
fresh manifest revisions. Lease timing assumes synchronized clocks, while CAS
prevents an old owner from publishing over a successful takeover. Business-state
version changes only for committed transitions, not ownership changes.

Open performs four conditional PUT attempts and two immediate GETs against a
unique probe key before any entity executes. Stores that ignore conditions fail
closed. Unused objects are retained until operator cleanup or opt-in maintenance below.
Conditional-write probes are retained; the optional maintenance access probe deletes
its own unique key. Do not apply a deletion policy
to the entity prefix. An operator can clean up the entire isolated test bucket
after all test processes stop and the test data is no longer needed.

Restore verifies snapshot identity, schema, version and SHA-256 integrity. A
missing/corrupt committed snapshot is an error, never a new empty entity.

## Prototype boundaries

- Maximum encoded snapshot and individual receipt: 1 MiB; manifest and index
  branch: 16 KiB; identity fields: 256 bytes. Guest transition frames remain 1 MiB.
- An immutable receipt index removes the former 1,024-receipt ceiling. Receipts
  preserve original results/version indefinitely; retained storage grows with
  committed work. Legacy inline receipts are archived on the next new transition.
  Plan quotas, pricing, billing and native-provider qualification remain pending.
- Request payload identity uses exact JSON bytes. Changing formatting with the
  same request ID counts as a conflicting payload.
- Handlers serialize within one manager. Competitors across managers may compute
  concurrently, but only one can publish a given state version. Retry conflicts
  using the same request ID. Callbacks must not send payments, emails or other
  external effects; a callback may run without committing.
- Alarms persist atomically with state, including clearing them. Delivery has a
  separate opt-in preview described below and no production latency guarantee.
- Owner-affinity routing, plan quotas/billing, deployment migrations, customer
  self-service admission, cross-entity transactions and entity deletion remain
  follow-up work. No permanent VM is allocated by this engine.

## Collect unused entity objects

Use the trusted harness with the same private bucket/credentials and exact entity
scope. This command requires LIST/DELETE permission and handles one page of at
most 32 objects within twenty seconds:

```bash
go run ./examples/durable-entities -entity customer:456 -cleanup
```

For runtime entities, supply `-account <account UUID> -app <app UUID>` and
`-environment-id <immutable environment UUID>` (the app UUID for a standalone
app). Add `-tenant-id <customer UUID>` when selected and `-namespace <namespace>`.
The defaults address only the original unscoped trusted counter harness. These
selectors do not authenticate the operator; bucket credentials are privileged.

The result reports `deleted`, `retained`, `failed` and an optional `next_cursor`.
Continue with that opaque value using `-cleanup-cursor '<next_cursor>'`; an absent
cursor completes the sweep. A live owner returns busy, so retry after release or
expiry. Restarting from an empty cursor is safe. Failed or uncertain deletions
can be revisited in another sweep. Background deletion requires the separate flag below.

Cleanup CAS advances the manifest's storage generation before deleting earlier
unreachable objects. In-flight older computations cannot publish afterward. The
collector preserves current state, all committed replay receipts, shared index
branches and the legacy archive. It retains new-generation uploads, manifests,
unknown paths and probes. A delayed orphan upload can be removed on a later
sweep. A reader racing reclamation can receive a retryable conflict.

Manifest writes upgrade to schema 5; older binaries fail closed. Stop old entity
callers/alarm/maintenance workers when upgrading. Downgrading needs an explicit
storage migration. Keep bucket lifecycle deletion disabled. Versioned buckets may
retain historical versions/delete markers; this command deletes current keys and
does not reclaim those physical versions or backups.

## Enable background maintenance

After configuring the invocation preview below, opt in separately:

```bash
export FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED=1
```

The private backend needs delimiter LIST, flat LIST and DELETE in addition to
conditional GET/PUT. Startup deletes only a newly created probe object to check
access. Maintenance processes at most one entity and one bounded cleanup or
inventory page per step, then waits thirty seconds. It alternates completed sweeps
and gives missing legacy accounting priority. Busy owners are skipped, and large entities
receive one page per rotation. Only explicitly allowlisted app UUIDs are eligible.
No customer handler or SQL state is needed for housekeeping.

The bucket stores a leased scan checkpoint per allowlist and a `maintenance.json`
cursor hint per entity. Restart resumes the pending directory page after any live
checkpoint lease expires (up to five minutes). Per-entity progress survives too.
Interrupted pages can repeat safely; expired list cursors reset on errors. Corrupt
checkpoints require operator repair. Many entities, large retained journals and
busy writers increase sweep time; there is no maintenance latency guarantee.

Apid exposes Prometheus metrics for storage calls/conditional conflicts, upload
volume, invocation/alarm/maintenance outcomes, cleanup candidates, checkpoint
recovery, the last discovery rotation, alarm attempt delays and completed storage
samples. All labels use
fixed categories and omit identities and keys. Upload bytes measure acknowledged
encoded transfers, including manifest rewrites, rather than retained bucket bytes.
Cleanup deletes current keys and preserves all committed retry results. Plan
quotas, pricing, billing and real-provider operational qualification remain pending.
See [ADR-712](../../docs/adr/712-object-storage-durable-entities.md) for exact metric names.

## Inspect storage and configure a preview cap

The manifest accounts for exact encoded bytes in the current snapshot and its
reachable receipts, index branches and legacy archive. This excludes manifests,
checkpoints, probes, superseded objects, abandoned uploads and provider history.
It measures committed per-entity storage; cleanup preserves all retry receipts.

To inspect an entity in the harness, rerun until the JSON result has
`complete:true`:

```bash
go run ./examples/durable-entities -entity customer:456 -inventory
```

Logical accounting follows verified object references in pages of at most 32
nodes. A separate flat LIST observation reports current-key bytes, including
metadata and orphans, when the provider supplies sizes. It spans multiple pages,
is not transactional and excludes noncurrent versions/backups. Inventory needs
GET/conditional PUT; LIST is optional for the current-key observation. Progress
lives in the bucket and survives restarting the command.

To set an explicit operator byte cap:

```bash
go run ./examples/durable-entities -entity customer:456 -set-storage-limit '<bytes>'
```

Use `-set-storage-limit 0` to explicitly remove a persisted cap. For runtime
entities supply the verified account and app UUIDs, immutable `-environment-id`,
optional `-tenant-id`, namespace and entity key on every operator command.
The demo defaults describe a separate harness identity.

Apid optionally accepts `FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES=<positive bytes>`.
It has no default plan quota or price. Ordinary acquisition can only tighten an
existing persisted cap. Removing the setting or starting an uncapped replica does
not remove a cap; use the explicit operator command and align replica settings.
New transitions over the cap return HTTP 409 `durable_entity_storage_limit` with
limit/observed bytes. Existing receipt replays continue at capacity. Rejected
transitions do not change state or alarms, although pure handler computation may
have run. Shrinking business state can reduce committed bytes; cleanup does not
remove retained receipts to free quota. The cap does not bound provider physical
storage, historical versions or temporary uncommitted uploads.

Nonempty legacy entities establish accounting through bounded inventory first.
With a cap they return 503 `durable_entity_inventory_pending` for new work until
the logical proof completes; reads and original receipt replays remain available.
The separately enabled maintenance worker alternates cleanup and inventory pages
and gives legacy accounting priority. For manual migration rerun `-inventory`.
Stop all old entity callers, alarm and maintenance workers before this schema-5
upgrade; binaries supporting only manifest schemas 1/2/3/4 reject new manifests.
A downgrade requires storage migration. Guest protocol version 1 is unchanged.

## Internal outgoing-intent contract

The private Go state engine can append `Transition.Outbox` webhook intents and
restore them using `Manager.PendingOutbox` for one exact entity scope. Messages
commit with state/receipts, retain stable IDs across replay/restart, and survive
ordinary transitions and cleanup. Pending bytes count in snapshot/storage caps.
Limits are 16 messages per transition, 128 pending, 64 KiB per payload and
256 KiB of encoded pending messages; the 1 MiB snapshot ceiling also applies.
Queue/cap rejection publishes neither new state nor outgoing messages.

This is an engine contract, with no delivery worker, acknowledgement/removal or
customer messaging API. Guest protocol v1 rejects an `outbox` field; handlers
remain pure. Registered-webhook admission and deduplicated relay acceptance
must precede guest enablement. See [ADR-903](../../docs/adr/903-object-storage-entity-outbox-contract.md).

## Deploy the counter invocation preview

Deploy [app/](app/gregale.yaml) as an ordinary Gregale HTTP app. Its Node handler
uses the state supplied in the request; it needs no SQL database, bucket
credentials, Gregale API token, ownership token or persistent customer disk.
The source manifest selects `npm start`, port 8080 and `/healthz`.

Enable `revision_pin_ttl_seconds` on the app using the existing app-update API
(for example `PATCH /v1/apps/counter` with
`{"revision_pin_ttl_seconds":3600}`). Project apps also require a registered
environment. An active project release is selected when present; otherwise the
current live deployment in the chosen environment is pinned. Exact pins prevent
a queued transition from silently moving to different code.

Configure apid with non-secret selectors:

```bash
export FAAS_DURABLE_ENTITIES_ENABLED=1
export FAAS_DURABLE_ENTITY_BACKEND='<configured S3 backend ID>'
export FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT='<placement fingerprint>'
export FAAS_DURABLE_ENTITY_BUCKET='<dedicated private platform bucket>'
export FAAS_DURABLE_ENTITY_APPS='<counter app UUID>'
```

The backend and its credentials come from the existing
`FAAS_OBJECT_STORAGE_CONFIG` registry. Use dedicated platform credentials and
a bucket outside customer bucket/binding management. The S3 placement fingerprint
is the SHA-256 of endpoint, S3 region, namespace, driver and platform region,
joined with NUL bytes; it excludes credentials. Pinning it prevents a registry
placement change from making an existing entity look empty. Changing the bucket
selector also changes the data store and requires an explicit migration; keep
these selectors stable. Never inject this configuration into the counter app.
Apid rejects startup if the selectors, explicit app UUID allowlist or storage
probe are invalid. Stop all entity callers before cleaning up an isolated bucket.

Call `POST /v1/apps/counter/entities/invoke` using the usual account bearer
authentication and deploy-write scope. The body is:

```json
{"namespace":"counters","key":"document:123","request_id":"increment-001","payload":{"delta":1}}
```

Repeating the body returns count 1, version 1 and `replayed:true`; a new request
ID increments again. Optional `environment` selects a registered project
environment. Optional `platform_tenant_id` selects an active customer owned by
the authenticated account. Verified account, app, immutable environment and
customer identity all contribute to isolation. Customer self-service tokens
are not accepted on this preview route; authenticated applications should proxy
it through an authorized backend.

The Go SDK exposes the same operation:

```go
result, err := client.InvokeDurableEntity(ctx, "counter", faas.DurableEntityInvokeRequest{
    Namespace: "counters", Key: "document:123", RequestID: "increment-001",
    Payload: json.RawMessage(`{"delta":1}`),
})
```

Keep the request ID and exact payload on every retry, including 503 uncertain
outcomes and 504 timeouts. `Idempotency-Key` does not identify entity work. Local
calls queue behind the current call. A different apid process receives 503 busy;
it can retry after release or expiry. Calls have a 25-second ceiling; an apid
crash can leave ownership busy for up to five minutes. Entity receipt replay
precedes deployment selection, but current app/customer/environment authorization
is always rechecked. Invocation rows record guest execution in the existing SQL
ledger; entity ownership, state and replay results remain authoritative in S3.

The guest receives protocol version 1, verified entity scope, request ID,
payload, state and selected deployment ID. It returns
`{"data":{"count":1},"result":{"count":1}}` with optional `alarm_at`.
Unknown fields, missing state/result, failed execution and cancelled calls do
not publish state. Alarm delivery is separately enabled as described below.
Handlers must avoid external effects; a response can be computed and discarded.
The JSON envelope does not authenticate ordinary public HTTP callers. Do not
use a posted entity/customer ID to authorize access to external customer data.
The counter computes only from the supplied state/payload and cannot publish
entity state directly.

## Enable scheduled entity wake-ups

With the invocation preview configured, enable:

```bash
export FAAS_DURABLE_ENTITY_ALARMS_ENABLED=1
```

The private platform credentials also need delimiter/flat LIST and DELETE for
index hints. Startup checks listing and deletes only a unique platform probe.
Apid reads up to eight time-ordered index entries per page, and independently
reconciles eight entity directories to recover alarms saved before the upgrade
or whose index publication failed. Each read has a two-second budget, each scan
a twenty-second budget, and each delivery the usual twenty-five-second ceiling.
One alarm runs at a time per apid process, with five seconds between sweeps.
Cursors are disposable and restart independently after listing errors.

Index entries under `gregale/durable-entities/v1/alarm-index/` contain private
entity identity, state version, scheduled time and retry reservation number.
They are advisory: the committed snapshot and fenced manifest remain authority.
A missing/failed hint cannot undo a committed transition; reconciliation repairs
it. Due stale hints are deleted after revalidation. Future entries stop an index
pass without reading their entity state; superseded future hints remain until
their indexed due time. Listing must be lexically ordered (native S3/GCS support
this); discovery still has no production ordering or deadline latency guarantee.

For the counter, send a payload such as
`{"delta":1,"alarm_at":"2026-10-08T12:00:00Z"}` with a new request ID. Choose an
appropriate future UTC deadline. The counter returns count 1 and persists the
deadline; when due, the alarm increments the count and clears its deadline.
An ordinary payload without `alarm_at` preserves the deadline; explicit
`"alarm_at":null` clears it. An ordinary call with `{"delta":0}` can observe the
count, but still commits a transition and consumes a receipt.

The guest receives `event:"alarm"` plus a payload containing `type:"alarm"`,
`scheduled_at` and `state_version`. Caller work receives `event:"invoke"`.
Request IDs beginning `__gregale_alarm/` are reserved. Delivery verifies the
observed version and deadline under ownership. Cancelled/replaced observations
do not reach the guest, and a preserved deadline is rediscovered in newer state.
Handlers return state/result and either clear or rearm `alarm_at`; returning the
same overdue deadline rearms it and can cause another delivery. Failed handlers
keep the original business deadline and retry metadata. Only a successful fenced
publication commits the state, receipt and next deadline together.

Suspended/deleted accounts, disabled/deleted apps, suspended customers and
deleted/recreated environments cannot start new alarm work. Existing alarms stay
in the bucket while held. Alarm receipts use the same immutable receipt index.
Before guest execution, a manifest CAS reserves an attempt and its next retry
time. Backoff is 30 seconds, one minute, two minutes, four minutes, then capped
at five minutes. Five reservations exhaust that state version's alarm. A crash
or lost reservation acknowledgement consumes an attempt if publication succeeded;
busy ownership, stale observations and rejected reservation CAS do not. Admission
holds before reservation consume no attempts. Successful receipt replay is checked
before retry gates, including after a lost final commit acknowledgement.

Exhaustion leaves state and the alarm deadline intact and stops automatic retries.
The last reserved attempt may still be running when inspection reports exhaustion.
Inspect one exact private entity scope with the trusted operator harness:

```bash
go run ./examples/durable-entities -alarm-status \
  -account ACCOUNT_ID -app APP_ID -environment-id ENVIRONMENT_UUID \
  -tenant-id CUSTOMER_UUID -namespace counters -entity customer:456
```

Omit `-tenant-id` only for an entity without customer scope. Output contains the
alarm identity, reservation count, next retry time and `exhausted`, never business
state or provider errors. Inspection does not acquire entity ownership. A deliberate
ordinary transition can clear the alarm or rearm it in a new state version, resetting
the budget. There is no automatic dead-letter replay or public inspection endpoint.
Advisory hint bytes are outside the per-entity committed-state cap; the existing
storage-operation and upload-volume metrics include them. Plan quotas, billing,
index compaction and provider/native qualification remain production work.

## Qualify a live provider

Use either provider's private test bucket and credential configuration above,
then opt in. For GCS, set `GREGALE_ENTITY_PROVIDER=gcs` and ADC; no S3 endpoint,
region or AWS credentials are required:

```bash
GREGALE_ENTITY_QUALIFY=1 go test -v -count=1 -timeout=3m \
  ./pkg/durableentity -run '^TestLiveDurableEntityQualification$'
```

The original `TestLiveS3DurableEntityQualification` entry point remains available
for S3 and skips when GCS is selected. Use an exact test name to run one harness.

The test uses a new `gregale/entity-qualification/<UUID>/` prefix. It probes native
conditional writes, restores across managers, submits concurrent calls, drops an
accepted commit acknowledgement at the platform boundary, rejects an obsolete
owner, and kills a separate owner process before replaying its committed result
through takeover. It also checks private delimiter discovery and committed alarm
replay. It verifies committed/current-key inventory, an explicit cap and receipt
replay at capacity. Its JSON report identifies the provider, target (`live_bucket`
or `wire_fixture`), cleanup qualification, retained prefix, parent-process
GET/conditional PUT/LIST/DELETE attempts and elapsed milliseconds; child requests
add extra cost. Objects are retained so results can be inspected. The test does
not access existing entities.
Add `GREGALE_ENTITY_CLEANUP_QUALIFY=1` to explicitly qualify native flat listing,
deletion and original receipt replay after collection within its isolated prefix.
That opt-in removes unused objects; committed state/receipts and probes remain.

This is provider-backed correctness evidence when run against the selected live
bucket. The CI S3 fixture runs the shared harness with `target:wire_fixture`;
GCS SDK wire tests exercise native paginated inventory, caps, cleanup preserving
original receipts and delimiter-based alarm discovery/delivery/replay. Neither
fixture qualifies a live bucket. The acknowledgement-loss and future-clock
fencing cases use explicit fault injection; they do not simulate every network
partition. Native deployed
counter acceptance, latency distributions, pricing, lifecycle protections and
provider failure testing are still required before production availability.

Once the counter app is deployed and enabled, its SDK acceptance test can run
against the actual API. Configure `GREGALE_API_URL`, `GREGALE_API_TOKEN` (account
credential) and `GREGALE_ENTITY_APP_SLUG` through the normal operator environment,
then run from `sdk/go/`:

```bash
GREGALE_ENTITY_API_QUALIFY=1 go test -v -count=1 -timeout=3m \
  -run '^TestDurableEntityDeployedCounterAcceptance$' .
```

Optional `GREGALE_ENTITY_ENVIRONMENT` and `GREGALE_ENTITY_TENANT_ID` select scope.
The test creates a unique retained entity, checks concurrent updates, replays the
original result after later transitions, rejects changed payloads, and verifies
the final count/version. It prints the retained key and elapsed time. It does
not kill or restart live platform services; that requires the native recovery
acceptance environment. Both live tests skip unless explicitly opted in.

To include scheduled delivery against the deployed counter, also set
`GREGALE_ENTITY_ALARMS_QUALIFY=1` on the SDK acceptance command. This requires
the operator alarm gate and the updated counter handler. It schedules a separate
retained entity and observes its alarm increment, then verifies that the original
caller receipt still replays unchanged. It does not prove native parked-VM wake
or crash recovery; those remain separate runtime acceptance gates.

## Verification

The guarded native e2e runner includes
`TestDurableEntityNativeParkRestoreMetal` in its `wake` phase and full-run
required-test contract. Use the existing dedicated-host procedure in
[native e2e CI](../../docs/ops/e2e-native-ci.md), with an exact committed source
SHA. The runner retains its host marker, exclusive lock, service restoration and
final leakcheck requirements. This test needs no external bucket credentials.

It deploys a static pure counter through imaged and the real scheduler/VM path,
then checks state restoration across two actual parked-VM snapshot wakes and
replacement of its own apid child process. Original receipts replay without
enqueueing another guest invocation or waking a parked VM. Changed payloads
conflict, and a failed guest transition leaves the next committed count/version
unchanged. Only apid receives the private storage fixture settings.

The test's bucket is a separate in-process S3 wire fixture that survives apid
replacement. Its JSON evidence identifies `runtime:native_kvm`,
`provider:s3_wire_fixture`, verified restore wake IDs and
`live_provider_qualified:false`. This proves native runtime integration when
actually run on KVM. It does not prove live-bucket durability, provider costs,
latency targets, alarm wakes or takeover of an in-flight apid owner. Keep the
live-provider and deployed SDK qualifications above as separate release evidence.

Local compilation and fixture checks can run without KVM:

```bash
go test -c -tags=metal -o /tmp/gregale-entities-e2e.test ./cmd/e2e
go test -race -count=1 -run 'TestDurableEntityS3Fixture|TestHarnessSetAPIDEnv' ./pkg/e2etest
go test -race -count=1 ./pkg/e2etest/testdata/helloserver
```

Compilation runs no native test, and a skipped native test is not acceptance.

```bash
go test -race -count=1 ./pkg/durableentity ./pkg/objectstorage ./examples/durable-entities
go test -count=1 ./cmd/apid -run '^TestDurableEntity'
```

Tests cover concurrent transitions, process replacement, ownership takeover
between the final read and publication, renewal during execution, lost commit
responses, uncommitted uploads, identity isolation, corrupt restoration and
legacy migration, more than 1,024 receipts, bounded snapshots, replay after
compaction, cleanup publication races and uncertain barriers/deletions.
S3 and GCS wire tests use the production provider SDKs against local
conditional HTTP fixtures; they do not qualify a live object-storage provider.
