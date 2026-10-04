# ADR-521 · Git-owned environment intent and continuous reconciliation

- **Status:** implementation in progress
- **Date:** 2026-09-30
- **Migration reference:** Earlier unreleased GitOps migration comments using
  ADR-387 and ADR-425, and this branch's earlier ADR-459, ADR-393, ADR-423, ADR-425, ADR-428, ADR-429 and ADR-430
  documents, refer to this contract.
  Published ADR-459 covers candidate verification cache isolation.
  Published ADR-430 covers the managed PostgreSQL Commit outbox.
  Published ADR-387 covers FOCUS invoices; ADR-393 covers exclusive operations;
  ADR-425 covers retained cache materialization.
- **Decision:** A registered project environment may bind one approved,
  immutable Git definition to a durable management source. The contract owns
  explicit intent fields, not runtime instances or provider-generated state.
  `apid` owns customer-intent execution; `githubd` resolves immutable source
  content and reports candidates. `schedd` retains exclusive instance ownership.
- **Definition:** `gregale.dev/environment/v1` declares workload source,
  runtime settings, non-secret variables, secret references, queue/service
  bindings, declared routes, supported environment policies, and configuration.
  The complete definition is bounded to 1 MiB by the API limits registry;
  its non-secret configuration object retains the existing separate bound.
  Map entries own individual fields; routes and supported edge policy lists own
  atomic collections. Omission leaves an initially unowned field unmanaged.
  Omitting a previously owned field yields a removal candidate, never an
  implicit deletion. Source images must carry immutable digests. Parsing rejects
  unknown fields, duplicate keys, aliases, and multiple documents.
- **Management:** Repository identity, installation, ref, manifest path,
  approval policy, report/enforce mode, and deletion policy are durable. Approved,
  attempted, and fully applied revisions are distinct. Runtime and credential
  rotation are not drift. Secrets remain sealed in Gregale; Git names refs only.
- **Adoption:** A reviewed plan maps stable logical identities onto existing
  IDs and transfers explicitly selected ownership. Adoption preserves resource
  values and data; subsequent reconciliation applies approved intent. A stale
  plan is rejected before ownership or intent changes. Another manager's fields
  cannot be taken implicitly. Terraform and Git cannot own the same field.
- **Reconciliation:** Durable work is triggered by approved revisions,
  intent changes, and periodic sweeps. One leased run per environment executes
  with a monotonically increasing generation fence. Every write checks its
  current generation and ownership inside the write transaction. Notifications
  wake workers but do not substitute for durable work. Report mode preserves
  authorized edits and records drift; enforce mode rejects conflicting edits
  except within a reviewed, expiring override. An override prevents a false
  converged status and is restored to Git intent after expiry.
- **Observation:** The planner reads one consistent intent snapshot and binds
  its hash to the observed version, resource IDs, ownership, active overrides,
  approved revision, and requested adoption/pruning policy. Application-shared
  settings must gain environment scope before being automatically reconciled;
  unsupported/shared settings are explicit blockers.
- **Execution:** Dependencies and immutable artifacts are prepared and
  qualified before workload release activation. Release sets coordinate
  deployments, but do not by themselves make infrastructure or edge updates
  atomic. Execution journals each step and verifies convergence. Partial
  execution remains visible and resumable. Pruning affects only previously
  owned resources under explicit policy; data and queued work are not reverted
  by a Git rollback. Source outages retain the last approved definition and
  expose stale-source status separately.
- **Why:** Push-to-deploy alone cannot detect or repair out-of-band changes
  to the environment graph, and configuration-only comparisons do not define
  writer authority. Existing project reconciliation has application-global
  mutations and therefore is a reusable detector/planner, not an environment
  executor without scope and transactional ownership changes.
- **Acceptance:** Strict parsing/canonicalization; accurate report-mode drift;
  adoption preserving IDs; stale-plan and ownership conflict rejection; real
  PostgreSQL write races and crash/lease recovery; API/worker/binding environment
  rollout with failure and supersession fencing; Git outage recovery; expiring
  overrides; controlled pruning; API, CLI, dashboard, and generated contracts.
  No launched capability is claimed until these gates cover the complete flow.

## Remaining implementation sequence

1. Complete deployment preparation against the persisted source/runtime specification
   against the original environment UUID, logical workload and mapped app ID,
   rather than update the shared `apps.manifest`. Adopted
   apps keep their IDs; creation must be idempotent under the current approved
   generation. Existing build owners and scheduler admission remain authoritative.
   Immutable image candidates now capture scoped inputs and commit a durable
   imaging handoff, including runtime-only ownership that inherits a reviewed
   immutable live image. Pinned Git source and Dockerfile candidates now publish
   held build rows from verified archives. Functions, new workload creation and
   inherited non-image source provenance still need preparation adapters.
2. Prepare the dependency graph using immutable source artifacts and durable
   deployment/effect identities. Qualify API and worker candidates before release
   activation. The preparation journal now captures every reviewed workload,
   candidate, retained live identity, original queue binding and private consumer.
   Its prepared phase records completed artifacts and grants no execution or
   activation authority. Complete prepared cohorts now publish durable runtime
   qualification requests with separate bounded execution leases. Dedicated
   scheduler instance admission now binds the current attempt and preserves
   reservation limits. Attempt-bound runtime publication and a bounded scheduler
   VM execution primitive and bounded node-owned page consumer are now available.
   Production polling, durable qualification/smoke/restore completion receipts,
   release commands and activation
   remain to be implemented. Retry and crash recovery resume the journaled operation;
   an older generation cannot activate a replacement approved graph. Release coordination
   must expose partial execution across database, edge and runtime boundaries.
   Host lifecycle consumers must use the same frozen contract as the guest.
   Ordinary console deployment, retry and rollback paths must respect the current
   manager's serving authority, as well as the candidate's immutable input hold.
3. Add scoped service-binding adapters that publish the qualified target identity
   and authorization policy. A missing dependency blocks the graph; it must not
   fall back to another environment or mutate an application-wide binding.
   Restore queue consumer projections using the original private consumer and
   receipt namespace, with reviewed repair for ambiguous/missing identities.
4. Qualify every valid catalog environment name across storage constraints,
   secret references, deployment/runtime receipt paths, queue admission and
   public clients. The portable storage and client compatibility checks now
   pass for short and numeric names, including retained queue statistics and
   dead-letter replay after membership changes. Native delivery acceptance
   remains outstanding. Default and all-scopes sentinel semantics stay explicit.
5. Publish graph preparation, qualification, activation and serving convergence
   separately from source freshness and applied intent. Exercise supersession,
   outage, overrides, failed preparation, recovery and retained work on the
   supported native Linux KVM hosts. Only then start the approved-intent worker
   in apid, initially qualifying report mode before continuous enforcement.

For the API/worker/queue example, the final acceptance flow is: approve an exact
reviewed definition, preview and adopt existing identities, prepare and qualify
the scoped graph, activate it, detect a permitted console edit in report mode,
and restore the owned setting in enforce mode after any active override expires.
The test must also prove that neighboring environments and unmanaged settings
retain their values, and that Git rollback does not discard accepted queue work.

## Implementation checkpoint

Source/runtime intent now has a dedicated scoped record keyed by the original
catalog environment UUID and mapped app ID. Observation binds inherited
non-secret runtime settings and original live source identities to the reviewed
plan. Image-source adoption requires unambiguous immutable provenance; other
live source kinds require a reviewed scoped import. Adoption preserves the
existing source and runtime values, including unset defaults. Reconciliation
publishes only owned scoped fields and validates the merged lifecycle against
the account plan and workload class. Shared app manifests and neighboring
environments retain their values. PostgreSQL guards raw SQL changes as well as
state-store writes, validates original tenant/project/environment identity,
and requires a current issued lease or an exact active override for enforced
owned changes. Source/runtime publication rolls back with later intent writes.
Final account purge releases its Git sources and ownership before removing
managed children, within the same deletion transaction. Active-account purges
return without transferring authority; failed purges preserve the original
definitions, ownership and leases. Project/environment cascades purge scoped
records without allowing ordinary owned-row deletion.

This record is preparation input, not a deployment qualification receipt. Any
owned source/runtime field keeps the workload unqualified and prevents the
applied revision from advancing. Variable/secret freshness remains a separate
check; unqualified source/runtime intent alone does not request a variable
refresh of the old deployment. Both the real apid backend and the store's final
convergence fence preserve this distinction. The executor remains disabled
until scoped workload preparation, qualification, graph activation and native
serving acceptance are implemented and verified.

An apid-owned image preparation adapter now creates durable candidates from an
intent-equal reviewed plan under the current source lease. Each candidate freezes
the original environment UUID, mapped app, logical workload, approved revision,
generation, intent version, plan hash, immutable image and inherited build/runtime
inputs. Candidate identity is unique per reviewed input; a lost response or retry
reuses the same row and artifact. PostgreSQL commits the imaging outbox event with
creation and rolls the candidate back if the handoff cannot be recorded. The real
apid backend requests this preparation while retaining partial serving status.
Imaging consumes frozen settings, preserves exact guest duration values and applies
explicit scoped fields after image inference and deployment overrides. Changes to
the shared app after review invalidate a new preparation but cannot alter an
existing candidate's build inputs.

Runtime-only ownership can now prepare that same held candidate without importing
source intent or transferring source ownership. Preparation captures every live
deployment identity in the exact scope and requires a consistent immutable image
and consistent inherited deployment settings. The review includes commands,
non-secret deployment env, secret reference names, probes, startup dependencies,
sealed sidecar env, workflows, full-rootfs policy, replica floor, release command,
startup CPU opt-out and automatic rollback policy. The candidate copies these
inputs before applying explicit Git runtime fields. Sealed sidecar values stay
sealed; decrypted values never enter observation. OCI-derived reload signals and
artifact/runtime receipts remain separate from these inputs.

PostgreSQL serializes live input changes with preparation under the original
source and application locks. Inserting a candidate checks the complete live
identity set, copied inputs and current scoped intent in the same transaction.
A changed baseline requires a new reviewed plan; an already prepared candidate
retains its original inputs. Unmanaged source settings remain editable. Conflicting
live inputs or a mutable/missing inherited image leave preparation unavailable;
the complete graph remains unqualified. Replay retains frozen inputs and the
execution hold, including for candidates created before this input projection.
Frozen app projection also preserves state-only caller authorization and build
metadata while applying the guest runtime fields; it does not reduce the captured
app manifest to the guest schema.

Shared memory/PostgreSQL checks cover runtime-only preparation, unchanged source
ownership, copied inputs, retry identity, stale review rejection and unavailable
mutable/missing/conflicting baselines. PostgreSQL rejects raw changes to captured
candidate inputs while permitting OCI-derived reload metadata. Imaging projection
and populated migration replay pass. Routed API/CLI and main's clone/JSON receipt
checks pass on the integrated tree. These are local unit and PostgreSQL checks;
graph qualification, activation and native serving acceptance remain outstanding.
The inherited-input checkpoint is integrated with main through `fdd9f1c0b`,
including mirror admission outcomes and MCP discovery annotation handling.
Combined-tree candidate, populated replay, scheduler hold/mirror cleanup, store,
gateway, MCP, routed API and CLI checks pass. Schema and SQLC regeneration match;
the main and embedded OpenAPI documents match. This integration leaves the full
graph and native acceptance gates above outstanding.

These candidates are held artifacts, not qualified deployments. The current
implementation deliberately supplies no path to lift the hold. Ordinary imaging
handoff, release commands, scheduler priming/recovery, instance insertion and live
promotion cannot execute or activate them. Raw SQL cannot clear or replace the
frozen inputs. Ordinary retry cannot copy a held candidate into an unheld row.
Artifact-complete held candidates survive the stale snapshot-handoff sweep;
future graph coordination must own supersession, cancellation and retention.
Source/dockerfile builds, function/new workload preparation, scoped bindings,
host runtime consumers, qualification, activation and native acceptance remain
required before the complete environment executor is enabled.

The versioned definition, pure ownership planner, durable approval/lease/run
store, and worker coordinator are implemented. PostgreSQL and memory adapters
read scoped variables, explicit routes, inline headers/CORS policies, and
configuration consistently. Adoption binds existing app IDs and preserves their
values. Applying a plan rechecks the observation in its write transaction;
publishing convergence also checks the intent version. Variable updates stamp
runtime freshness and invalidate existing caches in that same transaction.
Overrides require a reason and expire within 24 hours; creation, removal,
controls, and adoption have a PostgreSQL audit trail. Pruning of previously
owned scoped fields preserves unmanaged fields and other environments.

PostgreSQL triggers guard scoped writes from existing API/Terraform paths,
including direct SQL mutations, and enqueue durable drift checks. Managed app
membership protects deletion while allowing ordinary runtime eviction. Existing
configuration history remains append-only when a source is attached. Deadlocks
with writers that acquire row locks before entering the trigger abort their
transaction; the durable worker retries. Source-first locking in additional
legacy write paths remains part of the concurrency audit.

Git archive reading verifies compressed/expanded size bounds, duplicate paths,
regular manifest files, the complete gzip stream, and strict definitions.
Registered API handlers verify repository identity through the installation
catalog, read exact commit bytes through githubd, and require a reviewed digest
for approval. Mutations use the existing authentication, MFA, deploy scope,
verified-email, and idempotency layers. Read-only credentials cannot mutate
source controls. The CLI carries saved review/adoption receipts to the final
action. The session-authenticated dashboard presents the definition and adoption
plan before submission and protects every form with account-bound CSRF.
Go, Node, and Python clients preserve the digest, generation, and field identity.

Apid now starts durable Git candidate discovery independently of the intent
executor. Each environment has a leased polling row; replicas claim due rows
exclusively and recover expired claims. Poll results check source generation,
lease identity, expiry, and suspension before publishing availability. The
reader verifies installation/repository identity, resolves the bound branch or
tag to an immutable archive, validates the complete stream, and checks the
definition's project/environment. Candidate commit, digest, and last successful
verification time are separate from the last check and its stable error code.
Failures retain candidate evidence and the approved definition. Manual discovery does
not change approval, generation, ownership, or reconcile work. Protected sources
require qualified merge evidence before verification and approval, as described below. PostgreSQL tests
cover competing replicas, stale claims, suspension, and approved-definition
recovery during a source outage; startup and dashboard tests cover the real
apid poller path through a Git transport fixture. CLI and typed SDK status
preserve the same distinction. Polling defaults on and can be disabled with
`FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED=false`; successful checks recur
every five minutes and source failures retry after thirty seconds.

