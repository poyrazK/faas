# Storage grants: compute without store credentials

ADR-20261117190000. Compute nodes reach the shared artifact store only
through 5-minute per-object URLs that secretd signs for tenants placed on
the node. Everything is off by default.

## Before

- Object GC runs on the control plane and nodes defer deletes
  (`docs/runbooks/object-gc.md`). Grants require defer mode.
- secretd already serves the nodes: `faas_secretd_target` is set, and each
  node has its secretd client leaf (`/etc/faas/tls/secretd-client/vmmd.crt`).

## Turn it on

1. **Control plane.** Set `faas_secretd_storage_grants: gcs` and
   `faas_secretd_storage_bucket: <artifact bucket>`, then converge secretd.
   - On GCS, secretd's service account must be able to sign for itself
     (`roles/iam.serviceAccountTokenCreator` on itself) and read and write
     the bucket.
   - On S3, set `faas_secretd_storage_grants: s3` and
     `faas_secretd_storage_s3_{endpoint,region,access_key}`, and put the
     secret key at `faas_secretd_storage_s3_secret_key_path`.
   - Check: the journal shows `storage_grants=gcs`, and
     `faas_secretd_storage_grants_total` is exported, all zero.
   - S3 stores must honor conditional writes (`If-None-Match: *`,
     `If-Match`): AWS S3 and MinIO do. Before turning grants on for a
     new S3 provider, run the live tests against a disposable bucket:
     `FAAS_S3_LIVE_BUCKET=… FAAS_S3_LIVE_ENDPOINT=… FAAS_S3_LIVE_REGION=…
     FAAS_S3_LIVE_ACCESS_KEY_ID=… FAAS_S3_LIVE_SECRET_ACCESS_KEY=…
     go test -run LiveGrantedS3 ./pkg/storage`.
2. **One compute node.** Set `faas_storage_grants: true` for the node and
   converge it. Then restart `faas-imaged`, `faas-builderd` and `faas-vmmd`
   on that node, in that order.
   - Check: each journal shows `shared store reached through secretd grants`.
   - Wake an app on the node from a cold cache, and run a build there.
   - `faas_secretd_storage_grants_total{outcome="granted"}` rises.
   - `select subject_class, access, sum(objects) from storage_grant_audit where granted_at > now() - interval '10 minutes' group by 1, 2`
     shows the node's tenants only.
3. **The fleet.** Roll the node switch out node by node.
4. **Take the store role away** (ADR slice 5). On S3 fleets this first
   needs the compute-node storage gates to accept a key-less
   `storage.env` and `imaged-storage.env` under grants
   (`compute_only_service`, `compute_admission`, `node_join.yml`,
   `gregalectl deploy join`): they still require `FAAS_S3_ACCESS_KEY_ID`.
   The daemons themselves start without a key under
   `FAAS_STORAGE_ACCESS=secretd`. Once every node runs
   grants, remove the compute service account's bucket role (GCS) or delete
   its access key (S3). Check from a compute node that a direct read of a
   bucket object is refused.

## Read it

- `faas_secretd_refusals_total{rpc="GrantStorageObjects"}` rising means a
  node asked for an object of a tenant not placed on it. Look at the node
  in secretd's journal (`refused storage grant`, with the class, subject and
  reason). A burst from one node is the compromise signal.
- A wake that fails with `grant ... PermissionDenied` while the app is placed
  on the node means a placement row is missing. Compare the node with
  `instances`, `deployment_image_preparations`, `builds.builder_node_name`
  and `sandbox_sessions.placed_node_id`.
- End-to-end check: the native lane `storage-grants-only`
  (`FAAS_E2E_PHASE=storage-grants-only`, `FAAS_E2E_MINIO_BIN=<minio>`) runs
  source and image deploy → wake with imaged, builderd and vmmd holding no
  key; teardown logs the grants issued per class.
- On granted nodes, imaged's superseded job artifacts and builderd's
  expired sources are no longer swept there. schedd's object GC worker
  sweeps them instead every 6 hours; schedd's journal shows
  `objectgc: sweep` with the number of requests, and the rows carry
  `objectgc sweep:` reasons.

## Turn it off

Set `faas_storage_grants: false` for the node, converge, and restart the
three daemons. They reach the store with their own credentials again, which
only works while the compute role still has bucket access, so turn nodes
off before slice 5 or restore the role first.
