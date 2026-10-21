# ADR: Data API file storage

Date: 2026-10-21
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR 20261018170742](20261018170742-data-api-access-modes-and-clients.md), [ADR-1035](1035-developer-managed-object-storage.md), [ADR-1190](1190-application-auth.md), [ADR-20261016151947](20261016151947-live-queries-incremental-engine.md)

## Context

Applications built on a Data API need files as well as rows: avatars,
attachments, exports. Managed object storage (ADR-1035) serves compute, through
bucket-scoped credentials bound to an app. A browser cannot use those
credentials, and managed buckets accept CORS only from origins the platform
configures, so a browser cannot upload to them directly either.

Application Auth (ADR-1190) signs storage URLs for its own users, using
`authorize()` roles and organization-scoped keys. A Data API, though, accepts
tokens from any issuer, and its owner writes access rules as row-level
security in their own database. Files should follow the same rules, decided by
the same engine, so that "users see their own files" is one policy.

## Decision

### Files are rows

- The owner adds a table `storage_objects` to the Data API's first exposed
  schema (normally `api`), with a migration that
  `gregale data-api storage sql` prints. Its columns are `bucket`, `name`,
  `owner`, `size`, `content_type`, `created_at` and `updated_at`, with key
  `(bucket, name)`. RLS is enabled on it.
- **Policies decide access.** The owner writes policies on it like on any
  table: SELECT governs downloads, INSERT and UPDATE govern uploads (`WITH
  CHECK` can bound `size` or `content_type`), and DELETE governs removal.
  `owner` defaults to the caller's `sub`. A guard trigger keeps `owner` and
  `created_at` on update.
- **Writes only through storage endpoints.** The guard trigger
  `storage_objects_guard` refuses every insert, update and delete unless the
  request's claims carry `gregale_storage: "write"`.
  - That claim is gateway-owned. The runtime removes it from every incoming
    token, as it does `gregale_access`, and adds it only to its own storage
    calls.
  - So a REST write cannot make the table disagree with the bucket, while
    REST reads, generated types and live reads of the table work as for any
    table. A file list is a query on `storage_objects`.
- **Startup check.** The runtime refuses to start with storage on unless the
  table exists in the first schema with RLS enabled and the enabled guard
  trigger fires before each row insert, update and delete, with the claim
  check in its body. The failure is reported as `data_api_storage_unsafe`.

### Buckets

- `gregale data-api storage enable NAME --bucket BUCKET` provisions the bucket
  if needed and binds it to the Data API app with read and write access, as
  `gregale add bucket` does. The binding arrives as `GREGALE_S3_<BUCKET>_*`
  secrets, the same shape Application Auth uses. The command then sets
  `DATA_API_STORAGE=on`.
- With storage on, every bucket bound to the app is a storage bucket, named by
  its binding. The credentials never leave the runtime.

### Endpoints

All routes are under `/storage/v1`, on the Data API's own origin and CORS
policy. They take the same credentials as REST: a user token, a server key, or
no token for anonymous reads.

| Route | Authorization (as the caller, through PostgREST) | Then |
|---|---|---|
| `PUT /object/{bucket}/{name}` | upsert the row with `Prefer: tx=rollback` | stream the body to the bucket, then upsert for real |
| `GET`/`HEAD /object/{bucket}/{name}` | read the row | stream the object, passing `Range` through |
| `POST /sign/{bucket}/{name}` | read the row | return a SigV4 GET URL valid for 60 seconds |
| `DELETE /object/{bucket}/{name}` | delete the row with `Prefer: tx=rollback` | delete the object, then the row |

- **Errors.** A refused write answers `403 storage_forbidden`. A row the caller
  may not see answers `404 storage_not_found`, the same as a missing one. A
  dry run that succeeds but whose commit fails (for example, a policy changed
  meanwhile) answers `503 storage_commit_failed` and leaves the object
  unreachable through the Data API.
- **Signed URLs.** They point at the bucket's endpoint and need no CORS, so
  they serve `<img>`, `<video>` and download links.
- **Uploads.**
  - Uploads need `Content-Length` and at most
    `DATA_API_STORAGE_MAX_OBJECT_BYTES`: 25 MiB by default, 100 MiB at most
    (`DataAPIStorageObject*Bytes`).
  - The body is streamed with a signed `content-length`, so the bucket stores
    exactly the size the policy approved.
- **Downloads.** They carry `Content-Security-Policy: sandbox` and
  `X-Content-Type-Options: nosniff`, so an uploaded HTML file cannot run as
  the Data API's origin.
- **Who may call what.**
  - Anonymous downloads and signing need `storage_objects` in the public-read
    allowlist; RLS still decides which rows. Anonymous uploads and deletes are
    refused.
  - A server key restricted to relations must list `storage_objects`, and a
    read-only key may only download.
  - The Data API's read-only mode refuses uploads and deletes.

### Resources

- **Admission.** Authorization queries run inside the runtime's SQL admission
  and request deadline, like any REST request. Transfers then release the
  admission slot, so a slow upload never holds a database connection. Each
  instance runs at most `DataAPIStorageTransfers` (4) transfers at once and
  answers `503 storage_busy` beyond that. Each transfer must finish within
  `DataAPIStorageTransferTimeout` (120 s).
- **Usage and billing.** Storage requests count in usage reporting like
  REST requests, with download bytes as response bytes. Bucket capacity,
  quotas and billing are object storage's (ADR-1320).
- **Restart only.** As with other Data API settings, storage takes effect on a
  fresh restart.

## Consequences

- Owners keep one authorization model, and file lists are ordinary queries
  that live reads can follow.
- Bytes pass through the Data API's VM. That avoids per-origin bucket CORS
  and keeps credentials server-side, at the cost of the VM's bandwidth and a
  per-object ceiling well below object storage's own.
- A commit that fails after a successful transfer can leave an object without
  a row. Such objects are unreachable through the Data API and visible to the
  owner in the bucket.
- The TypeScript SDK gains `createStorageClient`. Python and Go clients call
  the same routes; typed helpers there come later.

## Rejected alternatives

- **Direct browser uploads to presigned URLs.** They need bucket CORS for every
  application origin, which managed buckets do not expose. They also cannot
  record the upload's metadata in the same step.
- **A separate storage policy language.** It would duplicate RLS and drift
  from the owner's data rules.
- **Reusing Application Auth's signer.** It ties storage to one issuer and to
  organization-scoped keys, while a Data API accepts any issuer.
- **A private `gregale_storage` schema.** The Data API login would need grants
  outside the schema that migration owners already grant to it, and file
  lists would lose REST and live reads.
