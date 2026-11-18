# ADR-20261117190000: Compute reaches the shared store only through per-object signed grants

- Status: Accepted (slices 1 and 2 implemented, off by default)
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
   | `base/…`, `kernel/…` | platform: any node may read, no node may write |
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
   - Platform keys are read-only.
   - Every grant counts against the per-node distinct-app budget, is
     audited (`secret_open_audit`, via `storage_get`/`storage_put`) and is
     counted in `faas_secretd_storage_grants_total{op,class,outcome}`.
4. **Compute holds no store credentials.**
   - With `FAAS_STORAGE_ACCESS=secretd`, the GCS backend on compute swaps
     its `gcsArtifactStore` for one that asks secretd and then uses the
     signed URLs. The backend keeps its envelope (compression, metadata,
     exclusive receipts), and the cache, router and deferral wrap it
     unchanged.
   - `List` is unsupported in that mode. Listing for GC belongs to the
     control plane (ADR-20261115180000).
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

## Slices

| slice | content |
|---|---|
| 1 | `pkg/storagegrant`: keyspace resolver, signer interface, GCS V4 and S3 presign signers (implemented) |
| 2 | secretd `GrantStorageObjects`: placement checks, `storage_grant_audit`, `faas_secretd_storage_grants_total`, budget; `FAAS_SECRETD_STORAGE_GRANTS=off\|gcs\|s3` (implemented, off by default) |
| 3 | Compute: signed-grant `gcsArtifactStore`, `FAAS_STORAGE_ACCESS=secretd` in imaged, vmmd and builderd; compute-side listing GC skipped. Also: agentd records a sandbox session's node at placement (today only at boot, after the node already read its image), and a session node may read its template layer |
| 4 | S3 artifact backend on grants (with the OVH S3 store) |
| 5 | Operations: remove compute's bucket role; runbook check that a direct read is refused |
