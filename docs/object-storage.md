# Object storage

Store files in private buckets that belong to your app: uploads from your users,
generated reports, build artifacts, media. Your app reaches them through the
Gregale CLI and API, short-lived signed URLs, or any S3 SDK pointed at
`https://s3.gregale.dev`. Buckets are separate from your app's instances, which
stay stateless: a bucket is not a disk mounted into the VM.

> **Generally available:** buckets (private and public), uploads and
> downloads, signed URLs and upload routes, S3 credentials and the S3 endpoint,
> multipart uploads, app bindings (including key-scoped ones), and bucket
> notifications.
>
> **Preview, released per account:** versioning, Object Lock, lifecycle rules,
> bucket encryption settings, CORS rules and bucket grants for API keys. The
> sections below mark them. Gregale's storage region does not support
> versioning or Object Lock yet.

## Plans and pricing

Object storage is available on every plan. Each plan includes an amount of
storage, measured as the average bytes you keep over the billing month, and
has a ceiling on what you can store:

| Plan | Included storage | Storage ceiling |
|---|---|---|
| Free | 1 GiB | 1 GiB |
| Hobby | 10 GiB | 20 GiB |
| Pro | 50 GiB | 250 GiB |
| Scale | 200 GiB | 1,000 GiB |

On a paid plan, storage beyond the included amount costs $0.03 per GiB-month,
up to the ceiling. Free storage is never billed: writes stop at 1 GiB.
Downloads (egress) and requests are not billed. Because storage is averaged,
20 GiB held for half a month uses 10 GiB of the allowance. A spend cap lowers
the ceiling to what it pays for, and support can raise a paid account's
ceiling. See [plans](plans.md).

A write past the ceiling is refused with `account_bytes`. While an account
stores more than its ceiling (for example after a downgrade to Free or a lower
spend cap), downloads are refused with `402 account_bytes`; listing, `HEAD` and
`DELETE` keep working so you can delete objects to get back under it.

When your account reaches its spend cap, existing buckets keep working within
your plan's limits. Retained object versions count toward stored bytes until
they are deleted.

## Region and durability

Buckets live in one region, served at `https://s3.gregale.dev` as region
`us-east-1`. Gregale stores objects on OVHcloud Object Storage and relies on
the provider's durability. There is no second region, and Gregale keeps no copy
of your objects outside the provider.

This region does not support versioning, so an overwrite or a delete cannot be
undone. Keep your own copy of data you cannot recreate.

## Create a bucket

Bucket names are unique within an app and an environment scope. Create one and
give an app access in one step:

```sh
gregale add bucket assets --app my-api --env production
gregale bucket list my-api
```

`add bucket` creates the bucket, waits until it is ready, and binds it to the
app with `read_write` access (`--permission read` or `write` to narrow it). It
is idempotent: running it again reuses the bucket and the binding.
`gregale bucket list` shows the bucket IDs that other commands take.
If a create or delete fails, Gregale keeps retrying it. While it does, the
bucket stays `provisioning` or `deleting`, and `bucket list` (and the API's
`retry` field) says why, how many attempts ran and when the next one starts.

With the API, `POST /v1/apps/{app}/buckets` creates a bucket:

```json
{"name": "assets", "scope": "production"}
```

`GET /v1/apps/{app}/buckets` lists your buckets with the limits and regions that
apply to your account.

Delete a bucket once it is empty, including any retained versions:

```sh
gregale bucket delete my-api BUCKET_ID
```

Deleting a non-empty bucket is refused. An app that still has buckets cannot be
deleted; delete its buckets first.

Full environment clones currently copy objects up to 5 GiB each on qualified
S3-compatible backends. A larger selected object is refused during capture with
`object_storage_copy_size_unsupported`, before object copying begins. Multipart
uploads keep their separate total object limit.

### Public buckets

