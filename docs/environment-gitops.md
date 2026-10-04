# Environment GitOps preview

Environment GitOps is an implementation preview under [ADR-567](adr/567-environment-gitops-contract.md). It adds an explicit Git source and field ownership contract to registered project environments. Complete environment enforcement and workload activation are still under development.

## Current scope

| Area | Implemented | Remaining before complete environment enforcement |
| --- | --- | --- |
| Git source | Immutable definition review, approval, polling and separate source freshness | Full outage and rollback journey with serving workloads |
| Ownership and adoption | Reviewed adoption plans, stable resource identities, stale-plan rejection and scoped ownership guards | Remaining workload source and service-binding adapters |
| Drift reporting | Opt-in continuous reports against the last approved definition, durable runs and restart recovery | Production report-mode acceptance for the complete API/worker/queue graph |
| Workload preparation | Scoped intent, immutable preparation inputs and durable qualification handoffs | Complete smoke, capture/restore and qualification receipts |
| Native execution | Fenced journals and private snapshot input/output ownership primitives | Native publication integration and dedicated KVM acceptance |
| Enforcement | Transactional ownership, override and execution fences | Production reconciliation worker, graph activation and serving convergence |

Approved Git content, observed intent, qualification and serving state are separate facts. A reviewed or adopted definition does not mean that a deployment is ready or that the environment is serving that revision. Unqualified owned source/runtime fields prevent the applied revision from advancing.

## Try review and reporting

Use an existing GitHub-linked project and registered environment. Start in `report` mode with manual approval and pruning disabled:

```sh
gregale projects environments gitops bind shop staging \
  --manifest-path environments/staging.yaml --mode report --approval-policy manual
gregale projects environments gitops review shop staging \
  --commit "$REVIEWED_COMMIT" > revision-review.json
```

`REVIEWED_COMMIT` must be a complete immutable commit SHA. Inspect the returned definition digest and generation before approving its receipt:

```sh
gregale projects environments gitops approve shop staging \
  --file revision-review.json --yes
gregale projects environments gitops adoption-preview shop staging > adoption-plan.json
```

Inspect the mapped resource IDs, owned fields and blocking reasons. Adoption preserves existing values and transfers the reviewed ownership; it does not activate a new workload:

```sh
gregale projects environments gitops adopt shop staging --file adoption-plan.json --yes
gregale projects environments gitops status shop staging
```

Stale or blocked receipts are rejected. Re-run the preview after changing relevant intent or source controls.

An operator must opt apid into continuous reports with `FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED=true`. Reporting is disabled by default and never claims enforcement work or performs workload activation. Git source polling defaults on; `FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED=false` disables it. A source outage retains the last approved definition and is reported separately from drift.

## Execution gates

Production qualification polling, graph activation and continuous enforcement are not enabled by this preview. Native process recovery remains an explicit vmmd opt-in (`native_process_recovery`, default `false`). The native capture backend rejects unsupported snapshot publication before pause or capture effects.

The native primitives have portable tests and Linux compile checks, but those do not establish Firecracker capture/restore acceptance. VM lifecycle completion requires `make test-metal` and `make leakcheck` on a dedicated native x86_64 Linux KVM host, as required by [the repository guide](../CLAUDE.md). Keep these gates closed until native publication, complete graph qualification, recovery and serving evidence pass.
