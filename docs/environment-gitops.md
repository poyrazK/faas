# Environment GitOps preview

Environment GitOps is an implementation preview under [ADR-568](adr/568-environment-gitops-contract.md). It adds an explicit Git source and field ownership contract to registered project environments. Complete environment enforcement and workload activation are still under development.

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

## Ownership and source maintenance

Customer source creation and updates support `report` mode. A request to enable
`enforce` returns `409 environment_git_enforcement_unavailable`; the dashboard
also disables that choice. Internal executor qualification remains separate.

The Terraform provider reserves the scoped fields managed by `gregale_env` and
`gregale_project_environment_config`, plus workload source intent for scoped
`gregale_deployment` resources. Refresh/import also registers ownership.
Git adoption rejects these fields, and Terraform cannot claim an already adopted
Git field, including in report mode. Variable and secret-reference keys share a
namespace. Other Terraform resource types do not yet register field ownership.
Legacy `default` variables outside a registered environment remain unscoped.
Upgrade the API before upgrading the provider; an API without the ownership
endpoint causes the provider to stop before writing these fields.

Claims contain resource identities and field paths, never values. The authenticated
`PUT /v1/environment-field-ownership` reserves fields with manager `terraform`;
`DELETE` releases the specified claims without changing values. Removing a resource
from Terraform state alone does not release its claims; explicitly release them
before reviewing a Git adoption. A failed write retains its reservation for retry.

Use the dashboard source controls or the CLI to rebind or disconnect:

```sh
gregale projects environments gitops rebind shop staging \
  --expected-generation 3 --manifest-path environments/staging-v2.yaml --yes
gregale projects environments gitops unbind shop staging \
  --expected-generation 5 --yes
```

Both operations require the current generation and report mode, release only the
retired binding's Git ownership and overrides, and preserve existing values,
resource identities, revisions, reports and execution journals. Pending effects,
graph preparation block retirement. Bindings with qualification records cannot
yet be retired by this preview; those records and their execution evidence remain
protected. Rebinding creates
a new verified source with a later generation and requires fresh review, approval
and adoption. Old approvals, plans and leases cannot authorize the replacement.
Retired binding history remains stored for operator inspection; the status page
shows the active binding. Repository identity comes from the project's current
verified GitHub connection. Disconnecting does not delete any workload or queue.

Completed report history keeps at most 1,000 runs per source and seven days of
reports, retaining the latest completed run and all active runs. Pruning happens
when a run is claimed or finishes; approval provenance and execution journals are retained
independently. Canary secret-reference agreement is required only for keys in the
Git definition or existing ownership; unmanaged secret differences do not block
adoption of unrelated fields.
