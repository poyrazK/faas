# ADR-20261117190000: Compute reaches the shared store only through per-object signed grants

- Status: Accepted (slices 1–4 implemented, off by default; amendment 1, 2026-11-21)
- Date: 2026-11-17
- Amends: ADR-054 (compute nodes read and write the shared artifact store
  with bucket-wide credentials)
- Related: ADR-20261110210000 (customer secrets open only on the control
  plane; secretd), ADR-20261115180000 (object deletion moves to the control
  plane), the 2026-11-15 compute keys audit (row 1: object-store
  credentials on every compute node)

## Context

ADR-20261115180000 took delete away from compute. Every compute node
still holds bucket-wide read and create, though (GCS `objectViewer` +
`objectCreator`, or the S3 equivalent). A rooted node can read every
tenant's app layers, job layers, sandbox captures and screenshots, and the
unsealed snapshots. Sealing (ADR-20261110210000 slice 3d-3) protects
snapshot memory, but not the rest. It can also create objects under any
key nothing has written yet.

Placement already says which tenants a node may serve. secretd already
checks it for every secret a node opens: owner, grant, image preparation,
job materialization lease, or build claim.

## Decision

Owner decisions (2026-11-17):

- Use control-plane-signed URLs rather than a byte proxy or IAM conditions.
- Grant only for apps placed on the node.
- Writes move to grants too.
- Cover GCS and S3.

1. **The control plane signs, the node fetches.**
   - secretd gets `GrantStorageObjects`. A node names up to 64 keys and an
     operation for each:
     - `get`
     - `get_generation(n)`
     - `create` (if-generation-match 0)
     - `replace(n)` (if-generation-match n)
   - For each admitted key, secretd answers with a signed URL valid for 5
     minutes, plus the headers the node must send.
   - The node downloads or uploads directly from the store, so bytes never
     cross the control plane.
   - Signing uses the control plane's store credentials: GCS V4 (signBlob
     or a key) and S3 SigV4 presign, behind one `storagegrant.Signer`.
2. **A key resolves to the subject that owns it.** `storagegrant.Resolve`
   is pure and table-tested, and it is the only parser of the keyspace:

   | key | subject |
   |---|---|
   | `base/…`, `kernel/…`, `scans/<base name>.scan.json` | platform: any node may read, create, or replace the object it read (ETag/generation-conditioned) (amendment 1) |
   | `sigs/<key>.sig` | the subject of `<key>` |
   | `sboms/<build>.cdx.json` | build output: the node that ran the build creates it |
   | `snap/<deployment>/…`, `apps/<slug>/<deployment>[-<sidecar>].ext4`, `layers/<deployment>.ext4` | deployment |
   | `snap/sandbox[-template]-<account>/captures/…`, `sandbox-screenshots/<account>/…` | sandbox account |
   | `jobs/<job>[__<artifact>].ext4` | job |
   | `sources/<build>.tar.gz` | build |
   | anything else | refused |

3. **The subject must be placed on the caller.**
   - Deployment: the node owns its app (a live instance) or holds a
     secretd grant for it (migration), the same check as `authorize`. An
     unfinished image preparation on the node (`NodePreparesAppImage`) or
     a build it claimed also qualifies. For reads only, so does a live
     sandbox session on the node that boots the deployment's layer (a
     sandbox template build).
   - Sandbox account: the node runs a live sandbox session of that
     account.
   - Job: the node holds the job's materialization lease, or runs one of
     its tasks.
   - Build: the node claimed the running build (`builds.builder_node_name`),
     within the build's run window. Sources are read-only for nodes.
   - Platform writes are creates or conditioned replaces from any node;
     build outputs are create-only. Both are audited (amendment 1).
   - Every grant counts against the per-node distinct-app budget, is
     audited (`secret_open_audit`, via `storage_get`/`storage_put`) and is
     counted in `faas_secretd_storage_grants_total{op,class,outcome}`.