The local githubd bridge now provides read-only protected-branch policy
evidence for an exact installation, repository ID, branch and commit SHA.
Its initial profile requires classic branch protection with approving reviews,
stale-review dismissal, last-push approval, administrator enforcement, no review
bypass allowances, and no force pushes or branch deletion. Repository identity
comes from the authenticated installation's API access. Head and complete policy
digest are read twice within a bounded observation; changes reject qualification.
Missing permissions, unavailable providers, incomplete responses and ruleset-only
protection cannot qualify. A receipt records its policy profile, identity, SHA,
digest and verification time; receivers reject mismatched, future or expired
receipts. The remote imaged-only listener cannot serve this method.

Current policy evidence is now combined with a merged pull request for the exact
candidate SHA and base repository/branch. The initial profile accepts only PR
heads in that same repository; fork heads remain unqualified. The `reviewed_merge/v1` profile requires
distinct human reviewers with current repository write/admin permission, final-PR-head
approvals strictly before merge, and no outstanding eligible changes request. It
rejects author reviews, bots, stale-head approvals, dismissals, direct pushes,
ambiguous associations, incomplete or oversized history, and policy/head changes
during the check. Equal review/merge timestamps are conservatively rejected.
GitHub attests the association between the merged PR and commit; this profile does
not independently compare their Git trees or prove historic permissions/protection.
The profile supports classic protection; ruleset-only sources remain unqualified.
GitHub App Administration read and Pull requests read access are needed for these
reads; repository metadata access supplies the current reviewer permission lookup.

For an explicit `approval_policy=protected_branch` binding, apid reads the complete
immutable archive before asking githubd for this proof. It then reads the exact
reviewed PR head archive and requires the same canonical definition digest.
Merge resolutions that change the definition are unqualified; the receipt
records this reviewed-definition digest. The source generation,
poll token, actual lease expiry, scope, canonical definition digest, and fresh proof
are checked again under the source lock. Candidate verification, immutable revision,
append-only provenance, approved pointer/generation, and durable reconciliation job
commit together. The provenance stores source/revision identities, definition digest,
approved generation, policy receipt, PR/head/merge and approving review receipts.
A retry of the current revision preserves its original receipt and running worker.
Manual approval cannot bypass a protected source; PostgreSQL also guards the pointer,
source identity, revision bytes and evidence history. Existing protected approvals
without provenance cannot be claimed, and migration replay grants no new trust.

The API, CLI, dashboard and generated Node/Python contracts expose the original
approval evidence independently from current Git availability and applied revision.
Protected binding is opt-in; manual remains the default. Failures use closed codes
`environment_git_approval_unavailable` and `environment_git_approval_not_qualified`,
retain the last verified/approved definition, and retry. For protected sources,
verification includes the declared approval requirements. These codes distinguish
review qualification from archive/transport errors. They do not change ownership or
claim serving convergence. A GitHub outage does not prevent recovery of approved work.
The local bridge provides this proof; the remote imaged-only listener denies it.

The merged-evidence read is bounded to thirty seconds, one hundred associated PRs
and one thousand reviews with one hundred entries per page. Approval receipts are
bounded to 64 KiB and one minute of freshness, including the locked write transaction.
These bounds live in the API limits registry. Core memory/PostgreSQL checks cover
proof rejection, atomic approval/work, unchanged retries, tenant-scoped evidence,
lease replacement and outage retention. Native runtime gates remain separate.

Source operational health is read as one bounded, consistent fleet aggregate
on each apid Prometheus scrape. Closed-set conditions separate unchecked and
unverified sources, stale checks, stale successful verification, availability
errors, candidates pending approval, and approvals pending application. Suspended
sources contribute only to the suspended count. Failed health reads omit counts
and ages and emit an explicit unsuccessful observation. Polling enablement is
separate from health, and replica counts use a maximum rather than a sum.
Internal alerts cover stalled checks, stale verification, and missing health
evidence; none establishes a serving outage or public status incident. The
dashboard applies the same ten-minute freshness boundary and does not label
old verification as current availability. PostgreSQL/memory tests cover startup
grace, approval/application separation, retained evidence through outages,
recovery, clock skew, suspension, and neighboring environments. Prometheus
fixtures cover the alert hold, failed/missing evidence, disablement, replica
aggregation, and recovery. The operator procedure is recorded in
`docs/runbooks/FaasEnvironmentGitSourceHealth.md`.

`make test-environment-gitops-controls` runs the strict PostgreSQL core gate,
authenticated HTTP and dashboard review/adoption/override workflows, contract
parity, and SDK transport tests. The Git transport is a fixture; the API routing,
intent store, and worker coordinator are real. Core PostgreSQL checks include
runtime cache invalidation/freshness stamps, quotas across environments, and
blocked workload pruning before any intent writes. Runtime eviction is allowed;
rejected app deletion preserves pending invocations. These checks do not replace
full staged deployment or serving-fleet/runtime acceptance.

Policy intent can now commit with a durable required-fleet effect. The apid
backend prepares every changed workload before one intent transaction, with a
shared deadline below the gateway fence TTL, then applies the gateway generations
concurrently. Every accepted acknowledgement is fenced by the current source
lease. Recovery replays pending generations before an equal intent plan can
publish convergence, keeps the original required gateways, and adds gateways
that joined after commit. A lost commit reply retains the temporary fence and
persisted progress. Source supersession does not discard post-commit effects.
Batch advisory locks use one direct database connection and share keys with
ordinary API policy writes; contended batches release partial locks before
waiting. PostgreSQL tests cover pending effects, incomplete/unrelated ACKs,
target expansion, and superseded-worker fencing. Controller tests exercise
notification failure, a fresh controller, and two-workload prepare/apply ordering.
These tests use gateway transport fixtures; native serving-fleet proof remains
required along with guest runtime acknowledgement.

Variable intent now commits with durable runtime effects. Their freshness
boundary comes from committed rows, remains stable across polling, and advances
with a new wake identity when configuration changes again. PostgreSQL enqueues
the scoped scheduler request in the same transaction as its durable effect;
notification delivery alone cannot complete the work. Recovery checks resident
acknowledged boot inputs, boot readiness, and restorable scoped snapshots before
completing an effect. Publishing convergence rejects pending runtime work or
freshly observed runtime drift. The apid backend also performs this verification
when intent already equals Git, and report mode exposes runtime drift without
requesting new VMs.

The scheduler reuses its existing fresh-boot, route convergence, and drain path
for an explicitly named environment. It leaves neighboring residents and idle
canary deployments untouched and preserves scale-to-zero. Memory/PostgreSQL
tests cover readiness, stable retries, advanced boundaries, superseded workers,
report mode, and durable outbox payloads without customer values. Controller
tests require runtime readiness after intent and fleet success, including
recovery in a fresh controller. Scheduler tests drive the scoped event decoder
and assert fresh capacity before retirement and zero stale snapshot captures.
These checks use a VM transport fixture; native runtime acceptance remains open.
Named environment variable changes now carry independent freshness stamps and
invalidate only that environment's snapshots. Default-scope variable edits and
application-shared credential changes retain a shared boundary. Snapshot
publication serializes with both kinds of stamp through the application row;
the scheduler consults the combined shared/scoped boundary before capture,
recovery, and refresh. Ordinary scoped variable edits use the same invalidation
contract. PostgreSQL checks cover two managed environments on one application,
delayed warm captures, and a publication waiting behind an uncommitted scoped
stamp. Shared memory/PostgreSQL conformance checks cover the same isolation and
default-scope behavior. Scope reads remain strict; the shared invalidation
boundary does not inject default-scope variables into named environments.

Cold boots and deployment primes now publish readiness together with an
immutable receipt of the non-secret variables sent to vmmd, the delivery
versions of staged sealed secrets, and the configuration boundary read before
loading those inputs. Receipts contain no secret plaintext or ciphertext and
are kept out of API, audit, and notification payloads. Runtime verification
compares the receipt against current committed variables and secret versions;
an instance becoming ready after a config change cannot prove freshness by its
start time. An explicit secret allowlist checks its staged references, while
the legacy stage-all path checks the complete scoped secret set. Host-key
resealing preserves delivery versions and does not invalidate input equality.

Snapshot publication locks the application and source instance, rejects an old
capture frame or stale receipt, and stores the captured receipt in the same
transaction as the snapshot. Snapshot receipts survive source-instance cleanup.
A restored process inherits its snapshot's inputs; a cold-boot fallback records
the new payload. Capture guards, rolling refresh, prime recovery, and GitOps
convergence require matching input evidence. A managed variable scope refuses
legacy snapshots or ready instances without a receipt. Memory/PostgreSQL
contract tests cover immutable acknowledgements, wrong-wake fences, atomic
readiness, actual value/version comparisons, and receipt retention. A real
PostgreSQL race admits a boot inside an uncommitted configuration window and
rejects convergence and delayed publication despite later readiness. Scheduler
transport tests cover that readiness race, inherited restore inputs, rolling
replacement, and preparation failures that release admission.

Native tests must still prove that the guest used the acknowledged payload.
Live migration now prepares runtime inputs before pausing its source. Read
failures abort preparation instead of silently dropping API variables. Vmmd
acknowledges the actual restore/cold-fallback result and the destination wake;
the scheduler preserves source inputs for a restore and uses the prepared
payload for a cold fallback. A fenced ownership transaction publishes the new
wake, readiness, destination network identifiers, and its corresponding receipt.
Older peers, unknown results, or missing wake acknowledgement remove source
evidence rather than certifying the destination. Legacy ownership commits also
advance wake identity so a late source acknowledgement cannot recreate evidence.
In-flight migrations count as pending runtime convergence. PostgreSQL/memory
tests cover atomic publication, source wake/lease/scope rejection, delayed
source captures, missing acknowledgement, and late source ACKs. Scheduler tests
drive the full handoff with restore, cold fallback, concurrent input changes,
and old/unknown replies; vmmd tests cover the actual method and wake wire fields.
Native migration and other runtime paths still require guest/serving evidence
before full environment graph support can be claimed. Start times and scheduler
notification success alone remain insufficient evidence.

Migration ownership failures now resolve under the instance row lock before
authorizing VM cleanup. A lost commit reply retains a committed destination and
its runtime receipt; a still-owned source attempt is fenced against late commits
before destination removal and source resume. Another destination wake or source
lease grants no cleanup authority. Missing/obsolete ownership uses a lease-bound
source acknowledgement rather than an unqualified instance destroy. Resolution
and acknowledgements have a detached five-second budget; database errors retain
both VMs, the destination reservation, and durable source lease. PostgreSQL tests
observe a real lock wait against an uncommitted ownership/receipt transaction,
cover commit and rollback outcomes, and reject cleanup authority on timeout.
Scheduler transport tests cover lost replies, expired requests, unresolved
database outages, newer wakes, and legacy ownership commits. Native acceptance
must still verify lease expiry, orphan destination recovery, and serving behavior.

Durable invocations capture their deployment scope at admission, including
legacy producers, and the database rejects later scope changes. Project
adoption or removal cannot reinterpret pending work as another environment.
Retries, queue dead-letter replay, and API replay preserve this identity;
keyed producer retries cannot replace it. Version pins and project release
selection use the stored scope. Scoped wakes select only matching deployments
and instances, and the wake coordinator shares results only within a scope.
App deletion still releases every scope, and app/account admission limits
remain shared. Automatic rollout overlap requires a counted predecessor
revision in the same scope. Ledger admission, warm promotion, and restart
reconstruction retain that scope without adding database reads to the wake
hot path. The gateway recovers the internal scope from durable admission after
the HTTP hop and verifies the target deployment, instance, and wake before
forwarding. Queue batches carry the durable invocation UUID and claim attempt
in explicit internal fields, independently of broker headers and metadata.
Admission requires a queue or delayed-task row in dispatching state, the current
attempt, a live lease, and the matching app. Legacy broker records retain their
synthetic identities. Retries remain at least once; checking the claim at
admission does not cancel a guest request already in flight when a lease ends.
Queue bindings and trigger/scaler projections remain app-scoped. These routing
prerequisites do not enable their GitOps adapter.

API binding mutations now publish the binding and its private consumer in one
store transaction. Creation, updates, deletion, and the binding portion of the
queue workload profile use this path. PostgreSQL scheduler notifications commit
with the rows. Failed projection or deletion rolls both intents back; queue
renames update the existing trigger and retain its receipt namespace. Consumer
admission and ordinary trigger creation share the account quota lock. Shared
MemStore/PostgreSQL cases cover conflicts, quota denial, receipt preservation,
and concurrent account admission; a PostgreSQL fault test covers rollback after
consumer deletion. The profile's scaling policy remains a separate mutation.

Private consumers now carry a typed, immutable binding UUID with a composite
tenant/app foreign key and a unique projection per binding. Public trigger
create/update cannot supply the reserved ownership marker; direct update,
pause, resume, and delete of an owned consumer must go through the binding API.
The migration adopts only a single historical projection with matching tenant
identity and mode. Ambiguous markers retain their rows and receipts for review,
and the scheduler reports an unowned marker instead of consuming through it.
Claims lock and recheck the live binding and trigger in mutation lock order;
disabled, renamed, or removed consumers cannot claim through a cached trigger.
An already admitted request remains at least once.
OpenAPI and the generated Node/Python clients expose the ownership conflict.
Their pause/resume success contract now reflects the server's existing 200
response containing the updated trigger.

