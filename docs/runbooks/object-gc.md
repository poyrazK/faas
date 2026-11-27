# Object GC on the control plane

ADR-20261115180000. Compute nodes record the shared-store objects they want
deleted in `storage_object_deletions`. A control-plane schedd deletes each
one once nothing references it. Both switches are off by default.

## Turn it on

1. **Control plane first.** Set `FAAS_SCHEDD_OBJECT_GC=1` for one
   control-plane schedd and restart it. Its `FAAS_STORAGE_*` must address
   the same store as compute, with credentials that can delete. Check:
   - The journal shows `schedd: object GC enabled`.
   - `faas_object_gc_requests_total` is exported, all zero.
2. **One compute node.** Set `FAAS_STORAGE_DELETE_MODE=defer` for imaged and
   vmmd on that node, then restart both. Check:
   - The journals show `shared-store deletes deferred to the control plane`.
   - Requests appear:
     `select state, count(*) from storage_object_deletions group by 1`
   - After about 10 minutes they move to `deleted`.
   - `faas_object_gc_requests_total{outcome="deleted"}` rises.
3. **The fleet.** Roll defer mode out node by node.
4. **Drop compute's delete right** (ADR slice 3). Wait until every node
   defers and the queue stays short:
   `select count(*) from storage_object_deletions where state = 'requested' and next_attempt_at < now()`
   should stay near zero. Then:
   - GCS: give the compute service account `objectCreator` + `objectViewer`
     instead of `objectAdmin`.
   - S3: deny `s3:DeleteObject` and turn on versioning.
   - Exclusive-artifact retirement (native capture cohorts) already runs
     through the outbox in defer mode (ADR slice 2). Native snapshot
     publication works only on GCS, so S3/OCI fleets have nothing extra.
   - Check: on a deferring node, a retired cohort shows up as four rows
     with a `generation`, and no `RetireExclusiveArtifact` errors appear
     in vmmd's journal.

## Read it

- The worker also lists `jobs/` and `sources/` every 6 hours and requests
  the objects their compute-side sweeps would delete (job attempts of
  deleted, failed or superseded jobs; sources of finished builds older
  than 24 hours). Look for `objectgc: sweep` in schedd's journal.
- `refused` rows are evidence. A refused snapshot or layer key means a node
  asked to delete something still live; `requested_by` names the node. A
  refused `platform` or `unclassified` key means a request the control plane
  never deletes. Look at the node:
  `select storage_key, requested_by, last_error, decided_at from storage_object_deletions where state = 'refused' order by decided_at desc limit 50`
- Rows retried with `last_error = 'referenced'` are normal while a cleanup
  is still deleting its rows. Rows retried with `delete:` or
  `delete generation:` errors mean the control plane's store credentials
  cannot delete.
- A row with a `generation` is an exclusive retirement. It deletes only
  that generation. `refused` with `the key holds another generation now`
  means the key was rewritten after the node published it; the newer
  object is left alone. That is expected after a republish, and suspicious
  only in bulk from one node.

## Turn it off

Unset `FAAS_STORAGE_DELETE_MODE` on the nodes first: they delete directly
again. Keep the worker running until the requested rows drain, then unset
`FAAS_SCHEDD_OBJECT_GC`. Rows left behind do no harm; an object they name
is only deleted after a fresh reference check.
