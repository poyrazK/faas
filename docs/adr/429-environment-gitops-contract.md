# ADR-429 · Git-owned environment intent and continuous reconciliation

- **Status:** implementation in progress
- **Date:** 2026-09-30
- **Migration reference:** Earlier unreleased GitOps migration comments using
  ADR-387 and ADR-425, and this branch's earlier ADR-393, ADR-423, ADR-425 and ADR-428
  documents, refer to this contract.
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
   immutable live image. Source/archive builders and new workload creation still
   need their preparation adapters.
2. Prepare the dependency graph using immutable source artifacts and durable
   deployment/effect identities. Qualify API and worker candidates before release
   activation. Retry and crash recovery resume the journaled operation; an older
   generation cannot activate a replacement approved graph. Release coordination
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
   public clients. Short and numeric catalog names currently expose differing
   legacy scope grammars. The default and all-scopes sentinel semantics must
   remain explicit; changing only the Git parser cannot close this gate.
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

These controls currently accept environment names of 3–33 characters, matching
the intersection of the catalog and sealed-secret scope contracts. Catalog names
of one or two characters remain an integration gate; the dashboard identifies
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
worker is not started from apid until these integration contracts are wired;
candidate discovery is running independently.

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
uniqueness pass. The contract is now ADR-429; historical unreleased migration
references to ADR-425 remain unchanged.

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