A bucket created with `--public --serve-at /assets` serves its objects at
`https://<your-app-host>/assets/<key>` to anyone, without a signed URL, and
without waking your app. Responses are cacheable for five minutes, so a deleted
or replaced object disappears everywhere within that time. Anonymous reads may
use at most 75% of your account's monthly request and egress ceilings, and each
bucket serves at most 100 reads per second (bursts to 200), and one client
address at most 10 per second per bucket (bursts to 50); past any limit,
readers get `429`. A read of a key that does not exist answers `404` and does
not count against your ceilings. A public URL with a query string is
redirected (`301`) to the same path without it before anything is counted, so
query strings never reach your bucket: put an asset's version in its key, such
as `/assets/app.3f9c2e.js`, not in `?v=`. The mount path cannot change after
creation. Buckets are private unless created public.

A public bucket serves each object with the type it was stored with, on your
app's own hostname, so an HTML or SVG object runs as a page of your app. That
is what hosting a static site needs, but it also means anyone who can write to
the bucket can publish script on your origin. If your users upload files (for
example through upload URLs from `gregale bucket sign`), keep those uploads in a
private bucket and hand out signed download URLs, which always serve HTML and
SVG as attachments.

When public mount paths overlap, the longest matching path serves the request.
For example, `/assets/images/logo.png` uses the bucket mounted at
`/assets/images`, regardless of when the bucket at `/assets` was created.

Conditional public reads return `304` when unchanged and `412` when a
precondition fails. An unsatisfiable range returns `416` with `Content-Range`
when the provider supplies it. These failures do not expose provider error
documents and are not cached.

Resumed public downloads honor `If-Range`: a matching strong ETag or modification
date permits a partial response; a stale validator returns the full current
object. If the provider ignores the condition, the full-read fallback counts as
another request and must fit the remaining read and egress allowances.

Reads of a mount are served before your app's edge rules and access control:
`ip`, `jwt`, `geo`, `limit` and other rules, and the app's public auth mode
(Basic, bearer, IP allowlist), do not apply to them. Put anything that needs
those behind your app instead. SVG and XML objects are served with
`Content-Security-Policy: sandbox`, so opening one directly cannot run script
on your app's hostname.

## Upload and download files

From the CLI:

```sh
gregale bucket upload my-api BUCKET_ID reports/2026-10.pdf ./report.pdf --content-type application/pdf
gregale bucket download my-api BUCKET_ID reports/2026-10.pdf ./report.pdf
```

Large files upload in parts automatically. If an upload is interrupted, rerun it
with `--resume UPLOAD_ID` (printed when it stopped); only the missing parts are
sent. `download` replaces an existing file only with `--force`, and only after
the whole object has arrived. `--if-none-match '*'` creates the object only if
the key is free, and `--if-match '"ETAG"'` replaces it only if it is unchanged.

### Signed URLs

To let a browser or another service move one object without credentials, ask
for a signed URL:

```http
POST /v1/apps/{app}/buckets/{bucket-id}/signed-url

{"method": "PUT", "key": "avatars/42.png", "size_bytes": 18231, "content_type": "image/png", "expires_in": 300}
```

From the CLI:

```sh
gregale bucket sign my-api BUCKET_ID v1/stream/segment.gz --expires 15m
gregale bucket sign my-api BUCKET_ID avatars/42.png --method PUT --size-bytes 18231 --content-type image/png
```

It prints the URL, its headers and a `curl` command to send it with; `--json`
prints the API response.

The response holds a `url`, a `method` and `headers`. Send the request with
exactly those headers and a body of exactly `size_bytes`. Use a plain HTTP
client, never one that adds your Gregale credentials. For a download, request
`{"method": "GET", "key": "avatars/42.png"}`.

- URLs last five minutes by default and at most fifteen.
- A PUT URL writes once: a retry of the same URL after success returns the
  saved result instead of writing again.
- Each URL reserves storage when it is issued, so do not retry the issuing call
  automatically.
- Revoking the API key or its bucket grant stops URLs that have not been used.

