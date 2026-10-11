# Fenced durable registry metadata cleanup

This optional operator workflow reclaims obsolete private registry shard objects.
It does not delete application manifests, state snapshots, request receipts,
backups, quota roots, active reservations or permanent retirement fences. It does
not credit customer byte quota: these objects are platform control-plane storage.

## Why an epoch is required

A content-addressed shard can become live again after its hash was removed from
one root. A delayed deletion worker could therefore destroy a reused key if
cleanup only compared its candidates to a current root snapshot.

Cleanup copies the complete authenticated registry into a strictly greater epoch
under `tenant-registry/v2/<scope>/<20-digit epoch>/<shard>/<hash>`, then publishes
all references and schema 6 in one quota-root CAS. Legacy schema-4
shards use `tenant-registry/v1/<scope>/<shard>/<hash>`. Once cutover commits,
writers only create shards in the current epoch. An older epoch is never reused.
An uncertain or stale cutover cannot authorize deletion. Uploads before a failed
cutover are inert; a later epoch rotation can collect those leftovers.

Every old root CAS is fenced by cutover. Older binaries reject schema 6; current
writers continue within the new epoch. The immutable-shard cache includes the
full epoch path and every root remains fresh. A worker delayed past completion
or a later cleanup can only delete epochs below its original barrier, so it
cannot delete newly live objects even when their content hashes are identical.

## Enable and start

Keep production disabled until independent GCS/KVM acceptance passes. Deploy
schema-6-capable quota readers to every participating node before enabling
`FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_ENABLED=1` in the acceptance environment.
It requires registry management and its managed-policy/migration/tenant-quota
prerequisites. Startup lists and deletes only its own unique private probe key,
then verifies that key is absent. No automatic cleanup worker is enabled.

Select one account/app/immutable environment/platform-tenant scope through the
existing exact entity selectors. The namespace/key identifies the scope; the
cleanup applies to every registry shard in that scope.

- `GET /v1/apps/{slug}/entities/registry/cleanup`: current checkpoint, or null
  when no cleanup has started.
- `POST /v1/apps/{slug}/entities/registry/cleanup/start`: body
  `{"operation_id":"<fresh UUIDv4>","expected_revision":"<registry GET revision>"}`.
- `POST /v1/apps/{slug}/entities/registry/cleanup/step`: body
  `{"operation_id":"<cleanup UUID>","expected_revision":1}` using the current
  numeric cleanup revision. Each call processes at most one bounded listing page.

Reads require app read access. Start/step require deploy write access, MFA and
normal recovery eligibility. All responses use `Cache-Control: no-store`.
First migrate the scope to the registry. Complete quota inventory migration,
resolve all tracked pending retirements and align node limits. Existing scope
connections may remain active. Start requires the exact observed quota-root
revision; a concurrent write conflicts. Exact replay of the latest cleanup operation and original
source revision returns its original actor/time and current progress.

Start copies all 16 live shards and atomically publishes epoch and references.
The grace boundary is at least the maximum entity lease plus invocation
timeout (currently 5 minutes 25 seconds). Cleanup steps before `not_before`
return 409 without listing or deleting. Existing readers have that interval to
finish using the prior namespace. In-flight old writes cannot publish their old
root after cutover. Entity ownership and application state transitions are not
changed; full entity reservations remain conservative while previously admitted
work finishes.

## Bounded cleanup and recovery

The persistent phases are `legacy_cleanup`, `legacy_verify`, `epoch_cleanup`,
`epoch_verify` and `complete`. Legacy passes collect canonical v1 shard keys.
Epoch passes collect canonical v2 shard keys whose epoch is strictly less than
the barrier. The entire current epoch, future epochs, foreign scopes and unknown
names are retained, even if an object is not currently referenced. No single
root scan authorizes deleting an object in the current epoch.

Each page is validated for prefix, count, duplicates and cursor bounds before
any deletion. Verification starts from an empty cursor; finding a collectible
object resets that phase's cleanup pass. Admission continues throughout both
cleanup and verification passes. The last checkpoint stays in the root; subsequent
cleanup uses a fresh operation/revision and advances the epoch again.

