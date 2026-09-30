# ADR-382 · Git-owned environment intent and continuous reconciliation

- **Status:** implementation in progress
- **Date:** 2026-09-30
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

## Implementation checkpoint

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
process start boundaries, boot readiness, and restorable scoped snapshots before
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

The remaining full feature gates include source polling and protected-branch
approval evidence; environment-scoped workload creation, source/runtime,
secret-reference and queue/service-binding adapters; staged graph qualification
and release activation; native serving-fleet and guest runtime evidence; source freshness and
operational status integration; and native runtime acceptance.
Unsupported resource fields currently block the complete plan. The source
worker is not started from apid until these integration contracts are wired.

## Review and control workflow

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
