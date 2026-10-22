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
then verifies that key is absent. Background continuation has a separate flag
and is disabled by default.

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

## Optional background continuation

Set `FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_WORKER_ENABLED=1` only in the native
acceptance environment after deploying compatible readers. It requires cleanup
management and its prerequisites. Startup checks bounded quota-root/index
listings and this process's unique private worker/index probes. Worker probes
verify conditional create/update/deletion; index probes verify create/deletion.
They do not inspect or step a scope.

One runtime goroutine normally visits up to four active index candidates per
5-second polling interval. Independently, it reconciles up to four quota roots
once a minute, including inactive roots, advancing a separate disposable cursor.
It operates only on the node's configured durable-app allowlist and existing,
unfinished cleanup operations past their reader-grace boundary. It never starts
a cleanup, rotates an epoch or creates a quota root. Operators still authorize
start explicitly. Root reconciliation runs at startup and continues even while
index discovery is failing or backing off.

Each eligible scope takes a known-acknowledged, 30-second CAS claim in
`tenant-registry/workers/v1/<scope-hash>` before attempting at most one cleanup
page. Claims and observations live outside the quota root; they are advisory
scheduling state. Operation/checkpoint and epoch fencing remain deletion
and accounting authority. Competing nodes defer while a claim is live. A lost
claim acknowledgement authorizes no work. A replacement worker cannot have its
observation overwritten by a delayed older claim because observation writes
also compare the private object's version.

A runtime cycle shares a 45-second deadline across both discovery lanes.
Listings have a 5-second deadline, each scope a 15-second budget and each cleanup
step a 10-second deadline. The runtime waits 5 seconds between cycles; successful
scopes wait at least 5 seconds between attempts. Each lane attempts at most one
page per eligible scope in its bounded batch. Failed scope attempts persist exponential backoff starting at 10 seconds
and capped at 5 minutes. Index and root discovery have independent bounded
process-local backoffs and cursors. Index errors start at 10 seconds; root scan
errors start at 2 minutes; both cap at 5 minutes and reset only their own cursor.
Scope records survive restart.
If a process dies before releasing its claim, another node resumes after lease
expiry. Runtime cancellation stops the goroutine and its in-flight contexts.

Unknown step acknowledgements never trigger an immediate blind repeat. The
worker records a sanitized error/backoff when it can, and the next eligible
attempt re-reads the authoritative root and uses its current checkpoint. If the
observation acknowledgement is also unknown, the persisted claim may remain
until expiry. This still confers no permission to repeat a stale checkpoint.
Expired cleanup-page cursors continue to require the operator's explicit
revision-checked `restart_cleanup` step; generic provider errors do not reset
cleanup progress automatically. Completed operations are not attempted again.

`GET .../entities/registry/cleanup` now adds `worker_enabled`, `worker`,
`worker_status_error` and `seconds_since_last_success`. The worker flag describes
this API node, not the whole fleet. The observation
includes operation ID, last observed checkpoint revision, last attempt/success
UTC times, next eligible attempt, lease expiry, consecutive failures (saturating
at 10) and a sanitized error category. It contains no provider error text. The
age is null before a known successful worker step; it measures observed worker
success, not manual progress. Observations can lag the root and are never
recovery authority. A different operation's observation is omitted. Unavailable
worker storage produces `worker_status_error:"unavailable"` while retaining the
authoritative cleanup response for manual recovery.

Turning the worker flag off restores manual stepping and preserves checkpoints,
backoff and unexpired claims. Disable the worker flag before disabling cleanup
management; startup rejects a worker enabled without management. Existing
schema-5 holds can be finished by the worker, but retain their
hold until completion. The worker does not change schema or silently lift them.
There is one small private observation record per attempted scope; temporary
probe leftovers from unknown outcomes are separate from collected shard epochs.

Normal discovery costs scale with active hints and stale candidates. Root
reconciliation still scans all quota roots at a lower rate, so large fleets can
have longer repair intervals; high admission contention can also delay progress. No completion-time guarantee or cross-node exactly-once
DELETE claim is made. Bounded contexts and short advisory leases complement the
permanent epoch fence; delayed provider requests remain safe because they can
only delete permanently obsolete namespaces.

## Active cleanup index

Markers use `tenant-registry/cleanup-index/v1/<scope-hash>/<20-digit epoch>/<UUID>`.
Their keys are canonical and immutable; their bodies provide no authority.
A known-acknowledged cleanup start publishes its marker best-effort, and exact
start replay repairs it. Known completion removes that exact marker best-effort.
Each publication/removal has a 2-second budget within the caller's context.
Marker failure does not turn a known root acknowledgement into an uncertain
start/step outcome. A lost root acknowledgement still requires root inspection
or exact replay before assuming start succeeded.

Root reconciliation repairs hints for authenticated unfinished operations,
including during reader grace. It also provides a bounded fallback cleanup step
when index access fails. A failed marker write is reported as deferred discovery
while fallback progress can still commit. Older binaries that do not publish
hints remain discoverable through this scan. Deleted/missing hints are not lost
work: the root's persisted operation remains authoritative. Replaying the latest
start request with its original source revision can repair a known scope's hint
without waiting for a complete root scan.