For files above the single-request limit, create a multipart upload with
`POST .../buckets/{bucket-id}/multipart-uploads`, sign each part with
`POST .../multipart-uploads/{upload-id}/parts/{part}/signed-url`, then complete
it with the parts' ETags in order. Uploads expire after 24 hours.

### Uploads from your users

An upload route accepts uploads on your app's own hostname and writes them
straight to a bucket, without waking your app:

```http
POST /v1/apps/{app}/upload-routes

{"name": "avatar", "bucket_id": "…", "key_prefix": "avatars", "max_bytes": 5242880, "allowed_content_types": ["image/*"]}
```

Clients then `POST /uploads/avatar` on your app's hostname with an API key that
has `storage:write`. The route itself authorizes writes to its bucket, so the key
needs no bucket grant; each key's uploads land under its own key ID below the
route's prefix. Send an `Idempotency-Key` header so retries cannot store a
file twice; `GET /uploads/avatar/receipts/{id}` reports an upload's status.
Organization-bound keys must match the app's organization, and project-limited
keys can upload and read receipts only for apps in their allowed projects.
Each request is one PUT and must fit the configured single-upload limit,
including when the route's stored byte policy is larger.

Active content (HTML, SVG, XML, XSL and JavaScript types) is accepted only when
the route lists the exact type: an empty list or a wildcard such as `image/*`
never admits it, so `image/*` does not admit `image/svg+xml`. A route on a
public bucket mount cannot list an active type at all, and such uploads are
refused with `415`, because the file would be served on your app's hostname.

Objects read through `s3.gregale.dev` never run as pages there, because every
customer shares that host. Every response carries
`X-Content-Type-Options: nosniff`, and an object without a type is served as
`application/octet-stream`. An active type keeps its `Content-Type` but is
served as an attachment (keeping a stored filename) with a sandboxing
`Content-Security-Policy`, including when a presigned URL asks for another
`response-content-type`. To host pages, serve a public bucket on your app's own
hostname.

## Use an S3 SDK

Each bucket can have S3 credentials that work with standard S3 tools:

- endpoint: `https://s3.gregale.dev`
- region: `us-east-1`
- path-style addressing: `https://s3.gregale.dev/{bucket}/{key}`
- Signature Version 4 only

Presigned PUT URLs from S3 SDKs can carry object metadata, tags and checksums in
their signed query parameters. Gregale preserves that metadata and validates
the checksum before writing; conflicting or duplicate values are rejected.

Create credentials for one bucket with
`POST /v1/apps/{app}/buckets/{bucket-id}/s3-credentials` and a body such as
`{"label": "laptop", "permission": "read_write"}`. The secret is shown once.
List credentials with `GET` on the same path and revoke one with
`DELETE .../s3-credentials/{credential-id}`; revocation takes effect on the next
request.

AWS CLI:

```sh
aws configure set profile.gregale.region us-east-1
aws configure set profile.gregale.s3.addressing_style path
aws --profile gregale --endpoint-url https://s3.gregale.dev s3 ls s3://assets/
```

Python (boto3). Set `signature_version` explicitly, or presigned URLs fail with
`403 SignatureDoesNotMatch`:

```python
import boto3
from botocore.config import Config

s3 = boto3.client(
    "s3",
    endpoint_url="https://s3.gregale.dev",
    region_name="us-east-1",
    config=Config(signature_version="s3v4", s3={"addressing_style": "path"}),
)
s3.upload_file("report.pdf", "assets", "reports/2026-10.pdf")
```

JavaScript (AWS SDK v3):

```js
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "https://s3.gregale.dev",
  region: "us-east-1",
  forcePathStyle: true,
  // Required for presigned URLs that browsers upload to (see below).
  requestChecksumCalculation: "WHEN_REQUIRED",
  responseChecksumValidation: "WHEN_REQUIRED",
});
await s3.send(new PutObjectCommand({ Bucket: "assets", Key: "hello.txt", Body: "hello" }));
```

