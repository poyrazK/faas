# ADR-1355: Declared rehearsal participants and managed async completion

Status: Accepted, rollout disabled pending native qualification

## Context

ADR-1352 contains commands and ADR-1353/1354 provide isolated database and object
fixtures. An API can delegate an import to a sibling HTTP worker through Gregale's
durable async routes. Merely polling for an idle queue cannot establish which
deployment executed the job or fence new admission while effects are collected.

## Decision

An optional version 1 `managed_work` plan explicitly lists every enrolled workload,
the primary workload, participating async workers, and resource capabilities. The
first adapter supports Gregale managed async-route invocations only. It requires
contained commands, simulated actions, warm execution and explicit fixture data.
Every async participant must execute at least one job. Ordinary HTTP siblings can
be listed with no resource capabilities.

Registration persists the canonical plan atomically with the existing run policy.
The API acknowledges its digest before deployment. Fixtures can be created only
by the named primary. Declared workers receive distinct PostgreSQL logins (the
explicit `read_write` capability for this fixture database) and workload-bound S3
credentials (`read` or `read_write` for this fixture namespace). No live resource,
account token or ordinary provider credential is copied to a worker. An undeclared
workload in the same private service namespace receives no fixture access.

The invocation ledger captures a bounded, retained rehearsal journal, including
the logical workload, app, deployment, payload digest, and each dispatch instance
and outcome. A run uses exactly one deployment per workload. Replacement releases,
missing journal data, cancellation and unknown/lost attempts cannot prove equality.
Dispatch attempts are ownership receipts; they do not prove application side effects.
Database, object and outbound collectors independently establish those effects.

Sealing acquires the existing run advisory lock and closes new invocation admission
before inspecting pending work. Admitted jobs and retries continue to drain with
fixture access. Admission during closing becomes a durable cancelled invocation
and poisons evidence; this preserves refusal evidence even inside a producer
transaction. After sealing, new invocation writes are rejected. Closing cannot be
reversed. Only after admitted work settles does the existing recorder barrier close
effects; fixture collectors seal after complete action and managed-work evidence.
Missing worker or recorder acknowledgements, unfinished retries, unsupported work,
cancelled jobs and deployment mismatches produce inconclusive comparisons.

Comparisons omit generated runtime identities while retaining logical worker,
request identity, retry outcomes, duplicate jobs and object mutation multiplicity.
Each revision receives fresh resources initialized from identical pinned fixtures;
participants share resources only within that revision. Cleanup revokes all worker
database roles and destroys isolated data. Existing resource cleanup/reapers remain
available with admission gates disabled.

## Boundaries and validation

The v1 journal supports at most 64 jobs, 32 dispatch attempts per job and 8 KiB
canonical stored payload per job. These are evidence-format bounds, not plan quotas.
Other queue backends, customer timers, detached processes, cron, trigger records,
multi-deployment routing and arbitrary worker processes are outside this adapter.
It does not establish completion of work that never reaches Gregale admission.
Explicit outcome assertions remain necessary. Closure does not permit new child
jobs: workloads must admit their supported work before collection starts.

Portable acceptance must cover API-to-worker flow, retries and duplicates, resource
authorization, late admission, cancellation, missing execution evidence, deployment
changes, recorder closure and confirmed cleanup against memory and PostgreSQL.
Native KVM/network/secret delivery and cleanup acceptance is required before
`FAAS_REHEARSAL_WORKERS=1`; preceding rehearsal gates also remain required. This
increment does not qualify cold start, resume or fault injection lifecycle behavior.

ADR-1356 adds a separately gated cooperative checkpoint/VM termination contract
for a single managed worker job. Unplanned worker loss and other lifecycle faults
retain the incomplete-evidence rules above.
