# ADR-20261106114500: Key-scoped bucket bindings and `gregale bucket sign`

- **Status:** accepted
- **Date:** 2026-11-06
- **Owner:** object storage maintainers (apid, s3-gatewayd)
- **Context:**
  - A compute binding gives an app S3 credentials for a whole bucket, at `read`, `write` or `read_write`. Multi-service apps want least privilege inside one bucket. For example, an ingest service writes `segments/L0/`, a query service only reads `segments/`, and a compactor writes `segments/L1/` and deletes from `segments/L0/`.
  - Signed URLs already exist (`POST /v1/apps/{app}/buckets/{bucket}/signed-url`), but the CLI has no command for them, so scripts reach for an S3 SDK.
  - While reading the gateway: a compute binding's previous key during rotation lives in a rotation stage row without the binding's app ID. `can(bucketSettingsPermission)` treated it as a customer-created `read_write` key, so during the overlap it could change bucket-wide settings (object lock, lifecycle, notifications, encryption, versioning).
- **Decision:**
  1. **`key_prefix` on compute bindings.** `POST …/compute-bindings` takes an optional `key_prefix`.
     - It is a literal prefix of 2 to 512 bytes (`api.MaxObjectKeyScopeBytes`, in `limits.go`) that ends with `/` and is a valid object key. The trailing `/` keeps `tenant1/` from reaching `tenant10/`. Pattern characters (`*`, `?`, `[`) are refused, so `v1/*/L0/` is an error rather than a prefix that matches nothing.
     - One validator, `api.ValidObjectKeyScope`, serves the API and `gregale.yaml` validation.
     - The credential row records it in a new nullable column, `object_storage_s3_credentials.key_prefix`. The binding view shows it.
     - A CHECK (added NOT VALID, so it checks new writes without scanning; a later migration validates it) bounds the column the same way. It also allows the column only on a compute binding or its rotation stage, never on a signed-URL credential.
     - `gregale add bucket --key-prefix` and `key_prefix` under `buckets:` in `gregale.yaml` set it, on both the CLI path and the server-side apply.
     - An existing binding with a different key prefix is an error, not a silent reuse. The binding idempotency keys include the prefix, so re-creating a binding with another prefix never replays the old response.
     - For different access to different prefixes, an app takes several bindings with different environment-variable prefixes.
  2. **The gateway enforces it before any route.** A key-scoped credential may make only these requests:
     - an object request whose key is under the prefix; for a copy (including UploadPartCopy), the source must be in the same bucket and under the prefix too;
     - ListObjects (v1 and v2), ListObjectVersions and ListMultipartUploads with exactly one `prefix` parameter that is under the prefix;
     - DeleteObjects, where every key in the body must be under the prefix, or nothing is deleted (403);
     - HeadBucket and GetBucketLocation.

     Everything else is refused with 403 `AccessDenied`: bucket-wide settings, unscoped listings, ListBuckets, and other buckets. A key-scoped key never holds bucket settings.
  3. **Every copy of the credential keeps the prefix.** This covers the rotation stage, which keeps the previous key valid during a rolling refresh. It also covers the environment-clone capture (SQL and memory), checkpoint credential slots, clone and shared-binding targets, and the clone equality check. A copy path that dropped the prefix would widen access, so each one is covered by a test.
  4. **Rollout gate.** An older gateway ignores `key_prefix` and would grant the whole bucket.
     - apid therefore issues key-scoped bindings only to accounts that the `object-storage-key-scopes` capability (preview) is released to. Before that it answers `403 object_storage_key_scopes_unavailable`.
     - Operators release it only after every s3-gatewayd runs this change.
     - A host that cannot decide capabilities refuses as well.
  5. **Rotation stages lose bucket settings.** `can(bucketSettingsPermission)` also requires no `rotation_parent_id` and no `key_prefix`.
  6. **`gregale bucket sign <app> <bucket-id> <key> [--method GET|PUT] [--expires D] [--size-bytes N] [--content-type T]`** calls the signed-URL API.
     - It prints the URL, its headers and a `curl` line to send it with.
     - PUT requires `--size-bytes`, and `--expires` is at most 15 minutes, matching the API.
     - Signed URLs keep their own model (single object, writes once, reserved storage). They are not tied to bindings.
- **Why:**
  - A literal prefix covers the layouts people use (`tenant/`, `segments/L0/`) and is cheap to check exactly. Glob or IAM-style policies would need a policy language and evaluation the gateway does not have.
  - The allowlist fails closed: an S3 operation added later is refused for scoped keys until it is reviewed.
  - Refusing the whole bulk delete is simpler than per-key errors, and it never deletes part of a batch that the caller did not expect to be cut short.
- **Consequences:**
  - One expand migration (a nullable column, no rows written), plus a registry entry for environment clones.
  - Scoped keys cannot use bucket-level reads such as `GetBucketVersioning`. SDKs that probe them must be configured not to.
  - Customer-created S3 keys stay whole-bucket. Scoping them is a follow-up if asked for.
  - Not done here: separate `delete` permission, several prefixes per binding, and scoped signed URLs.