Schema 6 permits invocation admission, grant mint/revoke, connection
acquisition/release and retirement tracking/credit throughout grace and scanning.
They retain the cleanup checkpoint and write only in the current epoch. Inventory
migration, policy changes and another rotation remain blocked until completion;
they are separate operator workflows. Read-only inspection continues. There is
no cancellation or automatic expiry of cleanup. Turning off the flag hides its
routes but leaves progress intact; schema-6-capable readers continue admission.
Re-enable management to resume collection. Older binaries cannot read schema 6,
including after completion; do not downgrade them onto a rotated scope.

Existing schema-5 cleanups retain their original full write hold, including
503 `durable_entity_registry_cleanup_pending` on admission. Finish or resume
that cleanup before starting a fresh operation that upgrades to schema 6. The
new binary does not silently lift a previously committed hold.

Unknown deletes may already have happened. They leave the checkpoint unchanged;
inspect and retry its current revision. Unknown checkpoint writes may have
advanced the root; GET status before another step. Root conflicts require the
current revision. When ordinary quota writes win a root CAS race, the worker
re-reads the root and merges only its checkpoint if operation, epoch and the entire
previous checkpoint still match. It preserves fresh reservations, counters,
grants, connections and retirement credit without listing/deleting the page again.
An advanced/restarted checkpoint fences the worker with 409. After eight definite
root conflicts the step returns a busy response with progress uncommitted;
inspect and retry. Unknown acknowledgements never trigger automatic retries.
Cleanup can need more retries under sustained write contention; the root remains
one shared CAS authority. If a provider cursor expires, submit the current
revision with `"restart_cleanup":true`; it checkpoints a reset to the first cleanup pass
without removing the epoch fence. Process restart preserves all progress in the
root. Deletion is idempotent because collected epoch keys can never be live again.
`deleted_objects` is a best-effort checkpointed count, not provider billing or
quota authority; lost acknowledgements can make it undercount.

An arbitrarily delayed old upload can leave another orphan after verification.
It cannot become live and a later cleanup can collect it. Grace and verification
bound practical leftovers without claiming all historical writers disappear.
Current-epoch superseded shards are deliberately retained until the next rotation.
Incomplete rotation uploads in future epochs are also retained until those epochs
are strictly below a later successful barrier.

## Source and native acceptance

The concurrent-admission implementation at source commit
`b1ae7602d7e79352069bc023ff245a7898ffbc2e` passed
[remote CI run 38165206034](https://github.com/poyrazK/faas/actions/runs/38165206034).
Validation includes four durable Go packages under the race detector (including
eight cleanup tests), 31 focused API race tests, CLI and generated-protocol
checks, 16 Node workflow tests and the packed starter install/typecheck workflow.
No local tests or native GCS/KVM acceptance were run for this revision.

The preceding admission-holding implementation at source commit
`7a9893c5e9d83bdb8870f4168132315540e20392` passed
[remote CI run 38162473641](https://github.com/poyrazK/faas/actions/runs/38162473641).
Validation includes four durable Go packages under the race detector (including
the five cleanup tests), 31 focused API race tests, CLI and generated-protocol
checks, 16 Node workflow tests and the packed starter install/typecheck workflow.
No local tests or native GCS/KVM acceptance were run for this change.

Remote coverage checks two epoch rotations, permanent fence and recovery-charge
preservation, paginated history deletion, unknown root/delete acknowledgements,
checkpoint restart/staleness, malformed pages, safe private probing, a stale
cutover racing admission and a delayed delete racing resumed writes with reused
content. API coverage checks read-only authorization, start replay, reader grace,
dispatch during grace and collection, completion and disabled/configuration
guards. Additional source coverage checks checkpoint merges preserving concurrent
admission, grants, connections and retirement credit; eight-attempt contention
bounds without repeated deletion; and schema-5 hold compatibility.

Native GCS/KVM acceptance must verify conditional cutover across nodes, old-schema
rejection, independent warm/cold cache behavior, bounded listings/cursor restart,
process loss and delayed writes/deletes, concurrent admission and checkpoint
contention, legacy schema-5 holds, current/future/foreign key preservation,
permanent retirement fences, unchanged customer accounting,
and metadata storage growth across repeated rotations. Measure reader grace and
checkpoint contention before production enablement. No production flag, PR or
deployment is part of this change.
