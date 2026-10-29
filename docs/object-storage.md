# Object storage

Store files in private buckets that belong to your app: uploads from your users,
generated reports, build artifacts, media. Your app reaches them through the
Gregale CLI and API, short-lived signed URLs, or any S3 SDK pointed at
`https://s3.gregale.dev`. Buckets are separate from your app's instances, which
stay stateless: a bucket is not a disk mounted into the VM.

> **Preview.** Object storage and every feature on this page are preview
> capabilities, released per account. Ask support to enable them. Behavior and
> limits can change before general availability.

Operators running Gregale themselves should read the
[object storage operator notes](ops/object-storage-operator-notes.md).

## Plans and pricing

Object storage is available on the Hobby, Pro and Scale plans. Each plan
includes an amount of storage, measured as the average bytes you keep over the
billing month:

| Plan | Included storage |
|---|---|
| Hobby | 10 GiB |
| Pro | 50 GiB |
| Scale | 200 GiB |

Storage beyond the included amount costs $0.03 per GiB-month. Downloads
(egress) and requests are not billed. Because storage is averaged, 20 GiB held
for half a month uses 10 GiB of the allowance. See [plans](plans.md).

When your account reaches its spend cap, existing buckets keep working within
your plan's limits. Retained object versions count toward stored bytes until
they are deleted.

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

### Public buckets

A bucket created with `--public --serve-at /assets` serves its objects at
`https://<your-app-host>/assets/<key>` to anyone, without a signed URL, and
without waking your app. Responses are cacheable for five minutes, so a deleted
or replaced object disappears everywhere within that time. Anonymous reads may
use at most 75% of your account's monthly request and egress ceilings, and each
bucket serves at most 100 reads per second (bursts to 200); past either limit,
readers get `429`. The mount path cannot change after creation. Buckets are
private unless created public.

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
has `storage:write`. Send an `Idempotency-Key` header so retries cannot store a
file twice; `GET /uploads/avatar/receipts/{id}` reports an upload's status.

## Use an S3 SDK

Each bucket can have S3 credentials that work with standard S3 tools:

- endpoint: `https://s3.gregale.dev`
- region: `us-east-1`
- path-style addressing: `https://s3.gregale.dev/{bucket}/{key}`
- Signature Version 4 only

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
});
await s3.send(new PutObjectCommand({ Bucket: "assets", Key: "hello.txt", Body: "hello" }));
```

What the endpoint does not support:

- A single request or upload part can be at most 64 MiB; larger objects need a
  multipart upload. SDK upload helpers such as `upload_file` do this for you.
- Creating or deleting buckets, ACLs and SigV4A requests return `NotImplemented`.
  Manage buckets with the CLI or API.
- Browser form uploads (presigned POST) are not supported; use a signed PUT URL.
- Keys containing `/./` or `/../` segments cannot be used.

## Give your app access

`gregale add bucket` binds the bucket to your app. Your app then receives six
environment secrets: `<PREFIX>_ENDPOINT`, `_REGION`, `_BUCKET`,
`_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY` and `_ADDRESSING_STYLE`. The prefix
defaults to `GREGALE_S3_` and the bucket name in capitals, such as
`GREGALE_S3_ASSETS`; set another with `--prefix`. Pass them to your
S3 SDK. The credentials are sealed: they are never printed or returned by the
API.

To keep the binding next to your code, declare it in `gregale.yaml`:

```yaml
buckets:
  - bucket: assets
    scope: production
    permission: read_write
    prefix: GREGALE_S3_ASSETS
```

`gregale deploy` creates or reuses the binding but never creates the bucket, so
create it first with `gregale add bucket`. Rotate or revoke a binding's key with:

```sh
gregale app binding object-storage list my-api assets
gregale app binding object-storage rotate my-api assets BINDING_ID --wait
gregale app binding object-storage revoke my-api assets BINDING_ID
```

Rotation keeps the secret names; running instances move to the new key before
the old one is retired. Your app must be allowed to reach `s3.gregale.dev` under
its egress rules. See [object storage from compute workloads](object-storage-compute.md)
for SDK helpers.

## API keys and bucket grants

Bucket management needs an API key with the `storage:manage` scope. Reading and
writing objects need `storage:read` and `storage:write`, and a key without the
`admin` scope also needs a grant on each bucket it uses:

```http
PUT /v1/apps/{app}/buckets/{bucket-id}/access-grants/{key-id}