Worker queue demand now reads the environment captured on each invocation.
The scheduler derives a separate target for each live environment, retains the
newest worker generation in that scope, and drains only its surplus or retired
workers. Explicit pool mutations require a valid scope; the legacy entry point
is restricted to the producer's default environment. All scope/demand reads
finish before teardown, so missing observations preserve the fleet. A selected
migration holds reconciliation instead of granting the scaler VM ownership.
App concurrency and node admission still use their shared ledgers, and account
worker sizing credits only the selected pool's current workers. Creating a
resident worker now locks its account, reads the current plan and resident
worker count, and inserts inside the existing node admission transaction.
Admissions across applications, environments, and nodes cannot spend the same
slot. Migrating and draining workers retain account capacity until they are
nonresident. Failed creation rolls back both reservations; termination releases
the account slot through the instance state. The worker wake and prime paths
refuse before contacting vmmd when account capacity is exhausted. Shared-store
cases and a real PostgreSQL cross-node admission race cover this contract.
Worker mode is immutable for the lifetime of an instance. A stopped or parked
worker must reenter through fresh creation and account admission. A PostgreSQL
trigger guards all state/mode writes, including readiness publication, while
the memory store mirrors that contract. Rejected publication preserves the row
and publishes no runtime input receipt. Ordinary mirror classification remains
supported, and an already resident worker can become ready, migrate, and stop
without acquiring a second account slot. Replay-safe migration and shared-store
checks cover legacy mutation helpers and publication paths.
Scale-out and scale-in cooldowns use retained admission and termination history
in the selected generation, respectively, so a recently active neighbor cannot hold a cold environment. New generations
can start without inheriting their predecessor's cooldown.

The production targets adapter now exposes the worker reconciliation path;
previously it could fall through to request admission, which rejects workers.
Ticks and lifecycle notifications use the same scoped demand. Per-binding caps,
worker replica bounds, and multiple queue targets apply to that demand. An
app-wide broker lag reading applies only to the producer's default environment;
other scopes need local queue depth or a future scoped broker signal. Missing
lag is not an empty queue. Application-wide custom/in-flight signals do not
authorize environment worker scaling through this path; their scoped signal
contract remains open. Existing app/binding queue gauges remain aggregate
telemetry. Winning-signal counters require an actual scoped admission, and
account-capped demand records a refusal rather than an idle observation.
Shared memory/PostgreSQL cases cover exact name/scope matching, leases, retained
backlog after project removal, completed-history exclusion, and retained worker
cooldown history isolated by deployment generation. Scheduler tests
cover per-scope scale-in/out, account-budget sharing, retired generations,
observation failure, migration holds, scoped cooldowns, binding/replica caps,
and real PostgreSQL intent through a VM transport fixture. Native guest and teardown proof is still
required.

Named queue bindings and their private consumers now retain the immutable
catalog environment UUID and deployment scope. Their name uniqueness includes
that UUID, so production and staging can use the same names, and one environment
can have several independent consumers. Historical bindings keep the empty,
application-shared scope and its existing singleton consumer contract. Public
scope selectors and the reviewed ownership adapter remain required before
enabling the queue adapter. The scoped demand path does not scope the
application-shared runtime/scaling policy or transfer queue ownership.
Renaming or pruning a binding also needs an explicit disposition for queued
work and existing trigger receipts. Public binding deletion now retires its
intent and disables its private consumer in one transaction, retaining the
binding ID, pending work, dead letters and delivery receipts. Switching from
push to pull retains the same disabled consumer; switching back to push must
recheck quotas and reuses its receipt namespace. Disabled active push consumers
still consume quota, while retained pull and retired consumers do not.

Retirement holds new queue admission and dispatch under a binding row lock,
including cached queue pollers, direct claims and keyed claims. Existing
in-flight deliveries can finish; retries and dead-letter replay keep their
stored identities and wait for recovery. New receipt claims also take that
parent lock and cannot acquire another generation while the binding is held.
Queue diagnostics continue to show retained backlog, while worker demand
excludes retired bindings even after the last active binding is removed.
An environment with a live delivery lease from a retired binding keeps its
current worker pool until the lease finishes or expires. That hold cannot
admit another worker, alter a neighboring environment or replace a generation.

Queue names remain reserved by retained binding rows. Storage guards reject
physical deletion of retained bindings and independent deletion of private
consumers, including through older write paths; permitted parent-app cascades
retain their existing invocation retention requirements. Ordinary PATCH and
recreation cannot silently release a retirement hold or give another consumer
the old backlog. Tenant-scoped internal history reads expose retained identity
to the future adapter; an explicit reviewed recovery/adoption operation,
public environment selectors for producers and bindings, and a reviewed retention policy
remain gates before enabling GitOps queue pruning. Binding retirement alone does not grant GitOps ownership.

Accepted queue messages now retain an immutable binding ID plus their original
queue label. Admission captures the observed tenant-matched binding, then locks
its immutable ID so a concurrent rename or disable cannot orphan that work; ordinary unnamed work captures its unique active private consumer when
one exists. Keyed unnamed work remains unassigned. Retry and durable queue
replay preserve identity, and binding caps, status, worker demand and retirement
lease holds include work accepted under earlier names. A replacement that reuses
a renamed label cannot claim messages pinned to the original binding. Cached
consumer claims still require the live parent and current consumer projection;
partial batch release also fences the original invocation attempt and owner.
Generic HTTP replay rejects bound queue messages with
`queue_replay_requires_binding`; the app queue dead-letter replay endpoint
retains the original row and work policy instead of orphaning its delivery lane.

Durable queue recovery now rearms the invocation and its original consumer
receipt in one transaction, retaining receipt UUIDs, captured scope, work policy,
payload and failure audit. Repeated failures refresh the current dead-letter
projection and retain earlier details in its failure history. Invocation replay, receipt retry and their unified
failed-event surfaces all use this recovery path. The customer retry counter
restarts at zero, while a separate ledger-owned replay generation increases;
queue claims, partial release, acknowledgement and batch HTTP admission fence
both values. Each dispatch captures its delivery handles so a later lease
recovery cannot change the fence used by an unfinished callback.
Disabled and retired parents still hold rearmed work until an authorized
activation or recovery releases them. Historical replayed work receives a
nonzero generation on initial upgrade so old envelopes cannot resume it. Migration
replay preserves current generations and receipt leases. This qualifies queue
recovery through PostgreSQL, receipt claims and HTTP dispatch; native guest
execution and environment-scoped queue ownership remain separate gates.

The additive migration captures historical ownership only from an existing
private consumer receipt or a current name whose last binding update predates
admission. Conflicting proofs and ambiguous history stay unassigned. Neither
migration replay nor ordinary SQL updates can adopt those rows later. Unassigned
legacy work retains the current name-based compatibility path and diagnostics;
named environment consumers require an exact captured binding ID and never
use that fallback. These IDs do not grant GitOps field ownership or release
retirement; the scoped source-of-truth contract and explicit recovery remain open.

Admission prefers a binding in the current catalog environment over a legacy
shared binding. An unnamed message captures the unique applicable consumer;
unavailable original environments do not create ambiguity for a new consumer.
Scoped receipt insertion rejects work owned by another binding or legacy work
without an ID. Candidate enumeration, concurrency caps, direct invocation
claims, receipt claims, and worker demand use the same scope contract. Existing
aggregate queue gauges sum same-name bindings rather than overwriting a neighbor.
The legacy queue-workload profile selects only its application-shared default.

Deleting an environment retains its bindings, captured work, and receipts.
Recreating the same slug produces a new UUID and cannot release that work or
give a new consumer its backlog. New binding and consumer UUID fields are
immutable; dispatch and new receipt generations hold when the original catalog
identity is unavailable. Admission and receipt claims lock the live catalog
identity to serialize with removal. Independent guards remain installed while
out-of-order replay replaces older admission/retirement functions. Shared
memory/PostgreSQL cases cover deletion/recreation and tenant/scope isolation;
real PostgreSQL scheduler checks cover separate caps, rename, replay and receipt
retention. A populated-database migration check exercises the older-function
interval and replays the twenty-four GitOps migrations alongside six additive
migrations merged from main, preserving captured identities and current leases.
These checks qualify the internal scope contract. They do not establish GitOps
queue ownership/recovery or complete environment graph support.


Customer queue bindings can now select a registered project `environment` at
creation. The response and binding status carry its original catalog identity;
updates cannot move a binding between environments. Queue sends and application
inbox messages accept the same selector and pin the observed binding before
admission. An explicit environment requires an enabled environment-owned
binding. Omitted selectors retain the default deployment scope and compatible
shared queues; exact environment bindings take precedence, including retired
bindings that hold work for recovery. Name inference considers only eligible
bindings in the selected namespace. Admission rejects deletion/recreation races
rather than accepting work against a replacement identity. Inherited Flags
context retains the originating customer and decision evidence independently of
the selected target environment.

Go, generated Node/Python SDKs, and CLI sends preserve the selector and returned
binding identity. The CLI lists each binding's environment, and application
manifest deployment reconciliation ignores named-environment bindings when
matching or pruning legacy queue declarations. Binding status reports a paused
`environment_unavailable` consumer when the original catalog identity is gone.
Queue completion notifications include the captured deployment scope and binding
ID. Application-wide keyed-work policies currently reject named-environment
bindings; environment-specific coalescing/fairness needs its own adapter before
that gate can be enabled. HTTP tests cover default/explicit selection, immutable
identity, release graph capture, retained backlog, retirement, and an admission
race with inherited flag context. PostgreSQL-backed HTTP tests exercise the
same catalog and admission guards; SDK tests use transport fixtures. These
selectors do not start the approved-intent executor or transfer GitOps ownership.

Scoped queue intent now participates in the consistent observation and reviewed
ownership plan. Adoption records the original binding UUID and preserves values,
consumer UUIDs, accepted work and receipts. Approved creates and updates reuse
the atomic binding/consumer publication transaction, with source lease fencing,
account quotas and committed scheduler notifications. Queue label changes retain
the original delivery lane. Memory transactions preflight every queue operation
on detached maps; PostgreSQL rolls back all intent when a later queue operation
fails. Explicit environment bindings take their own source ownership; legacy
shared queues and neighboring environments remain unmanaged by this source.

Enforce-mode ownership guards cover binding intent and private consumer intent,
including direct SQL edits. Report-mode edits schedule durable drift checks.
Temporary overrides preserve the incident value, prevent a converged run and
restore approved queue intent when removed or expired. API mutations acquire the
source lock before app/account/binding locks, and return the existing
`environment_field_git_managed` conflict for rejected writes. Retired bindings,
missing original identities and inconsistent private projections are blockers;
Git cannot implicitly recover them. Reviewed queue retirement and recovery are
described below. The approved
intent executor remains disabled in apid pending complete graph integration.

Shared memory/PostgreSQL tests cover scoped adoption, report drift, stale
reviews, label changes, receipt retention, creation, overrides, quota rollback,
and pruning/recovery holds. PostgreSQL tests cover SQL guard bypass attempts and
a real source-lock wait against a competing API mutation. Migration replay
preserves queue ownership, accepted work, receipts and current controller leases.
HTTP checks cover the public ownership conflict. These tests qualify queue
customer intent; native dispatch and full staged graph acceptance remain open.

An approved definition can now explicitly select `queue_pruning_policy: retain`.
With source pruning enabled, removal of an owned queue retires admission and
dispatch in the same transaction as its consumer disablement and ownership
release. The original binding, consumer, accepted work, replay generations and
receipt namespace survive. Neighbors and application-shared queues remain
unmanaged. Already retired rows preserve their original retirement timestamp;
report mode and active overrides do not release or retire work.

Restoring a retained queue requires `queue_recoveries`, keyed by its declared
binding name and containing its original canonical scoped binding UUID. The
retirement appears as `null` in the adoption preview, so adoption transfers
ownership while preserving the hold. Subsequent enforce-mode reconciliation
checks the exact approved revision, source generation, controller lease, catalog
identity and ownership before releasing it. A missing/wrong UUID, replaced
catalog identity, or inconsistent consumer projection blocks the complete plan.
Recovery preserves the original private consumer and receipts; it does not
reinterpret queued data. Recovery metadata contributes to the reviewed definition
digest, without inventing a managed setting or perpetual drift after activation.

Retirement and recovery reuse atomic binding/consumer publication and committed
scheduler notifications. Retiring removed consumers precedes admission of new
ones in the same transaction. Recovery rechecks application/account trigger
quotas before releasing the hold. A later quota, projection or field failure
rolls back the complete intent transaction. PostgreSQL guards require the live
approved controller lease even for direct SQL recovery, reject superseded
generations, and permit only hold release in that guard transition; ordinary
PATCH/recreation cannot recover a retired queue. Native delivery and the complete
staged environment release remain separate gates.

Retrieve its original identity with
`gregale queue bindings list shop-worker --include-retired` or
`GET /v1/apps/shop-worker/queue-bindings?include_retired=true`. This read-only history
view uses the normal app read authorization and includes `retired_at`; default
listing continues to include only active bindings. Copy the retained binding ID
into `queue_recoveries` in the reviewed definition. Valid compact UUIDs returned
by the memory implementation are normalized before computing the review digest.

For example, a reviewed recovery definition retains the original delivery lane:

```yaml
api_version: gregale.dev/environment/v1
project: shop
environment: production
queue_pruning_policy: retain
workloads:
  worker:
    app: shop-worker
    queue_bindings:
      orders:
        queue_name: orders
        mode: push
        workload_class: worker
    queue_recoveries:
      orders: 11111111-2222-4333-8444-555555555555
```

The UUID comes from the retained binding history; an arbitrary UUID cannot
qualify. Review and adopt the plan before reconciliation can resume the queue.
Omitting `queue_pruning_policy` continues to block queue removals.

The branch's unreleased migrations are replay-safe as a complete set. Their
rollback retains management intent, ownership, captured work and runtime
receipts. Historical capture runs only during initial installation; replay
cannot reinterpret an invocation's environment or adopt a later consumer
marker, and polling backfill preserves existing claims and schedules. A
populated-database recovery check verifies these identities through replay of
the GitOps set together with the six additive migrations merged from main. The
combined-tree invocation check retains environment identity, pinned failure rules,
and completion classification through admission, claim, and completion.

