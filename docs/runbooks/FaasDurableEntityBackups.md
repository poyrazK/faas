# Durable entity backups and restore preview

This is a local, unqualified operator preview. Tests/builds/provider checks are
pending; do not treat source implementation as recovery evidence.

## Symptom

Expected backup metadata is missing, capture scans fail, or an operator needs to
compare a saved export with current entity state. A delayed capture is not proof
that live state was lost. A backup listing alone does not prove restore readiness.

## Check

Enable `FAAS_DURABLE_ENTITY_BACKUPS_ENABLED=1` only with the private entity bucket,
existing invocation preview and explicit app allowlist. The provider must support
strongly consistent conditional writes, ordered listing and deletion. Startup
checks these capabilities with a unique backup probe. Credentials remain private
platform configuration. The worker starts immediately, scans bounded pages and
captures one application export per visited entity per UTC hour. It does not wake
the app or acquire its execution lease.

The retention policy is seven days with bounded eventual pruning after a successful
current-hour capture. Large buckets, failed scans and downtime can delay capture
and expiry. There is no backfill or guaranteed hourly recovery point. Budget for
roughly 169 hourly exports per continuously visited entity near the retention
boundary, plus delayed cleanup. These bytes are additional to live entity storage.
Configure lifecycle expiry restricted to `gregale/durable-entity-backups/v1/` for
orphaned/disabled app backups; account-erasure procedures must delete backups too.
Do not apply that lifecycle policy to live entity or receipt prefixes. This work
does not apply a bucket lifecycle policy or enable a production worker.

## Recover

To inspect recovery options:

1. List metadata at `/v1/apps/{slug}/entities/backups` using namespace/key and the
   environment/tenant selectors. Follow `next_cursor`; lists are observational.
2. Read an exact `backup_id` at `/entities/backups/get`. Keep its complete export
   envelope private and unchanged. JSON round-trip changes can invalidate checksums.
3. Inspect current state metadata, then send that current expected version and the
   export to `/entities/restore/preview`. Preview needs read scope and performs no
   mutation. It shows pending/exhausted work that restore will preserve.
4. Validate application schema compatibility separately. The preview deliberately
   reports `unverified`; a schema match is not proof that the current handler can
   use the exported application data.
5. Restore using the existing owner mutation endpoint with deploy-write scope,
   an explicit stable request ID and expected current business version. Preserve
   the identical request for retries after timeout or uncertain acknowledgement.
   Preview is not a reservation; a concurrent transition can make restore stale.

Backup capture excludes receipts, alarms and outgoing work. Restore preserves the
current histories and retry state; it does not rewind completed effects or clear
exhaustion. It cannot recover a lost receipt journal or clone state into another
immutable scope. An independently secured backup provider is still needed for
off-provider disaster recovery. Existing aggregate object-store operation metrics
include `backup_scan` success/failure; no payload or entity identifier is logged.

## Application validation before restore

Deploy a synchronous pure validator at the distinct private
`/__gregale/entities/validate-restore` route before enabling
`FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1` consistently across API writers.
Resolve outstanding unvalidated restores first. The read-only preview remains
metadata-only; application validation uses a deploy-write execution endpoint.

Call `/entities/restore/validate` with the export, expected version and stable
request ID. If `valid` is true, set `validation_deployment_id` to the returned
`deployment_id` in the actual restore. Restore revalidates under its private claim;
receipt replay bypasses validation. Keep all fields unchanged on uncertain retries.
Rejected candidates and malformed/unsupported validators cannot publish state.

Purity is an application contract: the runtime does not independently disable
arbitrary guest network/database calls. Normal invocation resources are used.
Validation does not migrate the export. Deployment routing and bucket publication
are not atomic, so avoid deploy changes during recovery and keep schema-compatible
readers. This gate is default off and has not been qualified or enabled here.
