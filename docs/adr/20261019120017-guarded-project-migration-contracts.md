# ADR-20261019120017: Guarded schema contract after expansion and rollout

## Status

Implemented behind `FAAS_PROJECT_MIGRATION_CONTRACTS_ENABLED=1`, default off.
Native SQL migration, task isolation and zero-leak qualification remain pending.
The [native contract acceptance lane](../ops/project-migration-contract-native-acceptance.md)
now covers success, guest failure and owner-loss scenarios; adding the harness
does not establish native qualification.
The [managed SQL qualification lane](../ops/project-migration-sql-native-acceptance.md)
adds real schema/credential and transactional interruption coverage; native results
remain pending.
Existing progressive and worker admission flags remain independent and unchanged.

## Context

Expansion and successful rollout cannot authorize destructive schema cleanup while
retained deployments, pinned work or old instances can still execute. A cleanup
command must also use the artifact and runtime inputs of the expansion, rather
than silently following a changed credential or database binding.

## Decision

The selected expansion workload may declare `release.contract_command` in
`gregale.yaml`. A multi-workload project keeps its single Procfile expansion
owner; a manifest with only `contract_command` supplies cleanup for that owner.
Contract planning requires `--migrate --atomic --verify --progressive --stabilize`
and paired configuration. Ordinary deployment never runs this command. Its shell
form is validated through existing app-task bounds and its immutable hash binds
scan/apply and CLI journal identity. The raw command stays in operational server
storage; it is absent from public deployment receipts.

The scheduler attests the expansion task's prepared runtime configuration fence
before restoring its task VM. The private contract record captures this fence
once, against the owned workload submission, planned expansion hash, unique task
and restoration lease. The running transition rejects absent or changed runtime
attestation. Contract admission and its VM preparation must reproduce the same
artifact, deployed variables, secret grants and sealed credential fingerprints.
Credential rotation or resealing is conservatively treated as drift. This initial
contract does not infer that two different credentials identify the same database.
A fingerprint includes no plaintext values in the public receipt.
Migration-only credential grants also reach server-recorded contract tasks;
ordinary manual tasks cannot obtain them by declaring a migration command.

`gregale deploy contract ATTEMPT_ID` submits an explicit request ID and inspected
checkpoint generation through the existing MFA-protected deployment write route.
The API performs fresh, attributed critical probes on the current production
graph. A lost-response retry of an admitted request returns its original task,
without another probe requirement or another command execution. A different
request or generation cannot enqueue a second cleanup for this attempt.

Admission locks the submission, configuration environment, project and apps in
the established order, then repeats the following barriers:

- Rollout succeeded and stabilization completed with healthy last evidence.
- The active graph and paired configuration still belong to this attempt.
- Fresh current probe evidence and runtime fingerprints match their owners.
- All other production graphs and revision/operation/workflow code pins expired;
  durable operation/workflow references no longer retain old code.
- Old revisions receive no fallback traffic. Project production invocations and
  app tasks drained; old instances are stopped, failed or parked.
- Old started tasks without an exit receipt block cleanup as uncertain work.
- No competing source build, submission or running canary can publish old intent.

In the same transaction, admission inserts one manual task pinned to the current
qualified artifact and permanently retires every existing non-current production
deployment ID. The existing scheduler owns task execution, fresh VM allocation,
ordinary resource caps and lifecycle. No API-to-VMM call or capacity exception is
introduced. Retry count is zero; the task must start inside
`ProjectMigrationContractStartTimeout`, the existing probe freshness window.
A running task with lost execution evidence remains uncertain and is not replayed.

Retirement fences survive success, failure, cancellation and uncertainty. Release
publication/resolution, explicit revision routing, worker pins, runtime projection,
app-task admission and instance transitions reject retired IDs. Parked metadata
may remain, but cannot wake. PostgreSQL triggers serialize relevant admission
with the project row and recheck fences after lock waits, closing a stale wake or
pin renewal racing cleanup. Benign zero-traffic maintenance remains possible.
New production graph/configuration publication and unrelated task claims wait
while cleanup is outstanding. Prepared task leases prevent older schedulers from
dispatching an unattested contract. Migration Down refuses to remove fences after
any cleanup was admitted. Deleting the contract owner app does not remove fences;
they live until the owning project/account is deleted.

The deployment receipt retains rollout success and exposes a separate contract
status, blocking reason, request/generation, task ID, retirement count and terminal
outcome. `deploy status` shows these fields. Acceptance of `deploy contract` is
asynchronous; inspect status and the existing app-task log resource for completion.

## Consequences and validation

Operators declare cleanup compatible with the current release and choose probes
that exercise database-dependent paths. This checks attributed route health and
platform-owned code/work retention; it does not prove arbitrary SQL semantics,
coordinate outside projects or promise exactly-once external database effects.
There is no force override or schema reversal. Failed/uncertain cleanup needs
operator investigation and a new explicit deployment for a new cleanup attempt.

Memory and real PostgreSQL checks cover barriers, source and generation binding,
health/fingerprint drift, atomic rollback, store reconstruction, lost responses,
missing runtime attestation and permanent fencing after terminal outcomes.
Scheduler tests cover the command-free runtime hook. API/CLI and generated-client
checks cover authorization, strict request bodies, acknowledgment and status.
Native qualification must exercise actual guest expansion/cleanup with delivered
SQL credentials, credential drift, an old wake racing admission, task teardown,
restart/lost-result recovery and zero leaked VM resources before enabling the flag.
