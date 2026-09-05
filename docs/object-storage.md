# Customer object storage preview

Gregale can manage private S3-backed object buckets without operating storage
nodes. Compute remains stateless: these are not VM volumes. The dashboard's
Storage page keeps object buckets separate from snapshot/image-layer usage.
Architecture and launch boundaries: [ADR-151](adr/151-provider-neutral-object-storage.md).

## Enable a qualified backend

1. Apply database migrations through the normal Gregale deployment process.
2. Copy [the example](../deploy/object-storage.example.json) to an operator-owned
   config path, e.g. `/etc/faas/object-storage.json`. Set the real namespace,
   endpoint, region, and exact browser origins. Use a dedicated upstream
   account/project, not one containing unrelated infrastructure buckets.
3. Supply the named access/secret environment variables to **apid only**, through
   the deployment's secret mechanism. Never put values in JSON, app envs, source
   control, URLs, or logs. Optional `session_token_env` supports temporary
   upstream credentials; restart/rotate before their expiration.
4. Set `FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage.json` for apid and
   restart all replicas with identical settings. Missing config disables the
   feature. Invalid config or missing credentials fails startup. There is no
   request-time config reload or credential refresh process.
5. Run the qualification checks below before admitting customers.

The identity needs bucket creation/deletion and CORS configuration plus object
list/get/put/delete for Gregale buckets. Restrict it to `gregale-*` where supported;
otherwise isolate the upstream project. Enable provider/account public-access
blocking where available. The driver creates buckets without public ACLs, but
does not manage provider-specific account policies, encryption keys, residency
controls, retention, or replication. Keep versioning and object lock off for
this preview; the UI does not manage historical versions or retention locks.

Bucket names in Gregale are logical and app/scope-local. Physical names are
UUID-based to avoid leaking customer identifiers or colliding across providers.
Only configured region defaults appear in the creation catalog.

## Provider configuration

All entries use `driver: "s3"` when the backend meets the contract. `region` is
Gregale's product region; `s3_region` is only an upstream signing/location
setting. A matching name is not evidence of physical colocation.

| Backend | Endpoint / signing region | Qualification notes |
| --- | --- | --- |
| OVH US Virginia | `https://s3.us-east-va.io.cloud.ovh.us`, `us-east-va` | Example targets the US S3 service, not the legacy Swift endpoint. Verify project availability and selected storage class. |
| AWS S3 Northern Virginia | `https://s3.us-east-1.amazonaws.com`, `us-east-1` | The driver omits CreateBucket LocationConstraint in this region. Use dedicated IAM permissions and account-level public-access blocking. |
| Cloudflare R2 | `https://<account-id>.r2.cloudflarestorage.com`, `auto` | Set `path_style: true`; account token must permit bucket management. R2's placement is not an AWS-style us-east-1 residency guarantee. |
| Your Ceph RGW / other S3 cluster | Your externally reachable HTTPS endpoint and configured zonegroup region | Set `path_style` to match the deployment. Verify TLS, privacy, CORS, signatures and failure behavior. Gregale does not provision the cluster. |

The example endpoint follows [OVH's endpoint guide](https://support.us.ovhcloud.com/hc/en-us/articles/10667991081107-Object-Storage-Endpoints-and-geoavailability).
R2 supports this subset, including CreateBucket, PutBucketCors, ListObjectsV2 and
PutObject Content-MD5, with `auto` as its signing region; see
[R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).
These are configuration targets, not a claim of completed live-provider testing.
Other providers may require a factory with different bucket provisioning/CORS
operations while reusing the S3 data driver. Do not equate “S3-compatible” with
identical IAM, retention or billing APIs.

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
Gregale sessions/API keys and MFA policy. Read operations require `apps:read` or
`admin`; mutations require `deploy:write` or `admin`.

- `GET /`: bucket list and configured limits/regions, across environment scopes.
- `POST /`: `{ "name": "assets", "scope": "default", "region": "us-east-1" }`.
  Omit scope/region for defaults. Retry the same name/scope after failed setup.
- `DELETE /{bucket-id}`: empty-only deletion. Nonempty returns 409; success 204;
  already deleted returns 404. Delete all buckets before deleting the app.
- `GET /{bucket-id}/objects?prefix=folder%2F&limit=100&cursor=...`: one page;
  pass `next_cursor` without interpreting it, keeping the same prefix.
- `DELETE /{bucket-id}/objects?key=...`: URL-encode the entire exact key.
- `POST /{bucket-id}/signed-url`: `{ "method": "PUT", "key": "hello.txt",
  "size_bytes": 5, "content_type": "text/plain", "expires_in": 300 }`.
  Use returned `url`, `method`, `headers` with the exact five-byte body. For
  download request `{ "method": "GET", "key": "hello.txt" }` (read scope).

Use ordinary fetch/HTTP for signed URLs, **not** the authenticated Gregale client.
Never forward Gregale Authorization/cookies. Browsers set Content-Length from
the File body; preserve other returned headers, including Content-MD5 for empty
uploads. URLs default to five minutes and allow at most fifteen; they may be
reused until expiry and PUT replaces an existing key. Do not log or persist them.
Changing app permissions does not revoke previously issued URLs.

## Switch providers without rewriting the product

Add a second backend with a new immutable `id` and `namespace`, then change
`defaults.us-east-1` to that ID. New buckets use it. Existing buckets retain
their recorded backend; keep its configuration and credentials available.
Endpoint, Gregale/signing region, driver or namespace changes fence existing buckets
with 503 instead of redirecting them. Rotate keys within the same namespace by
changing secret values and restarting, not by changing the backend identity.

There is intentionally **no automatic existing-bucket migration**. A future
migration worker must stop new writes/signing, wait out issued URLs, copy and
verify objects/metadata, explicitly update placement, and retain rollback data.
The registry and durable placement remove the API/UI rewrite, not the cost or
operational risk of moving bytes. Native customer S3 keys require a separate
tenant IAM adapter; never hand out the operator-wide credential.

## Qualification and launch checklist

- Create a bucket; retry creation and verify only one upstream bucket exists.
- Check unauthenticated list/get/put are denied. Check another Gregale account
  cannot list, sign, delete, or guess access to this bucket.
- Upload/download a Unicode/spaced key, an empty object, and a file near the
  configured limit from the actual console origin. Verify exact contents.
- Tamper with a nonempty signed upload's Content-Length and an empty upload's
  body: both must fail. Verify wrong key/method and expired URLs fail.
- Test CORS preflight, pagination, deletion of objects, nonempty bucket rejection,
  empty deletion, upstream outage, and retry after apid interruption.
- Add a second backend and switch the default. Old/new buckets must use their
  respective backends; removing the old config must fail closed.
- Keep operator monitoring/budgets in place. Defaults are 10 buckets/app and
  100 MiB/upload, configurable up to 100 and 5 GiB. These do **not** cap total
  bytes, request count, egress or customer spend. Presign counts cannot meter
  actual usage. No object-storage prices, allowances or invoice lines ship here.
- Before paid/general availability, implement durable usage reconciliation,
  pricing/margin policy, spend/abuse controls, tenant S3 keys if needed, and a
  coordinated account-deletion workflow. Active buckets block account
  hard-deletion; confirmed-deleted bucket metadata is purged with the account.
  Do not bypass these guards and orphan customer data.

Deferred: native S3 credentials/endpoint, multipart uploads, public hosting,
lifecycle/version management, bucket-wide byte quotas and automatic migrations.