Two settings matter with AWS SDK v3 (and boto3 1.36 or later):

- `forcePathStyle: true`. Without it the SDK sends requests to
  `assets.s3.gregale.dev`, which does not exist.
- `requestChecksumCalculation: "WHEN_REQUIRED"` (boto3:
  `Config(request_checksum_calculation="when_required")`). Newer SDKs sign a
  CRC32 checksum into every presigned PUT and upload-part URL. When you
  presign before you have the file, that is the checksum of an empty body.
  Gregale validates signed checksums, so the browser's upload of the real
  file is then refused. Direct SDK uploads work either way.

What the endpoint does not support:

- A single request or upload part can be at most 64 MiB; larger objects need a
  multipart upload. SDK upload helpers such as `upload_file` do this for you.
- Creating or deleting buckets, ACLs and SigV4A requests return `NotImplemented`.
  Manage buckets with the CLI or API. An upload that sends `x-amz-acl: private`
  or `bucket-owner-full-control`, as many upload libraries do by default, is
  accepted: objects are always private to the bucket's credentials.
- Browser form uploads (presigned POST) are not supported; use a signed PUT URL.
- Keys containing `/./` or `/../` segments cannot be used.

The endpoint runs a few of an account's uploads and upload parts at a time.
Others wait up to 15 seconds for a turn, then answer `503 SlowDown`. S3
transfer managers send several parts at once (the AWS CLI and boto3 send 10),
so set yours to 4: `aws configure set default.s3.max_concurrent_requests 4`,
`TransferConfig(max_concurrency=4)` in boto3, or `queueSize: 4` (the default)
for `Upload` in `@aws-sdk/lib-storage`.

## Give your app access

`gregale add bucket` binds the bucket to your app. Your app then receives six
environment secrets: `<PREFIX>_ENDPOINT`, `_REGION`, `_BUCKET`,
`_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY` and `_ADDRESSING_STYLE`. The prefix
defaults to `GREGALE_S3_` and the bucket name in capitals, such as
`GREGALE_S3_ASSETS`; set another with `--prefix`. Pass them to your
S3 SDK. The credentials are sealed: they are never printed or returned by the
API. These six values, like a managed PostgreSQL `DATABASE_URL`, do not count
toward your plan's per-app secret limit, which covers only the secrets you set.

To keep the binding next to your code, declare it in `gregale.yaml`:

```yaml
buckets:
  - bucket: assets
    scope: production
    permission: read_write
    prefix: GREGALE_S3_ASSETS
```