Every indexed candidate is checked against its fresh authenticated root,
including operation ID, epoch, phase, grace and the app allowlist. The ordinary
worker claim/backoff and step's fresh checkpoint validation still apply. An index
entry cannot create an operation, bypass grace or allow application-state deletion.
Whole pages are checked for prefix, duplicates, count and cursor bounds before
processing any candidate. Future epochs, unknown names, unmatched current-epoch
operations and roots that are absent/unreadable remain untouched.

Only a marker in a strictly older root epoch, or the exact completed operation
in the current epoch, may be pruned. A delayed prune can never hit a later
rotation's marker, even if an operation UUID is reused, because the epoch is
part of its key. A delayed publisher can leave another safe stale marker after
completion; a later index pass collects it. No root, worker observation or shard
is removed by index pruning. Neither index hints nor delete counts are customer
quota authority. These markers are private platform metadata.

There is no atomic transaction between root and index. Safety comes from root
validation; liveness comes from independent bounded reconciliation. This reduces
routine scans of inactive roots without promising immediate repair, zero stale
metadata or constant discovery cost for arbitrarily large active populations.
Configured app subsets still share the candidate pool and can scan one another's
hints without acting on them. Native acceptance must measure index backlog,
root-repair revisit time and provider request/storage cost.

## Cleanup health and bounded backlog observations

The cleanup GET also returns `discovery_health` and `progress_health` without
adding bucket scans. Discovery describes the serving node's index and root
reconciliation lanes: last attempt completion, last successful discovery,
scheduled retry and consecutive discovery failures. It resets on process restart;
another node may report different observations. `healthy` means page discovery
succeeded, not that every operation progressed or that a full scan completed.

Discovery is `retrying` during scheduled backoff and `stalled` if no attempt
completes within one minute after its scheduled time. Startup gets one minute;
disabled and stopped workers have explicit states. Inspect both lanes: successful
reconciliation can continue cleanup while the index is unavailable.

Scope progress includes `unfinished_age_seconds` and distinguishes reader grace,
manual operation, missing/unavailable observations, an active claim, scheduled
retry and ready work. `stalled` means ten minutes have elapsed after the later of
reader grace, last observed success and the scheduled attempt, with no active
lease or pending retry. Missing observations remain `awaiting_observation`; bounded
discovery may not have reached the scope. Treat a stall as an inspection signal,
not permission to override ownership or replay an uncertain mutation.

Worker debug logs report counts for each bounded page: keys scanned, authorized
active operations seen before stepping, oldest active start time in that page,
attempts, scope failures, obsolete hints pruned and hint publication failures.
Counts include grace waiting and can repeat across pages or nodes. They are
observations, not an exact total backlog; do not sum them into one. Customer GETs
do not expose cross-tenant page counts. No external notification is configured.
See [ADR 998](adr/881-durable-registry-cleanup-health.md).

## Source and native acceptance

The cleanup-health implementation at source commit
`8dae92be904aaca71b6e9a55d266f10e035307f3` passed
[remote CI run 38177282361](https://github.com/poyrazK/faas/actions/runs/38177282361).
Validation includes four durable Go packages under the race detector, 34 focused
API race tests (including discovery/progress health and authenticated response
coverage), CLI and generated-protocol checks, 16 Node workflow tests and the
packed starter install/typecheck workflow. No local tests or native GCS/KVM
acceptance were run. Native acceptance must also check independent node snapshots,
clock skew, shutdown/restart, discovery outages, delayed progress and debug-log
observations at realistic backlog sizes before production enablement.

The indexed-discovery implementation at source commit
`4c4fe97fc62e52a6c293c4825e5faf210e4e8a13` passed
[remote CI run 38174072709](https://github.com/poyrazK/faas/actions/runs/38174072709).
Validation includes four durable Go packages under the race detector (including
seven new index tests), 32 focused API race tests exercising indexed cleanup,
CLI and generated-protocol checks, 16 Node workflow tests and the packed starter
install/typecheck workflow. No local tests or native GCS/KVM acceptance were run
for this revision.

The preceding root-scan worker implementation at source commit
`1f3061811ad5355af6d1aadbc2fed1c0951d0186` passed
[remote CI run 38169049230](https://github.com/poyrazK/faas/actions/runs/38169049230).
Validation includes four durable Go packages under the race detector (including
eight new worker tests), 32 focused API race tests, CLI and generated-protocol
checks, 16 Node workflow tests and the packed starter install/typecheck workflow.
No local tests or native GCS/KVM acceptance were run for this revision.

The preceding concurrent-admission implementation at source commit
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
bounds without repeated deletion; and schema-5 hold compatibility. Worker
coverage adds start authorization/grace/allowlist checks, restart recovery,
persisted backoff, competing claims, unknown claim/delete/checkpoint outcomes,
stale observation fencing, bounded discovery, probe isolation and cancellation.

Native GCS/KVM acceptance must verify conditional cutover across nodes, old-schema
rejection, independent warm/cold cache behavior, bounded listings/cursor restart,
process loss and delayed writes/deletes, concurrent admission and checkpoint
contention, legacy schema-5 holds, current/future/foreign key preservation,
permanent retirement fences, unchanged customer accounting,
and metadata storage growth across repeated rotations. Measure reader grace and
checkpoint contention before production enablement. Worker acceptance also
requires multi-node takeover, clock skew, shutdown/restart, prefix permissions,
manual/worker races, observation loss, missing/unknown index writes, delayed
pruning, index outages and discovery/repair cost at realistic root counts. No production flag, PR or
deployment is part of this change.