Environment secret reference intent now stores destination-to-source names under
an immutable catalog environment UUID, independently of sealed values and delivery
versions. Observation and reviewed adoption read names only. Adoption preserves
existing reference values; enforce reconciliation changes owned destinations and
leaves neighboring environments and secret ciphertext intact. Missing source names
and plaintext variables that shadow a desired destination block the whole plan.
References share the cross-environment variable quota, including reviewed removals.
Report writes schedule durable drift checks; enforce ownership covers ordinary and
SQL writes, with the existing reasoned temporary override contract.

The primary workload resolver selects the named source in the exact deployment
scope and delivers it under the declared destination key. Environment reference
intent overlays individual deployment references. Legacy automatic delivery keeps
unmanaged secrets, while sidecars retain their explicit secret selections. Invalid
persisted references fail before an overlay can hide them. Cold wake, prime,
migration cold fallback and application task projections use the same decoder;
restored instances retain captured reference evidence. Delivery audit versions use
actual source names and deduplicate repeated aliases. The VM transport carries
separate source and destination keys. Alias envelopes must contain exactly the
selected source; decrypted staging emits only declared destination names. Before
an alias boot, the client requires node support and checks the serving handler's
response again. Missing acknowledgement prevents runtime publication and triggers
bounded teardown, including a node replacement between discovery and boot.

Instance and snapshot runtime receipts now retain the actual destination mapping
as well as sealed source versions. Current timestamps and versions cannot qualify
a wrong mapping, a shadowed destination or an older receipt without managed
reference evidence. Reference mutations commit scoped freshness stamps and cache
invalidation; the effects transaction retains scoped runtime work. Shared
memory/PostgreSQL cases cover adoption, stale review rejection, report drift,
enforcement, quota/pruning, catalog recreation, overrides, immutable receipts and
serving convergence fences. Scheduler transport fixtures cover aliases, legacy
inputs, sidecar isolation, wake, prime and migration evidence. Populated replay
preserves references, ownership, receipts and active controller leases across the
complete additive migration set. These checks qualify the internal reference
contract; native guest delivery and lifecycle acceptance remain required. Legacy
deployment reference adoption is described below; native runtime evidence remains
part of the complete adapter gate.

Environment clones now copy the current destination-to-source references under
the new catalog environment UUID alongside their scoped sealed sources. They
preserve incident or pending-drift values rather than applying the source's
approved definition. Git source bindings, field ownership, overrides and runtime
receipts remain attached to the original environment. References share the
cross-environment variable quota during clone preflight; concurrent clones
serialize through source-first application locks and cannot spend the same
remaining slots. Reference copy failure rolls back the catalog, values and
freshness stamps in the same PostgreSQL transaction. Clone saga compensation
also removes the new references, so recreation of its slug cannot inherit them.
The API, CLI and generated Node/Python clients report a separate
`secret_references_copied` count; generated clients accept older responses that
omit it. Shared-store cases cover quota rejection, current-value preservation,
independent ownership and rollback/recreation. PostgreSQL cases exercise an
uncommitted source write, a forced copy failure and concurrent quota admission;
HTTP and SDK checks cover the public count and transport compatibility.

Public reference controls now expose GET/PUT/DELETE under
`/v1/apps/{slug}/secret-references`, with an explicit `environment` query parameter.
Responses contain the catalog UUID, destination/source names and shared quota
usage; they never read sealed values. A mutation captures the catalog identity
within its request, and the store checks it under the write locks. Replacing the
environment under the same slug during that request returns a conflict. Writes
require an existing source in the exact scope, reject plaintext shadows, preserve
sealed values and use the same Git ownership/temporary override contract as the
internal adapter. Quota problems report the actual attempted count.

The CLI offers `gregale secrets refs list|set|unset` with explicit `--app` and
`--environment` flags. The app environment dashboard lists names and forwards its
CSRF-protected forms through the authenticated JSON handler. Its verified session
identity is preserved, with the API's scope/MFA checks. The Go, Node and Python
SDKs expose the same operations. Routed HTTP, dashboard session/CSRF, CLI and SDK
transport checks cover this customer flow; memory/PostgreSQL checks cover catalog
replacement, exact source scope, ownership and temporary overrides. OpenAPI AST
parity and the Go SDK coverage gate include the new contracts. SQLC regeneration
matches the committed query output. The repository-wide OpenAPI lint still has
14 pre-existing errors, also present before these paths were added.

At this checkpoint, these controls accepted environment names of 3–33
characters, matching the intersection of the catalog and sealed-secret scope contracts. Catalog names
of one or two characters were an integration gate; the dashboard identified
those unavailable names without hiding supported environments.

Legacy reference adoption now observes every live deployment in the exact scope,
including the automatic same-name delivery baseline. It compares effective maps
only after validating each persisted deployment definition and applying scoped
intent. Conflicting live canary maps block the complete plan until the rollout
finishes or is aborted. The reviewed hash includes the sorted live deployment
identities; a replacement with identical names still requires a new review.
Adoption materializes only the selected current names into scoped intent before
transferring ownership. It preserves the existing source mapping rather than
applying the approved replacement. Imported references spend the same shared
variable quota as ordinary scoped references. Existing suppression is observed
as a `null` reference value and also requires reviewed adoption; adoption preserves
that absence until reconciliation applies the approved mapping later. Live
baseline mutations serialize with adoption and enqueue durable checks; writers
that acquired legacy locks first may be aborted by PostgreSQL's deadlock detector
and must retry.

Ordinary reference removal and reviewed Git pruning now retain durable destination
suppression under the environment UUID. Suppression removes a key from the primary
workload's original deployment map and automatic delivery without deleting the
sealed source. Sidecar selections remain separate. Setting an explicit reference
clears suppression atomically. Removed Git fields release ownership under the
existing pruning contract; the retained suppression remains current environment
intent until an explicit reference re-enables it. Suppressions do not consume
variable slots, but their retained names are bounded to 1024 per application across
environments by the API limits registry. Clone preflight includes this separate
bound and copies current suppressions to the new catalog identity; rollback and
catalog deletion remove them. PostgreSQL guards mutually exclusive positive and
negative intent, immutable identity and the existing ownership/override contract.

Scheduler reads combine references and suppressions at one statement snapshot.
Runtime receipts must omit every suppressed destination and, in a scope with
suppression, contain exactly the source versions selected by the reported mapping.
An older stage-all receipt containing only current versions cannot prove removal.
Suppressed intent continues to require runtime receipts and serving observation
after a pruning effect completes. API/CLI/dashboard and typed clients expose only
the retained names through optional `suppressed_keys` metadata. These changes apply
to future cold wakes; ordinary controls do not promise immediate resident refresh.
Native guest delivery, full graph release activation and the executor's remaining
adapter gates are still required before enablement.

The suppression checkpoint covers shared memory/PostgreSQL adoption, pruning,
runtime proof, the retained-key bound, clone rollback and catalog recreation.
Deleting catalog intent advances the surviving application's scoped boundary and
invalidates captured snapshots, including when no sealed values remain. Populated
migration replay preserves both positive and negative intent, ownership, receipts
and an active lease. Routed HTTP/dashboard, CLI, scheduler wake/sidecar and
Go/Node/Python SDK checks pass. SQLC regeneration matches the query output and the
main and embedded OpenAPI documents match. These are focused unit and PostgreSQL
checks; native guest/lifecycle acceptance is still outstanding. The schema dump
also includes the already merged scenario chaos and exclusive schedule columns
that the preceding snapshot omitted.

The integration checkpoint includes main through `72893dc28`. After combining
its protocol, SQL and client sources, regeneration and focused GitOps/store,
HTTP/dashboard, CLI, scheduler alias and Go/Node/Python SDK checks pass. The
complete `vmmdgrpc` and `fcvm` unit suites also pass. This is unit and PostgreSQL
evidence; native guest delivery and lifecycle acceptance remain outstanding.

The remaining full feature gates include native qualification of protected-branch
approval with the complete serving flow; environment-scoped workload creation, source/runtime,
short catalog-name scope support and
service-binding adapters; queue projection repair; staged graph qualification
and release activation; native serving-fleet and guest runtime evidence;
full staged graph/runtime operational status integration; and native runtime acceptance.
Unsupported resource fields currently block the complete plan. The approved-intent
executor is not started from apid until these integration contracts are wired;
candidate discovery runs independently. The opt-in report controller described
below observes approved intent without enabling enforcement.

The reviewed queue retention/recovery checkpoint passes shared memory and
PostgreSQL reconciliation, raw-SQL authority and supersession fences, quota and
projection rollback, and populated migration replay with retained work, receipts,
ownership and a current controller lease. Routed API history checks cover the
explicit selector, original timestamp, authentication, read scope and neighboring
tenants/apps. CLI and Go/Node/Python SDK checks preserve the recovery identity and
timestamp. Isolated SQLC regeneration and the embedded OpenAPI copy match. The
public Go checks used the internal linker after the normal external linker ran
out of workspace disk; native guest/lifecycle and staged graph acceptance remain
outstanding, and this checkpoint does not enable the approved-intent executor.

This checkpoint is integrated with main through `a3e1800e3`. Focused routed
GitOps/queue and policy-retirement API/CLI checks, the PostgreSQL GitOps suite,
populated migration replay and Node/Python transport checks pass on the combined
tree. Isolated SQLC regeneration, matching OpenAPI copies and ADR number
uniqueness pass. The contract is now ADR-430 to keep the number unique alongside main's
handled-service-request ADR-429; historical migration comments retain their
original ADR references.

Main's subsequent request-streaming gateway fix through `650574079` is also
integrated. Its duplex/body-admission tests and focused routed GitOps/queue API
checks pass on this tree. It changes no GitOps SQL or client contracts; the
preceding PostgreSQL and transport qualification remains applicable.

For an existing scoped secret, the public reference commands are:

```sh
gregale secrets refs set --app shop-api --environment production DATABASE_URL=secret:DATABASE_PRIMARY
gregale secrets refs list --app shop-api --environment production
gregale secrets refs unset --app shop-api --environment production DATABASE_URL
```

Reference edits invalidate the selected environment's snapshot cache and apply
on a future cold wake. These commands do not assert that existing serving
processes have switched their runtime configuration.

Workload membership must be explicit. `workloads: {}` represents an empty
environment or one that manages only environment configuration; a missing or
null map is rejected. Removing the last workload produces the same reviewed
prune candidates as any other removal, preserves unmanaged resources and
other managers, and remains blocked by the workload pruning adapter gate.

Source and Dockerfile preparation now persists the exact approved source commit
separately from its directory/Dockerfile parameters. A commit-only change therefore
produces reviewed drift even when the environment definition is unchanged. This
scoped source revision has the same ownership and original environment identity
as the other workload settings; it records intent rather than serving evidence.

Preparation first verifies every mapped workload and the current reviewed plan,
then requests only missing source artifacts. The API streams the pinned commit
through the bound GitHub installation/repository, applies plan archive limits and
existing source validation, verifies the approved definition digest and exact
member/Dockerfile paths, and hashes the complete archive. Each artifact is bound
to its revision UUID, commit SHA and definition digest. The existing filesystem
or remote source transport stages the archive before a source-first transaction
publishes the held deployment and durable queue row under a reserved UUIDv7
build ID. A failed queue publication rolls back the candidate. A lost reply is
recovered from the existing candidate/build identity without downloading again;
a superseded lease or an artifact from another revision cannot publish work.
Remote source objects use the existing build-aware retention. Local spool retention
and graph-owned cancellation still require the complete operational qualification.

Builderd consumes the candidate's frozen app baseline and explicit source selection,
including clearing a shared Dockerfile for autodetected source builds. Dockerfile
selection cannot fall back to the repository root. The API scans that same
selected file for unsupported persistence directives and rejects a missing or
oversized Dockerfile before publishing work. Builds retain the existing
builder microVM, slot and plan boundaries, and artifact completion does not release
the workload hold. Shared manifest environment/service bindings, function or new
workload creation, inherited non-image runtime-only sources, scoped dependency
qualification and release activation remain gates. Native Linux KVM serving,
`test-metal` and `leakcheck` evidence are still required before enabling the
approved-intent executor.

Source preparation qualification covers the HTTP review/adoption/build flow,
commit-only drift, substituted archives, retry without another download,
frozen builder runtime/Dockerfile inputs, PostgreSQL queue rollback and raw SQL
mutation fences. Successful source and Dockerfile builds preserve the frozen
inputs, provenance and execution hold; a lost imaging notification is recovered
from committed build/artifact records in both memory and PostgreSQL.
The selected Git definition, builder, source transport, API,
PostgreSQL integration, migration replay, imaged and scheduler tests pass on
macOS. These checks do not substitute for native KVM serving qualification.

The merged checkpoint includes main through `46c33b276`. Focused Go checks for
the above packages plus gateway, MCP hosting, CLI and gatewayd-internal pass;
the Node MCP starter's 33 tests also pass. SQLC output, the embedded OpenAPI
copy and ADR number uniqueness remain consistent.

## Durable graph preparation journal

Candidate publication now atomically records one complete reviewed workload
cohort. The journal binds the approved definition digest, original source and
environment UUIDs, generation, intent version, reviewed plan hash, mapped app
IDs, candidate deployment IDs, retained live deployment IDs and observed resource
identities. Queue bindings retain their original IDs and private consumer
namespaces. Journal identity and inputs cannot be rewritten or ordinarily deleted.
Deletion of the original source or environment releases the journal through the
existing project/account purge transaction.

The current lease reconciles artifact progress across the entire cohort. One
finished image cannot mark a graph prepared while another image or source build
is incomplete. Preparation is durable and idempotent; retry preserves the cohort
and its preparation timestamp. A failed member stops the graph. Superseded source
authority cannot advance its old journal. A failed graph write rolls back all
candidate, build and durable imaging publication in the transaction.

Prepared means every managed candidate has its rootfs artifact in the expected
preparation phase. It is separate from qualification, release commands, instance
admission, queue delivery, activation and serving convergence. All execution holds
remain in place, and preparation alone cannot advance the applied revision.
The next graph stage must introduce separately fenced qualification and activation
authority while preserving frozen inputs across scheduler and runtime consumers.