`gregale deploy` creates or reuses the binding. It creates the bucket only when
the entry says `create: true` (or `create: {region: …}`) and no bucket with that
name exists; otherwise create it first with `gregale add bucket`. A deploy never
changes or deletes an existing bucket. To run your app when files land in the
bucket, add `triggers` to the same entry ([run code on upload](#run-code-on-upload)).
Rotate or revoke a binding's key with:

```sh
gregale app binding object-storage list my-api assets
gregale app binding object-storage rotate my-api assets BINDING_ID --wait
gregale app binding object-storage revoke my-api assets BINDING_ID
```

Rotation keeps the secret names; running instances move to the new key before
the old one is retired. Your app must be allowed to reach `s3.gregale.dev` under
its egress rules. See [object storage from compute workloads](object-storage-compute.md)
for SDK helpers.

### Limit a binding to part of a bucket

A binding can be limited to object keys under one prefix. Give each service
only the keys it needs, with its own secret-name prefix:

```yaml
buckets:
  - bucket: segments
    app: store
    permission: write
    key_prefix: v1/L0/
    prefix: GREGALE_S3_L0
  - bucket: segments
    app: query
    permission: read
    key_prefix: v1/
  - bucket: segments
    app: compactor
    permission: read_write
    key_prefix: v1/
```

Or `gregale add bucket segments --app store --permission write --key-prefix v1/L0/ --prefix GREGALE_S3_L0`.

- The prefix is literal and ends with `/`: `v1/L0/` matches `v1/L0/a.gz` but
  not `v1/L0x/a.gz`, and `tenant1` is refused so it cannot reach `tenant10/`.
  Patterns such as `v1/*/L0/` are refused.
- Object reads, writes, deletes and multipart uploads work only on keys under
  it. A copy's source must be in the same bucket and under it too.
- Listings must pass a `prefix` under it. `DeleteObjects` fails with 403 if any
  key is outside it, and then deletes nothing.
- Bucket-wide requests (versioning, lifecycle, notifications, encryption,
  Object Lock, listing every bucket) are refused.
- The key keeps its prefix through rotation and environment clones.
- Until key-scoped bindings are released to your account, a binding with
  `key_prefix` is refused with `403 object_storage_key_scopes_unavailable`.

## API keys and bucket grants

Bucket management needs an API key with the `storage:manage` scope. Reading and
writing objects need `storage:read` and `storage:write`, and a key without the
`admin` scope also needs a grant on each bucket it uses. Bucket grants are in
preview: until they are released to your account, use an `admin` key, S3
credentials or an app binding.

```http
PUT /v1/apps/{app}/buckets/{bucket-id}/access-grants/{key-id}

{"permission": "read"}
```

`permission` is `read`, `write` or `read_write`. `storage:manage` alone does not
read or write objects.

## Versioning (preview)

Versioning is in preview, and Gregale's storage region does not support it yet.

With versioning on, overwriting or deleting an object keeps the previous
version, and you can read or restore it:

```sh
gregale bucket versioning enable my-api BUCKET_ID
gregale bucket versioning status my-api BUCKET_ID
gregale bucket download my-api BUCKET_ID notes.txt ./old.txt --version-id VERSION_ID
```

Turning versioning on takes at least fifteen minutes; writes to the bucket pause
until it is ready. Suspending it keeps the versions you have. Retained versions
count toward your stored bytes. S3 SDKs can also list versions
(`ListObjectVersions`) and read or delete a specific version.

## Lifecycle rules (preview)

Lifecycle rules are in preview.

Lifecycle rules delete objects for you: current objects after a number of days
or on a date, older versions, expired delete markers, and abandoned multipart
uploads. Rules can filter by prefix and tags.

```sh
gregale bucket lifecycle set my-api BUCKET_ID lifecycle.json
gregale bucket lifecycle get my-api BUCKET_ID
```

```json
{"rules": [
  {"id": "temporary", "status": "Enabled", "filter": {"prefix": "tmp/"},
   "expiration": {"days": 30}, "abort_incomplete_multipart_days": 7},
  {"id": "old-versions", "status": "Enabled", "filter": {},
   "noncurrent_version_expiration": {"noncurrent_days": 90}}
]}
```

`expiration.days` removes current objects that many days after upload, and
`noncurrent_version_expiration.noncurrent_days` removes older versions once
they have been noncurrent that long.

Rules are checked hourly. Storage-class transitions are not supported. S3 SDKs
can use `PutBucketLifecycleConfiguration` instead.
On GCS, older-version age starts when the generation becomes noncurrent.
Older-version rules continue to apply after the last live object is deleted.
Current-object age starts when the upload completes; changing tags does not
restart its expiration clock.

## Object Lock, retention and legal holds (preview)

Object Lock is in preview, and Gregale's storage region does not support it yet.

Where your region's storage supports it, Object Lock keeps chosen object versions
from being deleted or overwritten:

- **Retention** protects a version until a date, in `GOVERNANCE` or
  `COMPLIANCE` mode. Active retention cannot be shortened or cleared, and
  `COMPLIANCE` cannot be changed to `GOVERNANCE`.
- **A legal hold** protects a version until you turn it off.

Object Lock is included on paid plans, and retention is capped by plan:

| Plan | Longest retention | Legal holds |
|---|---|---|
| Free | Not included | Not included |
| Hobby | 90 days | Yes |
| Pro | 1 year (366 days) | Yes |
| Scale | 7 years (2,557 days) | Yes |

The cap applies to retention dates, event-hold durations and bucket default
retention, in both modes, measured from the time of the request. A longer one
is refused with `403 object_lock_plan_limit` (S3 SDKs: `AccessDenied`), naming
the limit and the days you asked for. Clearing a default and releasing a hold
always work. Retention set before these caps is kept as it was.

`GET /v1/apps/{app}/buckets/{bucket-id}/object-lock-capabilities` shows what
your bucket supports. Enabling Object Lock on a bucket is permanent, and the
bucket then keeps every object version.

Some storage accepts Object Lock only when a bucket is created
([ADR-20261119065000](adr/20261119065000-object-lock-chosen-at-bucket-creation.md)).
Choose it then with `gregale add bucket NAME --app APP --object-lock` (API:
`"object_lock": true`). Enabling it later on a bucket created without it is
refused with `409 object_lock_requires_new_bucket` (S3 SDKs:
`InvalidBucketState`); create a new bucket with Object Lock and move the
objects there.

Each change takes an operation ID you choose, which makes retries safe:

```sh
gregale bucket protection legal-hold my-api BUCKET_ID notes.txt VERSION_ID ON OPERATION_ID
gregale bucket protection retention my-api BUCKET_ID notes.txt VERSION_ID GOVERNANCE 2027-01-01T00:00:00Z OPERATION_ID
```

A bucket or account holding protected versions cannot be deleted until the
protection ends. Run `gregale bucket object-lock set --help` for the bucket
defaults. (Object Lock is in preview, so the public CLI reference does not
list its commands.)

## Encryption

Objects are encrypted at rest by the storage provider. Bucket encryption
settings are in preview. Where they are released to your account, you can also
choose a bucket default (`gregale bucket encryption set AES256 my-api BUCKET_ID`)
or your own enrolled keys. Run `gregale bucket encryption set --help` for the
options.

Public asset GET and HEAD responses also validate provider encryption metadata.
An unknown or another account's KMS key, customer-supplied encryption, or ambiguous
encryption headers returns a temporary-unavailability response before object data
is served.

## Tags, copies and checksums

- Tag objects with `gregale bucket tags`, or with S3 tagging calls.
  On GCS, a concurrent object replacement or metadata change can return a
  conflict; retry the tag operation against the current object. Each GCS tag
  write uses two requests from the native request allowance.
- `CopyObject` copies within a bucket. To copy from another of your buckets, give
  the S3 credential a copy-source grant on it (`gregale bucket copy-source`) and
  name the source by its bucket ID, as `x-amz-copy-source: <source-bucket-id>/<key>`;
  a source named by its bucket name answers `NoSuchKey`. A project-limited API
  key may grant copy access only when both buckets' apps are in its allowed
  projects.
- Conditional writes (`If-None-Match: *`, `If-Match`) work on single uploads.
- Where your bucket's storage supports them, multipart uploads can carry CRC32,
  CRC32C, CRC64NVME, SHA1 or SHA256 checksums, validated on every part, and
  completing a multipart upload can be conditional. Otherwise the endpoint
  answers `NotImplemented`. Recent AWS CLI and SDK releases add a checksum to
  every multipart upload by default; where your storage lacks them, set
  `AWS_REQUEST_CHECKSUM_CALCULATION=when_required` (or
  `request_checksum_calculation = when_required` in `~/.aws/config`).

## Events and notifications

Every confirmed write emits an `object.created` event, and deletions emit
`object.removed`, from the `gregale.storage` source. That includes writes
through signed URLs, upload routes, multipart uploads and S3 SDKs. There are
three ways to act on them:

- **Run your app on upload:** `triggers` on a `gregale.yaml` bucket entry
  (below). Simplest; calls the app that owns the bucket.
- **Send to a queue, or to another app:** the bucket's notification rules.
- **Subscribe through the event router (preview):** `event_triggers` in
  `gregale.yaml` (see [events](events.md#internal-event-subscriptions)).

### Run code on upload

Declare the trigger next to the bucket binding. On each deploy Gregale makes
the bucket's notification rules match the list:

```yaml
buckets:
  - bucket: uploads
    permission: read_write
    triggers:
      - id: new-image          # stable name; renaming it replaces the rule
        prefix: incoming/      # optional
        suffix: .png           # optional
```

Each matching write sends one `POST /` to your app, as an asynchronous
invocation, with an S3-style body:

```json
{"Records": [{"eventVersion": "2.1", "eventSource": "aws:s3", "awsRegion": "…",
  "eventTime": "…", "eventName": "ObjectCreated:Put",
  "s3": {"configurationId": "…", "bucket": {"name": "uploads", "arn": "…"},
         "object": {"key": "incoming%2Fcat.png", "size": 18231, "eTag": "…"}}}]}
```

- The object key is URL-encoded, as in AWS S3 events; decode it before use.
- `events` defaults to every `ObjectCreated` event. Triggers do not fire on
  deletes; use a notification rule for those.
- Delivery is at least once. The `x-gregale-event-id` header stays the same
  across retries, so record it before applying side effects. Failed deliveries
  follow the async retry policy and end in the app's dead-letter queue
  (`gregale dlq list my-api`).
- The trigger always calls the app that owns the bucket. Leaving out `triggers`
  keeps the current rules; `triggers: []` removes the ones the manifest created.
  Rules you set by hand with `gregale bucket notifications` are kept, and a
  trigger whose `id` or filter clashes with one fails the deploy instead of
  overwriting it.
- `gregale dev` accepts the same `triggers` on `dev.buckets`; they call your dev
  workspace, not production.

**Browser uploads without a "complete" call.** Your backend issues a signed PUT
URL ([signed URLs](#signed-urls)) and returns it to the browser; the browser
uploads straight to `s3.gregale.dev`; the trigger then runs your processing
code. The browser never needs to call back to say the upload finished, and
your app sleeps while the upload is in flight.

### Notification rules: queues and other apps

To put work on a queue instead of calling the app directly, or to reach an app
other than the bucket's owner, set the bucket's notification rules:

```sh
gregale bucket notifications set my-api BUCKET_ID notifications.json
gregale bucket notifications get my-api BUCKET_ID
```

```json
{"rules": [{"id": "thumbnails", "events": ["s3:ObjectCreated:*"], "prefix": "incoming/",
  "destination": "arn:gregale:sqs:REGION:ACCOUNT_ID:APP_ID/thumbnail-jobs"}]}
```

A function destination is `arn:gregale:lambda:REGION:ACCOUNT_ID:function:APP_ID`.
`REGION` is the bucket's region (`gregale bucket list`), `ACCOUNT_ID` is the
`id` from `gregale whoami --json`, and `APP_ID` is the `id` from
`gregale app get APP --json`. Queue messages carry the same body as above.
Rules support `ObjectCreated:Put`, `:Copy`, `:CompleteMultipartUpload`,
`ObjectRemoved:Delete`, `:DeleteMarkerCreated` and the lifecycle expiration
events. Notification rules are not declared in `gregale.yaml` yet.

Notifications and compute triggers run app code, so they need a paid plan;
Free answers `402`, like other triggers. Company apps use the organization's
plan, including its queue depth limit, after resource ownership is adopted.
Queue destinations must have a unique enabled queue name across the app's
environments. Notifications enter the captured queue's environment and retain
that destination if the queue is renamed.

## Uploads from your own sites (preview)

Cross-origin bucket rules are in preview.

To let your own sites use signed URLs and upload routes from the browser, set
CORS rules on the bucket:

```sh
gregale bucket cors set my-api BUCKET_ID cors.json
gregale bucket cors get my-api BUCKET_ID
gregale bucket cors clear my-api BUCKET_ID
```

```json
{"rules": [{"allowed_origins": ["https://app.example.com"], "allowed_methods": ["PUT", "GET"], "allowed_headers": ["content-type"], "expose_headers": ["ETag"], "max_age_seconds": 3600}]}
```

A bucket holds up to 100 rules; the first rule matching the origin, method and
headers applies. An origin is `*` or an `http(s)` origin with at most one `*`,
such as `https://*.example.com`. CORS decides which pages may read responses; it
is not authorization, and every request still needs a signature or grant.

## Usage

```sh
gregale bucket usage
```

shows stored bytes, requests and egress for the current month, and marks any
meter that is not available yet as unknown.

## Limits

A request over a limit fails with an error that names the limit, its value and
what you used.

| Limit | Value |
|---|---|
| Buckets per app, object size, single upload and part size | `max_buckets_per_app`, `max_upload_bytes`, `max_single_put_bytes` and `max_part_bytes` in `GET /v1/apps/{app}/buckets` |
| Request or upload part through `s3.gregale.dev` | 64 MiB |
| Parts in one multipart upload | 10,000 |
| Unfinished multipart upload | expires after 24 hours |
| Signed URL lifetime | 5 minutes by default, at most 15 |
| Unused signed URLs per bucket | 1,024 |
| S3 credentials per bucket | 10 |
| Copy-source buckets per S3 credential | 32 |
| Lifecycle rules per bucket | 1,000 |
| Notification rules per bucket | 1,000 |
| CORS rules per bucket | 100 |
| Anonymous reads of a public bucket | 75% of the account's monthly request and egress ceilings; 100 per second per bucket; 10 per second per client address per bucket; missing keys not counted |
| Requests through `s3.gregale.dev` | 100 per second per account, bursts of 200; more answer `503 SlowDown`, which S3 SDKs retry with backoff |
| Requests each month | Free 50,000, Hobby 500,000, Pro 2,500,000, Scale 10,000,000 ([plans](plans.md#object-storage)). At the limit, every request is refused until the month ends |
| Uploads and upload parts in progress through `s3.gregale.dev` | A few per account; more wait up to 15 seconds for a turn, then answer `503 SlowDown` |
| Stored data | Free stops at 1 GiB, Hobby at 20 GiB, Pro at 250 GiB and Scale at 1,000 GiB. A lower spend cap lowers it to the included storage plus what the cap buys at $0.03 per GiB-month; a $0 cap stops at the included storage. Need more? Ask support to raise your account's ceiling; your spend cap still applies. At the ceiling, uploads are refused; while you store more than it, downloads answer `402` until you delete objects. Listing, `HEAD` and deletes keep working, and nothing is deleted for you. A downgrade is refused while your data does not fit the new plan |
| Objects per account, counting retained versions | 1,000,000 on every plan |
| Object Lock retention | Free not included, Hobby 90 days, Pro 366 days, Scale 2,557 days (`object_lock_plan_limit`) |
| Egress each month | 10 times your plan's included storage: Free 10 GiB, Hobby 100 GiB, Pro 500 GiB, Scale 2,000 GiB ([plans](plans.md#object-storage)). Public objects served from the CDN's cache do not count. At the limit, every request except deletes is refused until the month ends |

The platform also caps stored bytes, requests and egress per account; the
current values are in `GET /v1/account/object-storage-usage` under `policy`.

## Deleting data and accounts

- Deleting an object without versioning removes it. With versioning, it adds a
  delete marker and keeps the previous versions until you delete them.
- A bucket can be deleted only when it is empty, including retained versions.
- When an account is deleted, or after 21 days of non-payment, a 30-day grace
  period starts. During it, and while an account is suspended, no request can
  read or write its buckets. After the grace period, every bucket it owns is
  emptied and deleted. Gregale releases legal and event holds and deletes
  `GOVERNANCE` versions too; versions under `COMPLIANCE` retention, which no
  one can remove early, are kept until their retention ends and then deleted.
- A bucket created for an organization belongs to the organization, not to the
  member who created it.