{"permission": "read"}
```

`permission` is `read`, `write` or `read_write`. `storage:manage` alone does not
read or write objects.

## Versioning

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

## Lifecycle rules

Lifecycle rules delete objects for you: current objects after a number of days
or on a date, older versions, expired delete markers, and abandoned multipart
uploads. Rules can filter by prefix and tags.

```sh
gregale bucket lifecycle set my-api BUCKET_ID lifecycle.json
gregale bucket lifecycle get my-api BUCKET_ID
```

```json
{"rules": [{"id": "temporary", "status": "Enabled", "filter": {"prefix": "tmp/"}, "abort_incomplete_multipart_days": 7}]}
```

Rules are checked hourly. Storage-class transitions are not supported. S3 SDKs
can use `PutBucketLifecycleConfiguration` instead.

## Object Lock, retention and legal holds

Where your region's storage supports it, Object Lock keeps chosen object versions
from being deleted or overwritten:

- **Retention** protects a version until a date, in `GOVERNANCE` or
  `COMPLIANCE` mode. Active retention cannot be shortened or cleared, and
  `COMPLIANCE` cannot be changed to `GOVERNANCE`.
- **A legal hold** protects a version until you turn it off.

`GET /v1/apps/{app}/buckets/{bucket-id}/object-lock-capabilities` shows what
your bucket supports. Enabling Object Lock on a bucket is permanent. Each
change takes an operation ID you choose, which makes retries safe:

```sh
gregale bucket protection legal-hold my-api BUCKET_ID notes.txt VERSION_ID ON OPERATION_ID
gregale bucket protection retention my-api BUCKET_ID notes.txt VERSION_ID GOVERNANCE 2027-01-01T00:00:00Z OPERATION_ID
```

A bucket or account holding protected versions cannot be deleted until the
protection ends. See the [CLI reference](cli-reference.md#bucket-object-lock)
for the bucket defaults.

## Encryption

Objects are encrypted at rest by the storage provider. You can also choose a
bucket default (`gregale bucket encryption set AES256 my-api BUCKET_ID`) or, where
enabled for your account, your own enrolled keys. See the
[CLI reference](cli-reference.md#bucket-encryption).

## Tags, copies and checksums

- Tag objects with `gregale bucket tags`, or with S3 tagging calls.
- `CopyObject` copies within a bucket. To copy from another of your buckets, give
  the S3 credential a copy-source grant on it (`gregale bucket copy-source`).
- Conditional writes (`If-None-Match: *`, `If-Match`) work on single uploads.
- Where your bucket's storage supports them, multipart uploads can carry CRC32,
  CRC32C, CRC64NVME, SHA1 or SHA256 checksums, validated on every part, and
  completing a multipart upload can be conditional. Otherwise the endpoint
  answers `NotImplemented`.

## Events and notifications

Every confirmed write emits an `object.created` event, and deletions emit
`object.removed`, from the `gregale.storage` source. Subscribe an app to them
with `event_triggers` in its `gregale.yaml` (see
[events](events.md#internal-event-subscriptions)), or send them to a function
or queue with the bucket's notification configuration:

```sh
gregale bucket notifications set my-api BUCKET_ID notifications.json
```

## Browser access (CORS)

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
| Anonymous reads of a public bucket | 75% of the account's monthly request and egress ceilings; 100 per second per bucket |

Your plan and account also cap stored bytes, requests and egress each month.

## Deleting data and accounts

- Deleting an object without versioning removes it. With versioning, it adds a
  delete marker and keeps the previous versions until you delete them.
- A bucket can be deleted only when it is empty, including retained versions.
- When an account is deleted, or after 21 days of non-payment, a 30-day grace
  period starts. During it, and while an account is suspended, no request can
  read or write its buckets. After the grace period, every bucket it owns is
  emptied and deleted, except versions still under Object Lock, which are
  deleted when their protection ends.
- A bucket created for an organization belongs to the organization, not to the
  member who created it.