Local memory/PostgreSQL checks cover a reviewed API/worker/queue cohort, artifact
completion, retry identity, rollback of durable handoffs, SQL input/phase/deletion
fences, substituted queue/consumer IDs, supersession, populated migration replay
and project/account purges. Native Linux KVM lifecycle acceptance remains required
for the complete feature; the approved-intent executor remains disabled.

## Qualification publication and attempt authority

After the whole cohort is prepared, apid atomically publishes one qualification
request per managed candidate and its durable notification. Requests bind the
original graph, app, logical workload, deployment, immutable rootfs artifact,
frozen build/runtime inputs and reviewed execution mode. A lost publication
response reuses the same identities. A failed durable handoff rolls back every
request in the cohort. Notification payloads contain only the four work identities;
execution tokens and frozen inputs have no public JSON projection.

Runtime owners claim a separate bounded lease rather than borrow the controller's
intent lease. Claims and renewals re-read the complete approved graph under source
and app locks, including inherited settings and all candidate artifacts. A changed
sibling, failed artifact, superseded revision, suspended source or expired attempt
revokes the lease. Releasing the apid controller lease retains approved runtime
work. Each non-job attempt reserves a fresh instance identity, retained on renewal.
Recovery cannot reuse that identity or replace an instance still in an active
lifecycle state. PostgreSQL guards request identity, cohort completeness, lease
duration, worker/attempt substitution and ordinary deletion, including raw SQL.
The original graph's parent purge releases its requests.

Queued and claimed remain work states. They are not qualification receipts and
cannot create instances through ordinary paths, mark deployments live or advance
the applied revision. The VM consumer, frozen host lifecycle inputs,
release-command execution, native readiness/smoke evidence, qualified snapshot
publication and graph activation remain required. The approved-intent executor
stays disabled until the complete native serving and recovery gates pass.

Local memory/PostgreSQL and apid checks cover unprepared cohorts, stable retry
identity, frozen-input isolation, lease expiry/replacement, sibling artifact and
inherited-setting changes, SQL lease/cohort guards, durable publication rollback,
populated migration replay, parent purges and the HTTP-reviewed source artifact
handoff. These checks do not establish native KVM qualification.

This checkpoint integrates main through `445cc397e`, preserving managed Commit
operations and distributed mirror slot coordination. Focused PostgreSQL, apid,
scheduler, database outbox, migration and gateway checks pass locally; schedd
compiles. Regenerated SQLC output, the embedded OpenAPI copy and ADR number
uniqueness checks pass. The configured native acceptance project's API currently
reports that the project is suspended, so the required KVM serving gates remain
unverified.

## Qualification instance admission

Schedd has a dedicated store capability to reserve the exact instance identity
issued by a current non-job qualification attempt. Admission rechecks the full
reviewed observation and artifact cohort, then applies the ordinary node RAM
reservation, account worker quota and service capacity protection. It also
requires the account and node to remain eligible and the requested RAM to match
the current app. Source/app locks precede the existing node/account reservation
order. Jobs cannot use this VM admission capability.

A lost response returns the same reservation with `Created=false`; changing its
node, wake, memory, mode or attempt cannot reuse it. A retired reservation cannot
be booted again by that attempt. Ordinary lifecycle writers cannot publish
runtime identity, readiness or runtime-config evidence on the held candidate.
Its app, deployment, node, wake and resource identity stay immutable. Terminal
cleanup remains possible after expiry and supersession through the immutable
execution frame and its separate cleanup capability. Direct deletion and attempt
replacement require durable retirement of that frame, in addition to the
existing unexpired-attempt retention guard. Project deletion revokes requests
and detaches apps according to the existing project contract, while preserving
unfinished execution holdings. App and account purges cannot remove those
holdings before retirement.

This admission capability records `cold_booting` only. It does not call vmmd,
run a release command, produce a qualification/snapshot receipt, activate a
deployment or advance the applied revision. Native execution must still use
the frozen host contract, revalidate the attempt before each effect, atomically
publish its bound input evidence, and retain recovery ownership until the VM
has actually retired. The approved-intent executor remains disabled.

Local memory/PostgreSQL checks cover stable admission retries, cancellation,
node and account worker capacity refusal, ordinary runtime publication fences,
attempt expiry, account ineligibility, sibling changes, immutable reservation
identity, duplicate reservation refusal, deletion/recovery and original parent
cleanup. The broad GitOps suite, focused state/scheduler regressions, populated
environment-intent migration replay, regenerated SQLC comparison and repository
ADR number gate pass. These checks do not establish native VM qualification.

## Qualification runtime publication and execution window

Schedd can atomically publish a held candidate's `running` identity and the
non-secret runtime input receipt under its exact qualification attempt. Both
stores require the durable dispatch mark and recheck the complete prepared graph,
lease, reserved instance, node, wake, memory, execution mode, deployment scope
and delivered input freshness.
An identical retry preserves the incarnation timestamp; a replacement netns,
node, wake or input receipt is refused. PostgreSQL fences raw receipt writers
as well as the dedicated store method. Receipt failure rolls back readiness.
Publication preserves the startup CPU reservation used to rebuild the ledger.
The runtime receipt grants no graph qualification or activation authority.

Intent job claiming also rechecks due time, desired generation and lease
eligibility on the updated job row. The source lock preserves lock ordering;
it cannot refresh a joined job's earlier eligibility snapshot. A contender
whose candidate snapshot predates another worker's commit cannot replace the
newly issued lease or supersede that worker's run.

The scheduler execution primitive materializes the frozen contract through the
same boot-payload builder as ordinary deployment priming. Explicit scoped port,
HTTP readiness path and gRPC health settings take precedence over inherited
deployment metadata, matching the imaging order. The candidate consumes normal
app concurrency, node memory/vCPU/CPU and existing account worker/service limits;
only the ordinary reviewed replacement overlap is allowed. The app lock covers
admission and preparation, then releases before VM RPCs and evidence checks so
existing serving wakes can continue.

One execution window is bounded by the supplied claim deadline; it does not
extend its own lease. Authority is rechecked around boot, publication and the
evidence visitor. A periodic check cancels in-flight work when the graph,
account, app owner or admitted node becomes ineligible. Cleanup uses a detached,
bounded attempt-aware retirement context. Retirement requires native process
and resource evidence for the exact persisted execution frame, or proof that
the frame was never dispatched when its durable dispatch mark is still false.
Generic Destroy success, NotFound and terminal instance state cannot provide
that proof. The retirement receipt and terminal state commit together before
capacity release. Uncertain retirement keeps the active row, persisted CPU
reservation and ledger charge for recovery. Reinvoking the
same window cannot replay a boot on an existing/retired reservation.
Admission and retirement app-lock waits respect their execution and cleanup
deadlines. If physical destruction succeeds but the retirement lock cannot be
acquired, the active row and capacity charge remain until recovery completes
the durable retirement; an expired attempt cannot replace that reservation.

This primitive is not wired to the durable qualification notification consumer.
Its visitor does not yet implement release commands, native smoke/snapshot
qualification, isolated worker/queue qualification or graph activation. Those
adapters must fence evidence publication and recovery against the same attempt;
successful return from the execution window is insufficient serving proof.
Host lifecycle and queue consumers still require their complete scoped contract
before wiring the consumer. The approved-intent executor remains disabled.

Focused memory/PostgreSQL and fake-VMM checks cover all non-job modes, custom
port/readiness materialization, receipt atomicity and raw SQL fences, source
supersession, cancellation, lease deadline, visitor/boot failures, node/account
ineligibility, changed memory, physical retirement and admission ledger reseeding
after an uncertain destroy. These are local contract/ordering checks; native
`test-metal` and `leakcheck` remain mandatory and unverified.

The combined environment-sync, PostgreSQL integration, scheduler, state and
migration checks pass. The competing intent-claim check also passes ten repeated
runs. The configured native acceptance host remains unavailable because its
Google Cloud consumer project is suspended; this is not native VM evidence.
Main through `d071df66e` is integrated. Its merged migrations replay, SQLC
comparison, SDK coverage, API/state checks, focused PostgreSQL runtime and
scheduler/migration checks, and GitOps/issue-ownership CLI checks pass locally.
Scheduler regression checks also cover caller/attempt expiry while admission
is contended and bounded cleanup contention after confirmed destruction.

Native stop ordering now spans app cold boots and snapshot restores as well as
job boots. A stop cancels and joins the complete boot unwind before consulting
the live map. The publication boundary rejects cancelled attempts, including a
VMM returning success after cancellation. Duplicate stops join the current
teardown and preserve its outcome; a replacement cannot use the same identity
until that teardown finishes. Park and unexpected-exit fallback cleanup share
this boundary. Builder interruption still reaches the child while its destroy
owner waits for export, rather than waiting on that owner's completion.

These barriers establish ordering, not a qualification receipt. Durable
execution frames, retirement receipts and scheduler recovery now fence attempt
replacement independently of terminal database state. The production native
evidence provider still requires complete host helper and bind recovery; native
process exit uncertainty and best-effort resource cleanup cannot satisfy its
process and resource acknowledgements. The qualification consumer,
serving proofs and graph activation remain unwired, and the apid executor stays
disabled until the full local and native gates pass.

Focused lifecycle checks cover late cold-boot/restore success, cancellation
before effects, cancellation during network setup, timeout recovery, duplicate
stop outcomes, cleanup contention, export-target mismatch and builder
interruption during an active destroy. The complete portable `fcvm` suite and
`vmmdgrpc` suite pass. The initial broader run used a temporary path too long
for Unix sockets; the `fcvm` rerun with a short path passes. Native `test-metal`
and `leakcheck` remain unverified; the dedicated host's project is still
suspended.

Process retirement also requires the native watchdog's exit acknowledgement.
A missing or timed-out watchdog leaves the process record and chroot owned for
recovery and returns an error. Explicit stops suppress unexpected-exit relays;
they do not erase the process registration before that acknowledgement. Signal
escalation preserves the same uncertain outcome. The Manager keeps the instance,
network and allocator lease after an unconfirmed stop or failed boot cleanup,
while dropping the guest CID join used for readiness and broker authority. A
later stop can finish the cleanup. A failed restore cannot proceed to cold-boot
fallback until its prior process has been confirmed stopped.

This remains separate from durable attempt-bound retirement evidence. Network
and cgroup removal still require native leak acceptance before the full feature
can be enabled.

Restart recovery must also handle VMs that survive vmmd while its in-memory
records disappear. The startup orphan sweep intentionally preserves instances
still live in durable state. An unknown-instance response after that restart
therefore cannot alone prove physical retirement; qualification recovery needs
to locate and stop that exact surviving incarnation before releasing its
reservation or publishing a retirement receipt.

Portable checks cover timeout/missing-watchdog refusal, recovery after the exit
acknowledgement, lease retention across destroy/signal/park/process-exit and
failed boot/restore, CID revocation, and refusal to overlap a failed restore
with cold boot. The complete `fcvm` and `vmmdgrpc` suites pass using isolated
temporary storage. The boot/teardown, uncertainty and builder interruption
checks also pass five runs with Go's race detector. A shared-disk compilation
failure and the timeout fixture's initial readiness/shutdown-setting mix-up
were corrected before these successful runs; neither is native acceptance.