4. **Compute holds no store credentials.**
   - With `FAAS_STORAGE_ACCESS=secretd`, the GCS backend on compute swaps
     its `gcsArtifactStore` for one that asks secretd and then uses the
     signed URLs. The backend keeps its envelope (compression, metadata,
     exclusive receipts), and the cache, router and deferral wrap it
     unchanged.
   - `List` reports nothing in that mode: a node cannot list the bucket.
     The compute-side sweeps that list (imaged's superseded job artifacts,
     builderd's expired sources) stop on granted nodes, so the control
     plane's object GC worker lists `jobs/` and `sources/` every 6 hours
     with the same rules and files deletion requests. The worker re-checks
     each one: a `jobs/` attempt is live while its job is materializing or
     publishes it; a `sources/` archive (class `source`) is live while its
     build is queued or running, or while its UUIDv7 name is under 24 hours
     old.
   - `Put` probes the key's generation and writes create-only or
     replace-that-generation, so a node never overwrites blindly.
   - Read grants are cached per key until 30 s before expiry; a store
     refusal renews once. builderd's 500 ms source poll asks secretd once
     per grant, not per poll.
   - Sandbox sessions record the node schedd placed them on
     (`sandbox_sessions.placed_node_id`) before boot, because the node reads
     the session's image and snapshot while booting; `node_id` still means
     "the VM is up". A session node may also read its template app's layer
     and its job image.
   - Operations: `faas_storage_grants: true` per compute node installs one
     drop-in for imaged, builderd and vmmd (defer deletes, grants, the
     node's secretd client leaf); each daemon picks it up at its next
     restart.
   - Deletes must already be deferred: secretd mode refuses to start
     without `FAAS_STORAGE_DELETE_MODE=defer`.
5. **Then compute loses the role (operations).** Once every node runs
   secretd mode, remove the compute service account's bucket role. On S3,
   delete its access key.

## Consequences

- A rooted node reads and writes only objects of tenants placed on it,
  for 5 minutes per grant, and every grant is recorded with its name.
- One extra control-plane round trip per cache miss: about 1 ms in-region
  against restore times of hundreds of ms. Grants are batched per wake, and
  hits in the node's read-through cache never ask.
- secretd becomes a dependency of cold cache misses. A node that cannot
  reach secretd serves from its cache and fails cold misses, as it already
  does for sealed snapshots.
- A key class the resolver does not know is refused. New key classes must
  be added to the table before they ship.

## Amendment 1 (2026-11-21): companion objects and platform creates

The first end-to-end run with real daemons on grants (the native
`storage-grants-only` lane: imaged, builderd and vmmd with no store key,
secretd signing for a loopback MinIO) found what the unit tests could not:

- **Companion objects were outside the keyspace.** Every artifact's
  signature (`sigs/<key>.sig`), the Grype sidecars of platform bases
  (`scans/…`) and build SBOMs (`sboms/<build>.cdx.json`) are read or written
  by compute daemons. A signature now resolves to the subject of the key it
  signs, base scan sidecars to platform, and an SBOM to a new
  `build_output` class: the node that ran the build may create and read it.
- **Compute imaged stages and refreshes platform bases.** It writes
  `base/…` and its `sigs/` and `scans/` sidecars when a base is missing,
  patches a base in place when guest-init changes, and renews scan
  sidecars. Owner decision (revised after review, 2026-11-22): any granted
  node may create a platform object or replace one conditioned on the
  ETag or generation it read, so concurrent writers lose no update and a
  key planted early can be overwritten by a legitimate node. A first
  draft made platform keys create-only; that refused every base patch and
  scan renewal. Build outputs (SBOMs) stay create-only.
  - Every platform write is recorded in `storage_grant_audit` (class
    `platform`, nil subject) and logged with its key; reads are only
    counted.
  - Integrity of bases and kernels rests on the control plane's signature
    (signd), which readers verify, not on who wrote them.
  - Scan sidecars are not signed. A node can write a misleading scan, as
    any node holding the bucket key can today; the audit row names it.
    Signing scan sidecars is a follow-up.
- **An S3 node could not start without a key.** The S3 backend required
  `FAAS_S3_ACCESS_KEY_ID`/`SECRET` even under `FAAS_STORAGE_ACCESS=secretd`,
  so deleting compute's key (slice 5) would have stopped the daemons. Under
  grants the key is optional and the client is anonymous.

A first run of 100 image deploy → park → wake cycles on grants passed on
the nested-KVM test host, but it kept `snap/`, `base/`, `kernel/`,
`layers/` and `scans/` on node disk. The lane now sets
`FAAS_STORAGE_LOCAL_PREFIXES=none` and `FAAS_REQUIRE_SHARED_ARTIFACTS=1`,
as production compute does, and fails on any secretd refusal.

## Slices

| slice | content |
|---|---|
| 1 | `pkg/storagegrant`: keyspace resolver, signer interface, GCS V4 and S3 presign signers (implemented) |
| 2 | secretd `GrantStorageObjects`: placement checks, `storage_grant_audit`, `faas_secretd_storage_grants_total`, budget; `FAAS_SECRETD_STORAGE_GRANTS=off\|gcs\|s3` (implemented, off by default) |
| 3 | Compute: signed-grant `gcsArtifactStore` (`storage.UseGrants`), `FAAS_STORAGE_ACCESS=secretd` in imaged, vmmd and builderd (builderd also defers deletes now), `build` key class for `sources/`, `placed_node_id` recorded by agentd, template and job-image reads for session nodes, `faas_storage_grants` drop-in (implemented, off by default). The control-plane sweep of `jobs/` and `sources/` (`objectgc.Sweep`, on with `FAAS_SCHEDD_OBJECT_GC`) followed with slice 4 |
| 4 | S3 artifact backend on grants: `head`, ETag-conditioned `create`/`replace`, and multipart uploads where `create_multipart`, every `upload_part`, `complete_multipart` (create-only or replace-that-ETag) and `abort_multipart` are each a grant; the multipart create/complete/abort are SigV4-presigned directly (the SDK presigns only object and part requests). Server-side copies report unsupported, so callers stream. Tested against a fake S3 in CI and against MinIO (`FAAS_S3_LIVE_*`) (implemented) |
| 5 | Operations: remove compute's bucket role; runbook check that a direct read is refused |
