# ADR-468: Regional managed jobs

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

ADR-465 constrains app execution, and ADR-467 carries placement through project
deployment. Account-scoped managed Jobs have no app owner. Their dispatcher
previously selected any admitting compute node, so app placement alone could
not provide a consistent regional execution contract.

## Decision

Persist immutable `jobs.compute_region`. Empty retains legacy unconstrained
placement. `gregale jobs add NAME --region REGION` and `CreateJobRequest` select
placement once when admitting a definition. Job reads and SDKs return the
persisted choice. PATCH accepts the same region and refuses changes, including
clearing it, before updating other intent. Create a new job to relocate it.

New regional definitions use the same default-off apid operator gate and
durable registry admission as apps and projects. The existing
`accepting_new_apps` policy now also governs new job definitions. It does not
stop runs, scheduled occurrences, retries, or replays of existing definitions.
Definition insertion locks registry policy within the account-locked quota
transaction, so a policy pause racing API preflight cannot admit a new job.

WakeJob loads the definition's immutable region and supplies it to the shared
capacity chooser. Jobs carry their own placement intent because they have no
app row. For apps, the persisted app region remains authoritative. Node-local
scheduler affinity cannot override either placement contract. Lack of regional
capacity or available nodes leaves a job task queued without consuming a retry
or creating a VM; the dispatcher may retry when capacity returns.

All runs reference the same immutable job definition. Manual runs, recurring
occurrences, flexible scheduling, retries after guest failures and node loss,
and linked replays therefore use the same region without a redundant per-run
placement snapshot or an override API. Existing flexible start deadlines still
apply during outages.

PostgreSQL independently checks every job instance insertion and node/job
assignment against the definition. It serializes placement with node relabeling
through a shared node-row lock. Every node in a referenced job region retains
its label, including idle peers and tombstoned definitions. Registry deletion
and down migration refuse to discard job placement constraints. MemStore
mirrors admission, instance placement, and node-label guards.

The receiving vmmd also verifies the durable job claim before `BootJob` allocates
resources. Its registered compute-node identity must match both the request and
the instance; the instance kind, run, task index and admitted RAM must match.
The run and definition must belong to the requested account, the definition and
node must still permit execution, and the node must satisfy the stored region.
Finally, the task must still claim that instance with the current, unexpired
lease token. Missing records, unavailable readers, stale claims, and a misrouted
RPC refuse before boot. The response reports the receiver's verified node ID.
Pausing new regional admission continues to permit an existing valid claim.

Named vmmd workers use their existing PostgreSQL connection and registered
identity. A legacy single-box vmmd can load its sole active `default-local` row
using `db_url`, `FAAS_VMMD_DBURL`, or the existing `DATABASE_URL` daemon setting.
It does not register or mutate that row. Identity inference is refused when any
additional compute node exists; shared fleets must configure self-registration.
Without a claim reader and verified identity, managed Job boots are unavailable.
Other VM surfaces retain their existing single-box startup behavior.

## Boundaries

This constrains compute execution in the shared fleet. Image artifacts, job
inputs/results, managed objects, billing and the control plane retain their
existing storage and routing topology. Compute-region selection does not
promise regional data residency or independence from control-plane outages.
Project app.yaml jobs remain app workloads under ADR-467; account-scoped
managed Jobs use this definition contract.

## Validation and rollout

Apply the migration and upgrade apid, schedd, and SDK/CLI clients. Retain the
default-off admission gate until native Linux x86_64/KVM qualification. Do not
run old schedulers against regional jobs: database guards refuse misplaced
instances, but an old scheduler cannot select the eligible regional peer.

Portable tests cover API admission and immutable updates, a pause racing job
insertion, region selection despite local affinity, regional capacity/outage
refusal, retry fencing, and legacy placement. PostgreSQL tests cover definition
persistence, direct SQL bypasses, idle-node relabeling, atomic claim rollback,
and paused admission. SDK and CLI tests verify region transport and display.

Native acceptance must execute a digest-pinned image on the selected region,
lose a worker and retry on an in-region peer, then lose every worker in the
region and verify no task instance or boot RPC reaches a remote worker. Inspect
every `instances.job_id` placement against `jobs.compute_region`, run the
affected `test-metal` suite and `leakcheck`, and retain fleet evidence before
promoting this capability. That qualification remains pending.

`make native-regional-acceptance` includes the regional Jobs drill alongside
app placement and source-export handoff. All workers must pass native-host,
KVM, distinct-identity, designated-fleet and service checks before workloads
or faults. The Jobs drill verifies retained terminal instance placement, a
failed worker attempt followed by an in-region retry, queued work with an
unchanged retry budget during a one-minute whole-region outage, and successful
resumption on a regional peer. The foreign vmmd's boot counter and systemd
invocation must remain unchanged. Pre/post worker leak checks and cleanup are
required. The dedicated gate refuses skipped or missing drills; implementation
and portable harness checks do not constitute native qualification. See
`docs/ops/native-regional-acceptance.md` for inventory and fixture inputs.

Independent regional control planes, artifact/data boundaries, geographic
ingress, and a global identity with multiple regional targets remain further
work in the end-to-end multi-region goal.