The next recovery substrate separates observing a native task from proving
that no producer can launch one later. Process discovery keeps every managed
PID, including duplicate instance IDs, and binds its UID, cgroup and kernel
start time. Failed reads or a disappearing process root do not count as
absence. Retirement pins the kernel task with a Linux pidfd, revalidates the
incarnation before signaling and waits for exit. An observed-task stop by
itself is explicitly not a qualification or absence receipt. The pidfd API's
exit semantics are documented in the [Linux manual](https://man7.org/linux/man-pages/man2/pidfd_open.2.html).

A private host-lifetime launch journal supplies the missing producer fence.
Its stable file lock spans child creation, durable PID/start-time publication
and opening an inherited pipe gate. Each record also binds the kernel boot UUID;
an unknown or different boot refuses both launch and retirement authority.
The boot UUID's scope is described in the [kernel documentation](https://www.kernel.org/doc/html/v6.1/admin-guide/sysctl/kernel.html#random).
The release helper cannot exec jailer until that gate opens, and exec preserves
the recorded incarnation (including a non-leader exec's original start time,
as implemented in [Linux exec](https://github.com/torvalds/linux/blob/v6.1/fs/exec.c#L1041)). If vmmd
dies before publication, its writer closes and the unrecorded child cannot
start a VM. Recovery first irreversibly revokes the journal generation, then
waits for the bound process and excludes unmatched duplicate tasks before
recording exit confirmation. Missing, corrupt, incomplete, duplicate-key or
untrusted records remain errors. Direct commands, daemonizing jailers and
changed UID/cgroup/network identities cannot bypass the launch gate.

The allocator can quarantine all recovered slots atomically, including
several UIDs for one duplicated instance ID. Ordinary boot, prepared-network
adoption and ordinary lease release cannot borrow or clear those holdings.
This establishes cleanup ownership without installing a live row or guest CID
join. Releasing a quarantine still requires confirmed exit and resource
cleanup; it is not authorized by a scan error or generic terminal state.

The journal-backed lifecycle is now available behind vmmd's explicit
`native_process_recovery = true` configuration; the default remains disabled
pending native acceptance. Manager recovers all held journal/native slots
before boot or prepared-network admission, without granting readiness or CID
authority. A process-lifetime lock prevents a second daemon from opening an
independent allocator for the same root. Disabling the mode while its journal
exists refuses admission instead of passing ownership to legacy reapers.
Legacy jail and layer-clone reaping is skipped while the mode is enabled.
Managed namespaces and host/peer interfaces must match a held launch or
observed-task lease; unowned remnants refuse admission. Native mode currently
requires `prepared_networks = 0`: claiming a cache entry can rename its namespace
before the full lease is journaled. Enabling that cache is refused before host
effects until its claim and helper producers are durably fenced.

App and job admission persist a full lease before host setup. Jailer starts
through the release-matched pipe gate; every fork gets a watchdog even if
publication fails. Restore fallback can replace only its locally registered,
confirmed prior incarnation. Finished generations remain immutable in the
private archive; a crash after archiving but before publishing the next
generation is retryable. Forking jailer options, including `--new-pid-ns`, are
refused so the journal's PID still identifies the actual Firecracker process.

Process exit and resource removal have separate durable acknowledgements.
Local cleanup verifies both ordinary and private host interfaces, namespaces,
cgroups and all managed Firecracker-version chroots before releasing a slot.
Cgroup removal uses directory removal rather than unlinking control files.
Bind cleanup preserves metadata and source modes across failure, and unknown
mounts refuse deletion. Recovered builder export cannot borrow artifact
provenance or fabricate a historical guest exit code.

A restarted daemon can revoke and confirm the exact VM's exit, but unfinished
records retain their quarantine: provisioning/export helper processes and
their full producer coverage is not yet durably recovered. Firecracker exit cannot
prove those helpers are gone. Previously completed resource acknowledgements
can be rechecked for idempotent stops. Exact helper recovery, bind provenance,
journal retention, legacy adoption and attempt-bound scheduler evidence still
remain necessary. The qualification consumer, serving proofs, graph activation
and apid executor remain disabled/unwired. Native `test-metal` and `leakcheck`
remain mandatory and unverified.

Jail device setup now uses a durable host helper frame under its original VM
launch generation. The frame binds the full persisted lease, kernel boot UUID,
helper UUID, PID/start time and an exclusive cgroup's path/device/inode. The VM
producer lock spans cgroup publication, fork, incarnation publication and opening
the release-matched helper's pipe gate. Linux creates the helper directly in
that cgroup with `CLONE_INTO_CGROUP`; moving an already running helper is refused.
Helpers inherit vmmd's control-plane cgroup limits and cannot borrow tenant or
builder capacity. Arguments and stdin are not persisted in these journals.

Retirement durably revokes the helper before killing its entire cgroup, waits
for the kernel's recursive `populated` acknowledgement and removes the exact
recorded cgroup. [The kernel documents inherited cgroup membership and recursive
kill semantics](https://docs.kernel.org/admin-guide/cgroup-v2.html). A crash before
cgroup inode publication can only leave an empty group; a populated group with
unpublished identity remains an error. Reused paths, missing/corrupt evidence and
unfinished helpers refuse resource acknowledgement or VM generation replacement.
Current and archived helper frames remain discoverable after daemon death;
startup rejects unknown helper cgroups and unfinished frames under retired owners.
Completed acknowledgements are rechecked for resource reappearance.
Kernel control-file evidence is bounded to 4,096 bytes by
`pkg/api/limits.go`; missing, duplicate or unknown event fields refuse proof.

The single command watchdog joins every fork, including publication failure and
cancellation. Native jail setup refuses legacy helper fallback after failure.
The remaining mount, node-wide network, export and materialisation producers and
durable bind/source-mode provenance still need this protocol. Restart cleanup
therefore continues to retain its quarantine; this increment does not enable attempt-bound
native receipts, the qualification consumer or environment graph activation.
Dedicated native `test-metal` and `leakcheck` remain required before completion.

The host helper checkpoint passes the complete portable fcvm, vmmd, vmmdgrpc
and jailsetup suites. Native ownership/launch/helper contracts pass five
repeated race-detector runs, including daemon death between fork and publication,
cancellation, uncertain retirement, immutable owner replacement and corrupt
journal refusal. Leak-layout checks include helper cgroups. The Linux x86_64
acceptance binary and release helper cross-compile; the dedicated native helper
test exercises orphan descendants and refuses changed cgroup identities, but has
not been executed here. These results do not establish native VM or leak
acceptance, full helper coverage, qualification receipts or graph activation.

Per-instance network setup, IP/nft stdin batches, rule-handle capture, egress
allowlist/port/circuit updates, DNS-gated egress and private-network policy/link
updates now use the same gated host helper protocol. Each operation captures its
original VM generation and lease before dispatch. A helper rechecks that frozen
identity under the VM producer lock; an old scope cannot acquire a replacement
generation through the same instance ID. Live instances retain an immutable
launch identity, and late policy cache writes affect only the original live
instance object. Private-network attachment carries that original target set
through its final route/policy fan-out; a replacement or removed target refuses
the remaining operation. Native commands cannot fall back to the legacy command runner.
The regular-file namespace-marker fallback is an in-process producer: it holds
the same launch lock across its identity check and file removal.

Normal effect authority stops at VM revocation. Network deletion has a separate
persisted `network_cleanup` purpose that requires confirmed VM exit and permits
only the three allocator-derived namespace/public-veth/private-veth deletion
commands from the original lease, without stdin. Cleanup can retain this limited
authority after the original daemon dies, but cannot create resources or execute
policy commands. Previously completed resource acknowledgements start no new
helpers. Older helper frames without a purpose retain ordinary effect semantics;
missing, duplicate, null or unknown purpose evidence cannot grant cleanup authority.

An unfinished or reappeared helper group prevents the next helper from starting.
Failed retirement retains the original journal and allocator holding; retry must
finish the old helper before deletion and resource acknowledgement can proceed.
This increment covers per-instance network producers only. Node-wide bridge,
fabric, static-egress and host policy producers, mount/export/materialisation
coverage, durable bind provenance and journal retention remain outstanding.
Recovered full VM cleanup therefore keeps its existing refusal. Attempt-bound
native qualification receipts, the qualification consumer, graph activation and
the apid executor remain unavailable. Dedicated native VM and leak acceptance
remain mandatory before completing the feature.

The network checkpoint passes the complete portable fcvm, jailsetup, vmmd and
vmmdgrpc suites, five repeated native-contract race runs and scoped pinned lint.
Protocol fixtures
cover gated stdin/capture, stale-generation refusal, restricted deletion,
unfinished-helper fencing, allocator retention/retry, duplicate cleanup and
replacement-safe policy caches. Leak-layout checks pass. The Linux x86_64
acceptance binary and release vmmd build pass. A dedicated native network test
uses real helper cgroups and IP/nft commands inside private mount/network
namespaces, verifies deletion and rejects an old scope after replacement. It
has been compiled but not executed. These results do not establish native VM,
snapshot, leak or complete environment serving acceptance.

Native pre-boot drive staging now has its own durable loop-mount session. Before
any attachment, it records the original prepared VM generation, full lease,
kernel boot identity, backing-file device/inode, loop block identity and mount
namespace. It records the private mountpoint directory before attachment, and
the actual mount ID before invoking a Go writer. The VM ownership lock spans
attachment, every synchronous write, unmount and cleanup acknowledgement; a stop
cannot overtake a live writer. Boot and restore retain the owner captured before
provisioning rather than resolving a newer generation at the staging boundary.

Linux uses `LOOP_CONFIGURE` with `LO_FLAGS_AUTOCLEAR` and a unique session marker,
then direct ext4 mount syscalls with `nodev,nosuid,noexec`. There is no subprocess
or two-step loop attachment fallback. [Linux documents the atomic configuration
and automatic-detach flag](https://man7.org/linux/man-pages/man4/loop.4.html).
Retirement checks the original namespace, loop marker, backing-file identity,
block device and mount ID before unmounting. A failed or busy unmount retains
the record and slot; there is no lazy unmount or recursive removal of a mounted
tree. A reused loop number cannot authorize detaching an unrelated attachment.
A successful detach request alone is insufficient: the original marker and
mountpoint must disappear before the journal acknowledges cleanup.

Secrets, API environment, workload environment/manifest/roster, batched pre-boot
files and job manifests use this session in native mode. Native staging requires
the canonical writable main drive and an unlaunched VM. Shared read-only sidecar
images cannot be patched through the compatibility staging API. The legacy
instance-only pre-boot cache is bypassed in native mode until its cache receipts
carry generations. Native artifact export now explicitly refuses the legacy
mount/copy path; its destination authority and complete producer coverage still
need implementation.

Unfinished staging blocks Firecracker and host-helper launch, additional staging,
resource acknowledgement and restore fallback. Startup inspects current and
archived session frames, their mountpoints and managed loop markers; unknown,
corrupt, incomplete and reappeared ownership refuses admission. A restarted
daemon can retire these exact staging sessions, while full recovered VM cleanup
continues to retain quarantine until bind/source-mode, export, materialisation
and node-wide network producers have durable provenance. This increment supplies
no guest qualification receipt or environment graph activation authority.

Portable tests cover publication failure before attachment and before writing,
retirement during a writer, cleanup failure/retry after restart, stale generation
and authorized-process refusal, strict record decoding, archive retention and
every staging entry point. The native test exits a writer process without Go
defers, observes its surviving real mount, refuses a changed backing identity
and recovers through the original journal. It still requires execution on the
dedicated native x86_64 Linux KVM host, together with `test-metal` and `leakcheck`.

This checkpoint passes the full portable fcvm, jailsetup, vmmd and vmmdgrpc
suites, five native-contract race runs and scoped Linux lint including metal
sources. The Linux acceptance binary, release vmmd and adjacent jail helper
cross-compile. Portable leak-layout and shell-inspection contracts pass; the
shell gate and Go checker both inspect native loop tokens, including attachments
that never reached a mount, and reject unreadable kernel evidence. The configured
acceptance project still reports suspension, so native execution and leak
acceptance remain unverified. The qualification consumer and full environment
executor remain disabled.

Scheduler qualification now records an immutable execution frame with the
original request, attempt, reviewed artifact, environment/source identities,
instance, placement, wake and a separate cleanup capability. Its dispatch mark
commits before the VM RPC; a lost response cannot authorize replay. The frame
survives source/request removal and instance collection. Cleanup follows its
recorded node after revocation, expiry or project deletion. Account/app purge
cannot remove an unfinished reservation.

Memory and PostgreSQL retirement require either an exact never-dispatched
frame or complete native process/resource evidence. A dispatched frame cannot
use never-dispatched evidence. The receipt and terminal instance commit together;
raw terminal/deletion writes and attempt replacement cannot release the holding.
States outside the charged set, including account-deletion eviction, cannot
bypass this fence. A native receipt or native incarnation cannot retire a second
execution. Uncertain pre-upgrade admissions are conservatively marked dispatched.
Frame/receipt retention and adoption of older admissions without a recoverable
attempt still require dedicated handling.

The scheduler primitive now requires the additive attempt-aware VM capability
before admission. Generic boot/Destroy and NotFound no longer satisfy that
contract. Recovery consumes the persisted frame without obtaining new execution
authority, and releases the ledger only after durable retirement. The production
vmmd client/router does not yet implement that capability: host helper/bind
recovery and the native attempt journal must supply its evidence first. Storage
and scheduler fixture receipts do not establish native retirement or serving
proof. The qualification consumer and full environment executor remain disabled.

Recovery discovery now pages through unfinished executions on their recorded
host, independently of surviving source, request, app ownership and node
eligibility. Pages contain at most 100 frames and use the original instance UUID
as their cursor. A current lease for that exact request, attempt and instance
excludes the frame. Discovery is advisory: the scheduler rechecks eligibility
under the original request and frame locks before any native RPC. PostgreSQL
uses its own clock and waits for in-flight renewals; a stale discovery snapshot
cannot retire a renewed lease. An expired original lease cannot regain dispatch
authority, and an unretired frame continues to block replacement admission.

One recovery page has a bounded cleanup deadline. Failed native retirement
retains the instance and ledger charge while advancing the cursor so neighboring
frames remain reachable. A later pass retries the holding. Complete native
evidence and durable retirement still precede capacity release; generic Destroy
cannot satisfy recovery. This discovery and recovery primitive does not start a
qualification consumer or grant new boot, graph activation or serving authority.

This recovery checkpoint passes the complete portable state, scheduler, apid,
CLI and API suites on the integrated tree. Real PostgreSQL checks cover expired
attempts, source purges, cursor bounds and a renewal held uncommitted across the
original lease expiry. The recovery contracts pass five repeated runs, including
five runs under the race detector against both memory and PostgreSQL. SQLC
regeneration, the exact-HEAD spec-citation gate and ADR number ratchet pass.
The merged OpenAPI has unique keys and valid local references; its embedded copy
matches, and the Node SDK's GitOps check and generated type compilation pass.
These checks do not establish the remaining native or serving gates.

The execution-frame checkpoint passes the complete portable state, scheduler,
API and imaging suites, real PostgreSQL qualification/retirement checks and
five repeated race-detector runs of the new scheduler and memory contracts.
SQLC regeneration matches the canonical schema. A subsequent terminal-outcome
check confirms that native retirement can finish an uncertain older terminal
admission without rewriting its parked, failed or account-eviction outcome.
These are storage and scheduler results; the production native capability and
dedicated native VM/leak acceptance still remain outstanding.

The complete portable `fcvm`, `vmmd`, `vmmdgrpc`, `jailsetup`, `state`, `sched`,
`apid`, `imaged` and CLI suites pass across the integrated-tree checks. Native
recovery, launch-gate and startup checks pass five repeated runs under Go's race
detector, including unknown-network refusal and prepared-cache admission. The
PostgreSQL qualification contract and hosting-failure checks pass against a
fully migrated private database. SQLC regeneration matches the canonical
schema, and the merged Prometheus rules validate. The Linux x86_64 test binary
and release helper cross-compile; Linux pidfd tests have not been executed
here. The dedicated native acceptance host's cloud project remains suspended,
so these results do not constitute native VM, snapshot or leak acceptance.

The qualification migrations were authored under the branch's earlier ADR-431
and ADR-435 numbers. Their append-only SQL comments retain those historical
citations; this decision now uses ADR-521 to avoid newer upstream decisions for
gateway trace retention and preview route reports.

## Review and control workflow

An environment can instead opt into reviewed merge approval at binding:

```sh
gregale projects environments gitops bind shop production --manifest-path environments/production.yaml --ref refs/heads/main --approval-policy protected_branch
gregale projects environments gitops status shop production
```

Start in report mode and review the adoption plan before reserving existing fields.
Status records the exact reviewed-definition digest and PR/review evidence separately
from fully applied intent. A protected source cannot use the manual approval action.

The current manual-approval interface uses an exact GitHub commit SHA. The
repository identity comes from the project's verified installation binding.
Reviewing does not approve a mutable ref or transfer ownership.

```sh
gregale projects environments gitops bind shop production --manifest-path environments/production.yaml
gregale projects environments gitops review shop production --commit <full-commit-sha> > review.json
# Review the definition in review.json before approving its digest.
gregale projects environments gitops approve shop production --file review.json --yes
gregale projects environments gitops adoption-preview shop production > adoption.json
# Review ownership transfers and blocking reasons before adoption.
gregale projects environments gitops adopt shop production --file adoption.json --yes
gregale projects environments gitops status shop production
```

Controls require the current source generation from `status`. Explicit false
values remain distinct from omitted pruning/suspension controls. Overrides name
the logical resource and owned field, with a reason and an RFC3339 expiry within
24 hours. Adoption preserves existing values; the approved definition becomes
effective through subsequent enforce-mode reconciliation. The apid worker
startup and full graph gates above remain required before deploying this feature.


Native image staging now journals shared source access and each jail reference.
The source epoch records the original device/inode, mount namespace and metadata,
plus every original prepared VM generation and full lease. Read-only references
aggregate their read grant across aliases of the same inode. Writable scratch
images have one exclusive reference and use the original VM's UID/GID. The last
reference restores the source's original mode and ownership before the epoch
retires; stopping one VM cannot revoke another VM's surviving access.

A private bind anchor pins the original inode before any chmod/chown or jail
attachment. Recovery therefore restores permissions through a verified FD even
if all input pathnames disappear. New epochs receive new anchor paths. An old
completed epoch never changes metadata for a later use of that inode. The lock
order is original VM then source; source operations never lock another VM. A new
epoch reads its original metadata only after acquiring that source lock, so
last-owner restoration cannot become the next epoch's stale permission baseline.
Permission intent and placeholder identity precede kernel effects, and readiness
follows exact inode, mount identity and restrictive bind attributes. A failed
acknowledgement preserves the previous permission transition for recovery.

Read-only image staging, ephemeral writable scratch staging and explicit image
binds use this journal in native mode. Boot and restore retain the owner captured
before provisioning. Native jail creation holds that owner's lock and refuses
an existing root instead of recursively wiping active image references. Launch,
pre-boot writers, host helpers, resource acknowledgement, replacement and startup
inventory inspect image ownership. Native stop retires image references before
removing the jail, including after vmmd death; the broader recovered-cleanup
quarantine remains until all other producers have durable provenance.

Portable tests exercise shared aliases and last-owner restoration, exclusive
writers, publication failures at every stage, acknowledgement loss after anchor
removal, stale generations, revocation concurrent with a permission producer,
strict record decoding and jail retry refusal. Linux tests reject changed access
flags and ambiguous mount tables. The native crash fixture leaves a real bind
mount before its final receipt, removes source pathnames and checks original
inode restoration through a fresh journal. Both leak checkers include private
image anchors. Compilation and portable evidence do not establish native KVM
acceptance; the dedicated acceptance project remains suspended.

Writable image clone/copy materialisation and snapshot drive export explicitly
refuse their legacy producer paths in native mode. Their complete provenance,
native private-jail device acceptance, node-wide network/policy effects and other host producers
remain required. Source/reference history is retained; its bounded retention
and legacy adoption still need implementation. Native incoming attempt/RPC
ownership, qualification consumer/readiness/restore receipts, function/new-app
adapters, scoped graph activation/serving proofs, all environment acceptance
scenarios, native test-metal/leakcheck and final executor enablement remain open.
The qualification consumer and approved-intent executor remain disabled.

The hardlink operation uses the procfs form of `linkat` against the pinned source
FD, preserving vmmd's existing capability bound (which excludes
`CAP_DAC_READ_SEARCH`); see the [Linux linkat documentation](https://man7.org/linux/man-pages/man2/link.2.html).

This image-ownership checkpoint passes the complete portable fcvm, jailsetup,
vmmd and vmmdgrpc suites, five race runs of native ownership/recovery contracts,
scoped Linux lint including metal sources, and portable leak-layout contracts.
The native acceptance binary and both release binaries cross-compile for static
x86_64 Linux. Native execution and leak acceptance are still pending; these
results do not enable qualification dispatch or the environment executor.

The main-branch ADR-192 operator staging diagnostics also pass through the native
original-owner session. Native mount timing includes ownership checks, durable
planning and attachment publication; unmount timing includes physical cleanup
and its durable acknowledgement. File work stays outside both phases. Timing
requests do not restore the legacy instance-only pre-boot cache or bypass
revocation, and they do not change customer wake event fields.

The host TUN bind now has its own durable original-owner journal. Its plan
retains the prepared VM generation/full lease, kernel boot, jail root, mount
namespace and original character-device inode/rdev/mode. The VM lock spans
source pinning, placeholder publication, attachment and readiness. The Linux
backend opens `/dev/net/tun` with `O_PATH` so preparation does not open a TUN
session, and it never changes host device metadata. It applies writable,
device-capable `nosuid,noexec` attributes to this bind only, using
[`mount_setattr`](https://man7.org/linux/man-pages/man2/mount_setattr.2.html).
Launch and later host helpers require completed attachment proof. Failed
publication holds the generation instead of authorizing another attachment.

Stop waits for original VM exit and scoped helper retirement, then retires the
host TUN bind before image references and root removal. Direct unmount requires
the original device and kernel mount identity; uncertain, stacked, replaced or
unavailable namespace effects retain ownership. A missing final mount receipt
can be recovered only from the previously published placeholder and pinned
original device proof. Retirement is replayable after physical removal but
before its acknowledgement. Resource acknowledgement, fallback replacement and
startup inventory inspect both active and immutable archived TUN frames. Boot
and restore use the owner captured before staging, and the native bind never
enters the legacy instance-only in-memory list.

Native private jail-device setup now uses a separate scoped helper purpose. The
original VM lock spans capture of its pidfd, mount namespace, root directory in
that namespace and original TUN descriptor. The journal records those identities,
the original process/start time and lease, kernel mount IDs, and the producer's
own descriptor numbers and process incarnation. Four descriptors accompany the
launch gate; parent copies close before helper authorization and gate release.
Missing publication or failed closure preserves the original frame for recovery.
Another setup cannot layer effects onto a generation with a retained frame.

The one-shot helper validates inherited identities and the pidfd target, unshares
filesystem state, enters the retained mount namespace and verifies its original
mount IDs. It uses a locked thread and private propagation before mounting a
private device tmpfs through the original root descriptor. It binds the original
TUN, creates the jail KVM node and checks both devices using the exact jail UID/GID
without host groups. The structured readiness receipt retains the exact scope,
device mount identities and successful KVM API/TUN access. Helper exit alone
cannot authorize readiness; publication requires original VM authority and helper
cgroup retirement. Native setup does not enter the legacy numeric-PID `nsenter`
path or its in-memory mount list. The existing legacy setup remains available.
See the Linux [setns](https://man7.org/linux/man-pages/man2/setns.2.html) and
[pidfd_open](https://man7.org/linux/man-pages/man2/pidfd_open.2.html) contracts.

Recovery proves producer descriptor closure after uncertain publication and
joins the original helper cgroup. VM cleanup, resource acknowledgement,
replacement and finished-frame inventory also require absence of the original
namespace from process threads and namespace descriptors. Uncertain inspection
holds ownership. This inventory does not independently prove absence of arbitrary
external root-held file or detached-mount descriptors; dedicated leak acceptance
and the remaining host producer coverage are still required. The TUN crash
fixture tests recovery in a surviving original namespace; it does not establish
production service restart recovery.

The deployed vmmd service uses `ProtectSystem=strict` and `ReadWritePaths`, which
create a service filesystem namespace. Systemd documents that these namespaces
are individual to service processes; see its
[execution environment documentation](https://raw.githubusercontent.com/systemd/systemd/main/man/systemd.exec.xml).
Durable access to the original namespace across a service restart is still
required for image, loop and TUN recovery. A fresh namespace does not supply
evidence about the old mounts; current backends hold ownership on identity
mismatch. Namespace persistence must preserve the existing service hardening
and receive dedicated restart acceptance.

Portable TUN contracts cover interrupted plan/placeholder/readiness/retirement
publication, revoked and stale callers, original-owner producer locking,
changed mount/device/root/namespace proof, strict private record decoding and
unknown startup effects. The Linux native crash fixture checks a real surviving
device bind and refuses altered identities before fresh-journal retirement.
Both leak checkers include TUN mounts at non-default and deleted target paths.
The native metal wrapper derives all `TestMetalNative*` ownership tests from
source and requires individual PASS evidence, refusing missing or skipped
producers. These gates do not enable qualification dispatch or the full
environment executor, and native execution remains outstanding.

Portable private-device contracts use real descriptor inheritance and launch
gating with modeled kernel identities. They cover original-owner locking,
closure before authorization, failures at each publication stage, exact receipt
validation, revocation, retry refusal and namespace retention across replacement
and archived-frame inventory. Shared mount/pidfd evidence parsing runs without
KVM. The Linux fixtures use a real private namespace/chroot, release-matched
helper and device access under the jail UID, and check confinement and cleanup.
The recovery fixture exits its producer only after validating a real device
receipt and physical helper retirement, before recording their acknowledgement.
A fresh journal rejects readiness and another setup, holds the original private
namespace while the target lives, and replays retirement before final cleanup.
Fixture cleanup is registered before any device attachment. Source compilation
is not execution evidence. Native timing reports the
complete ownership, wait, device setup and receipt phase in `SetupJailMs`; legacy
subphase timing fields remain zero on this path. Native crash-after-device-setup
and hardened service restart acceptance remain outstanding, along with the
complete qualification, serving and environment gates listed above.

The incoming qualification journal now records the complete original execution
frame, cleanup capability, local node, kernel boot, one request generation and
its bounded dispatch deadline. A cleanup request arriving before Create writes
an irreversible tombstone. The same instance UUID has one canonical file key;
changing its spelling cannot create parallel authority. Claim and revocation
share a private file lock, and acknowledgement loss after publication cannot
authorize replay. Record decoding requires every field, including false flags
and nested artifact fields; damaged, public, replaced or wrong-boot records hold
ownership. Cleanup capabilities stay outside diagnostic formatting and public
execution JSON, and persist only in the private journal.

This journal currently records incoming identity and revocation only. It grants
no native VM launch or retirement receipt, and is not wired to production RPCs.
The next required integration must bind every original native lease and producer
to this request under its revocation fence, quarantine uncertain admissions on
startup, reject generic instance operations that bypass attempt ownership, and
return complete native process/resource evidence. Client, router, server,
qualification consumer and graph serving integration remain required. Portable
file-lock, publication-loss, deadline, changed-frame and strict-record contracts
do not establish native retirement or end-to-end environment acceptance.

Integration with ADR-461–479 preserves managed PostgreSQL admission fencing,
release-only migration credentials, confirmed teardown, failure redelivery and
restart resource quarantine. A single operation registry must cover both boot
and resumable live operations; duplicate teardown callers join the first owner's
result and export target. Cleanup keeps the complete identity until physical
retirement and durable journal removal succeed. The scheduler's private process
notification counter is not serialized native authority: native ownership still
compares every durable lease field exactly after reopen.

Scoped Git secret aliases resolve before binding privilege selection. An alias
to a migration credential cannot grant it to serving code or a companion; only
a persisted release task receives that credential. Release credentials must not
be omitted by a serving allowlist, and a conflicting environment alias fails
closed rather than silently changing its target.

The native qualification recovery protocol and ADR-472 restart quarantine are
independent ownership systems. Co-enabling them is refused before production
admission until their producers and recovery authority have one qualified
contract. Restart quarantine alone does not produce a native retirement receipt.
This integration does not enable qualification dispatch or the environment
executor, and dedicated native lifecycle and leak acceptance remain required.

Serving runtime receipts use the same credential audience as sealed delivery.
The all-secrets baseline excludes release-only migration credentials, and an
explicit alias or version for such a credential cannot prove serving freshness.
Memory and PostgreSQL receipt validation enforce this boundary; rollback keeps
it in place.

The resumable image-preparation integration projects the candidate's frozen app
before selecting its build path, staging its runtime base, or completing the
handoff. Shared runtime changes cannot change an already reviewed candidate's
artifact preparation. The node-owned outbox retains the dedicated environment
image channel; a held artifact handoff still grants no serving or release work.

Git queue names use the existing binding catalog contract: one to sixty-three
lowercase characters, beginning with a letter and followed by letters, digits
or hyphens. App hostname restrictions do not apply to queue identities. API,
state admission and Git compilation share this validation. Reviewed adoption
preserves original values and consumer IDs for short, long and `tag-` names;
later reconciliation preserves accepted work and receipt identity when an
approved destination label changes. This closes the queue-name compatibility
gap without granting qualification or graph activation authority.

## Continuous drift reporting in apid

Apid can run an independently enabled report controller with
`FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED=true`. It uses the real apid
backend and the durable approved definition, so a Git transport outage does
not stop local drift observation. The controller is off by default while the
full environment feature is still being qualified. It checks successful
report attempts every minute and retries observation failures after thirty
seconds. The daemon joins the controller on shutdown.

Report-only claims select `mode=report` inside the same source-locked
transaction that issues the lease. An older due enforce source cannot consume
a reporting attempt, and idle reporters do not delay enforce jobs. The
worker refuses a store without this atomic claim capability. Source mode
changes advance the generation, revoke prior report publication authority,
and make the newly selected mode immediately eligible.

Report attempts observe intent and runtime without applying intent, recovering
pending enforce effects, emitting fleet changes, refreshing instances, or
launching qualification. Both the worker and apid backend prevent enforce
effect recovery in report mode. Existing unacknowledged gateway and runtime
effects remain durable and appear as runtime drift even if saved intent is
equal to Git. Full convergence publication retains the store's final intent
and runtime fences. Source discovery errors and reconciliation runs remain
separate in authenticated API/dashboard history.

This enables continuous reporting independently of the unfinished complete
graph executor. Enforcement and native qualification dispatch remain disabled
pending the serving, recovery and native acceptance gates above.

## Original native producer binding

The private incoming journal now uses version 2 and lives under the native
ownership root. Before publishing a physical launch record, it durably freezes
one physical generation and every exported field of the original allocator
lease. A failed physical publication leaves that planned lease quarantined on
daemon restart. Missing launch records and incoming revocation grant no exit,
resource-removal, readiness or retirement receipt. Version-1 incoming records
fail closed; this journal has not been enabled in production.

Generic UUID preparation, launch and resumable manager operations use the same
canonical incoming lock as claim and revocation. Alternate UUID spellings cannot
borrow the reserved instance. A private producer must carry the exact incoming
frame, request generation and kernel boot within its original dispatch deadline.
The launch ticket retains both incoming and physical locks through authorization.
An original qualification generation cannot be replaced by restore fallback.
Revocation persists its incoming tombstone before fencing the exact physical
generation; replay completes a physical fence interrupted by daemon death.
Startup rejects damaged or changed bindings and preserves uncertain allocations.

Private qualification transitions retain instance notifications and audit events
without requesting ordinary service or worker capacity reconciliation. In
particular, testing a held service candidate must not boot its serving predecessor
as a side effect of the private RUNNING or STOPPED transition.

The Manager now has an attempt-aware cold boot and retirement surface, bound to
an explicitly configured local compute UUID. Boot validates the exact instance,
app, deployment, artifact key, guest memory and wake correlation before claiming
incoming authority. It rejects snapshots, builder exports and other execution
classes. The scheduler remains responsible for preparing the complete frozen
workload payload. An accepted incoming attempt cannot dispatch twice.

Retirement revokes incoming creation, joins the original Manager teardown and
checks the exact physical generation, kernel boot and complete durable lease.
Only confirmed process exit and resource removal supply `native_retired`.
The incoming generation becomes the stable receipt UUID after those checks;
retry does not mint another receipt. Unbound requests and uncertain physical
publication remain charged. A pre-create tombstone excludes delayed delivery
but supplies no physical proof, and cannot retire a foreign parent.

Native exclusion evidence for unbound requests, complete producer and namespace
recovery proof, durable qualification consumer and graph
activation remain required. These methods do not remove the recovered-helper
ownership fence or enable qualification dispatch. Dedicated native x86_64 Linux
KVM lifecycle and leak acceptance remain outstanding.

## Attempt-aware native RPC transport

The dedicated create and retire RPCs now carry a versioned, typed execution
frame, including the original host, attempt, artifact and private cleanup
capability. The wire adapter preserves every field and UUID spelling. Missing,
unknown or unsupported capability fields fail closed. Host placement and
single-valued wake/app/deployment/instance/node correlation must agree before
the native owner sees a request. Boot carries the existing complete AppSpec
payload and checks the exact artifact key and guest memory reservation.

Schedd's typed client and router now implement the additive qualification
surface. Cleanup resolves the recorded node after app ownership changes and
returns evidence only when the reply echoes the complete original frame.
Incomplete exit/resource proof, generic absence, a substituted artifact or
cleanup capability, and an older node cannot release the scheduler reservation.
Secret-alias capability loss cannot invoke the generic Destroy helper on this
path. Native errors have fixed diagnostic text and a stable unconfirmed code;
private cleanup values never enter that text.

The production Manager still has no configured qualification identity and the
durable consumer is not enabled. These RPCs supply transport, not smoke,
snapshot, serving convergence or activation evidence. Native exclusion and
complete recovery proof remain prerequisites to enablement. Portable bufconn
checks cover the complete runtime/cleanup round trip, older-node refusal,
substituted responses, malformed capability and correlation rejection, private
error redaction, secret-alias loss, and routing to the original host. They do
not establish supported native KVM acceptance.

CI now runs the existing memory/PostgreSQL GitOps parity suite in its database
shard. Cross-package coverage instruments the real state adapters exercised by
that suite; the exact pkg/state 70% floor is retained. Previously the external
suite ran in the lightweight shard, where its PostgreSQL cases could skip and
its adapter calls did not contribute to the state profile.

## Catalog and runtime scope compatibility

The runtime scope contract now accepts one to forty lowercase alphanumeric
characters with internal hyphens, preserving the legacy upper bound. Every
catalog name, including short and numeric names, fits this contract. The catalog
retains its separate thirty-three-character bound and reserves `default`; the
runtime keeps `default` as its omitted-scope value. `__all__` remains a read-only
query sentinel and is rejected on writes and catalog registration.

An append-only migration updates scope constraints for variables, sealed secrets,
deployments, invocations, upstreams, OpenAPI snapshots, GitHub branch mappings,
app tasks, secret revocations and managed object-storage credentials. Catalog-owned
queue bindings, private consumers, reference intent and suppression use the
catalog grammar and retain their original identity and membership guards.
The migration changes constraints only. It preserves values and accepted work;
a downgrade that cannot represent existing short/numeric scope data fails
atomically instead of rewriting or removing that data.

The shared validator, public OpenAPI schemas, CLI reference commands and dashboard
editor use the relaxed runtime grammar. This change does not grant graph
activation, native qualification or serving authority. Shared memory/PostgreSQL
checks cover short, numeric and maximum-length catalog names through reviewed
adoption, owned-variable enforcement, scoped references and suppression, queue
reconciliation, accepted work/receipt retention, deployment identity, and runtime
input/snapshot receipt storage. A populated migration upgrade and replay retain
legacy forty-character scopes and accepted numeric-scope work; the actual Down
SQL rejects incompatible data inside a transaction and preserves the current
constraints and intent. Routed variable/all-scopes and authenticated dashboard
checks pass; CLI and Node/Python reference transports preserve the exact name.
Pinned Node/Python regeneration is deterministic, OpenAPI source/embed parity and
AST checks pass, and SQLC regeneration matches. Shared conformance checks also
retain short/numeric queue depth and accepted invocation identity/scope through
project removal and dead-letter replay. The unpublished scope migration uses
a version emitted by the repository generator. These receipts are portable store
fixtures, not native guest or snapshot lifecycle acceptance. Scoped Linux lint
passes with zero issues. Repository CI for source `eadb76904aeca42c0c4bb9e0239bdfaab0ea9c51`
passes both complete PostgreSQL/race state shards, the external adapter suite,
full migrations and all other PR checks. Aggregate exact `pkg/state` coverage
is 72.7%, above the unchanged seventy-percent floor. These results cover this
increment, not native lifecycle acceptance or the complete feature.


## State CI inventory and coverage union

The previous database shard timed out after twenty minutes while creating its
next migrated fixture, before the coverage gate ran. The complete state package
now uses two deterministic test-name shards on independent PostgreSQL runners.
The inventory includes fuzz seeds and runnable examples, excludes `TestMain`
(which Go still executes for each binary), and is checked against Go's compiled
registered list. Existing E2E boot-contract partitioning remains separate.
Race detection and the twenty-minute test-binary timeout are retained. Both
state profiles and the external PostgreSQL GitOps adapter profile are required
by one aggregate exact-package seventy-percent coverage gate. Repeated source
blocks contribute statements once and count hits from any successful shard.
This CI partition change does not supply native qualification evidence.

## Durable qualification discovery

Schedulers can now page committed qualification request IDs without depending
on notification delivery or an in-memory queue. Each pass uses an exclusive UUID
cursor and a bounded page size. Current scheduler ownership and tenant/project
identity filter work before the limit. Only the current prepared, approved,
enforce-mode graph is discoverable; suspended sources, held accounts, active
execution leases and unretired original executions are excluded. A Git source
outage preserves qualification work for the last approved graph. Jobs are
excluded until their separate qualification execution adapter exists.

Discovery is read-only and exposes no frozen inputs or private capabilities.
The existing source/app/request-locked claim still rechecks the complete
observation and artifact cohort, and the scheduler rechecks app ownership
before native effects. An expired claim with no admission can resume with a
new attempt and reservation. An admitted, uncertain attempt remains held until
its original execution has retirement proof. Cursor exhaustion starts a fresh
pass, so a newly eligible request behind the cursor is eventually discovered.

Portable memory/PostgreSQL checks cover missed notifications, restart discovery,
exclusive pagination, unchanged active leases, original reservation retention,
fresh attempt authority, owner transfer, account holds, source revocation,
unsupported jobs, source outages and competing PostgreSQL claim transactions.
This is durable queue discovery for the pending consumer. It does not publish
smoke/snapshot evidence, qualify a graph, enable dispatch, or activate workloads.
The complete portable adapter suite passes with race detection and PostgreSQL:
123 top-level tests and 448 cases, with no skipped cases. Scoped Linux lint
passes with zero issues, and SQLC regeneration matches the committed output.

## Node-bound qualification consumption

Schedd can consume one bounded page of durable qualification work. Its claim
capability checks current app ownership and execution class inside the same
source/app/request locks as the complete approved graph. An advisory page from
before an owner transfer cannot reserve an attempt for the old scheduler. Jobs
remain excluded. Invalid nodes, cancelled claims and a stale ownership check do
not consume an attempt. The existing generic qualification lease remains
available for its separately owned execution classes.

The page consumer requires an explicitly matching scheduler owner, a bounded
worker identity and attempt-aware native execution/retirement capabilities before
claiming work. Each candidate runs in its own frozen, lease-bounded runtime
window and completes original retirement before the consumer advances. A failed
candidate advances the cursor so its neighbors remain discoverable. Cancellation
stops before claiming the next candidate. Active and uncertain original attempts
retain their existing durable holds and cannot be redispatched by another scan.

A successfully executed visitor is not a qualified graph. The consumer does not
publish smoke, snapshot, release or serving receipts, remove candidate holds,
emit ordinary deployment-ready activation, or advance the applied revision.
Production does not start this consumer. Durable completion receipts and the
polling lifecycle, isolated worker/queue smoke, supported native qualification,
remaining graph adapters and activation still gate enablement.

Portable scheduler qualification checks pass with race detection: 27 top-level
tests / 77 cases, including original retirement before page advancement, neighbor
progress after failure, competing schedulers, ownership transfer, source
revocation, cancellation and retained uncertain capacity. The complete
memory/PostgreSQL GitOps adapter suite passes 124 top-level tests / 451 cases
with race detection and no skips. Linux lint passes with zero issues in state,
scheduler and external adapter tests; SQLC regeneration matches. Initial local
fixture failures and a shared-host disk-exhaustion run are retained in the audit.
These portable checks do not establish native smoke or snapshot acceptance.

## Original-attempt snapshot capture

The additive `CaptureEnvironmentQualification` RPC carries the complete original
execution and selects its recorded node. Generic snapshots and older node
capabilities cannot substitute for it. The production JailerVMM still blocks
private-drive snapshot export because its durable producer adapter is missing.
The Manager checks that capability before recording start or beginning snapshot
effects. This checkpoint implements the original-attempt boundary and transport;
the real native capture adapter remains required. With a supported adapter, vmmd
validates the live instance's original
physical generation and complete lease against both host journals, pins the
Manager flight, and serializes capture with incoming revocation. Destroy cancels
and joins that flight. The stored incoming deadline bounds every capture.

vmmd derives a warm v2 object namespace from the original deployment and incoming
generation. Callers supply neither object keys nor host paths. The capture pairs
memory, device state, and the frozen private drive, resumes through the existing
warm path, and requires successful publication of the kernel/base backing sidecar
remembered at boot. Capture start is durably recorded before snapshot effects;
completion is durable only after publication, resume, positive byte evidence and
revalidation of the same physical owner. Duplicate completion returns the same
receipt without recapture. Interrupted publication or uncertain completion retains
the started record and rejects another capture in that namespace.

Native recovery validates these separate capture records against the original
incoming journal, including unknown/missing/duplicate fields, changed identities
and symlinks. It does not reconstruct a live instance or promote an interrupted
capture. The private RPC validates the original echoed frame, profile, physical
identity and all coupled keys before returning capture evidence. A capture alone
grants no health, smoke, restore, deployment-ready or graph activation authority.
Durable graph completion must additionally bind this capture to matching original
retirement evidence and the required smoke/restore results under current state
fences. Production qualification polling and enforcement remain gated.

Portable qualification checks cover capture replay, failed capture/resume/backing
publication, changed live and physical identity, cancellation joins, racing
delivery and damaged journal recovery. The final original-attempt host checks
pass with race detection. Scheduler qualification
and RPC checks pass 41 top-level tests / 116 cases. The broader portable VM,
wire and RPC regression run passed 2,039 cases with six platform-dependent skips;
the final app/deployment identity guards were subsequently covered by the focused
host checks. Linux lint reports zero issues in the four changed behavior packages.
Protobuf regeneration matches and retains the normalized version-comment policy.
These checks use journal/process fixtures and do not establish native KVM
capture/restore acceptance. Dedicated native `test-metal` and `leakcheck`, durable
graph completion, isolated smoke/restore evidence and graph activation remain
required before enablement.

## Original native snapshot input

The Linux image backend now exposes the original private drive through its
retained source anchor. The input boundary requires the current daemon's
original local generation and daemon ownership lock. Recovered records cannot
become export producers. Under the physical VM lock it checks the original
kernel boot, PID/start time and every durable lease field; under the source lock
it rechecks the same epoch, inode, namespace and single writable binding. The
backend checks the recorded jail, mount identities and permissions, source
metadata and descriptor inode. A replacement source pathname cannot redirect
the reader. Missing, unfinished, shared, read-only or ambiguous inputs fail
closed.

The synchronous input consumer retains both ownership locks until its file
descriptor closes, including on cancellation and failed reads. This is an input
capability for the pending export adapter. It creates no output, proves no VM
pause or capture, and does not relax the native capture preflight. Durable output
creation/publication, writable-image materialisation, complete producer recovery,
supported native lifecycle acceptance and graph completion remain required.

Portable race checks pass 44 top-level tests / 159 cases with no skips across
the input, image ownership and qualification journal boundaries. They cover
replacement paths, original daemon authority, cancellation, damaged bindings,
descriptor closure and retirement blocked by an active reader. Linux lint and
type checking report zero issues. The Linux placement refusal tests require
Linux execution in CI; these local results establish no native mount, VM capture
or restore acceptance. Gateway clients also implement the additive capture RPC;
the full race/load gateway suite passes 2,234 cases with no skips.
