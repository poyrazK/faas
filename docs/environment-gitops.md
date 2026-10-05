# Environment GitOps preview

Environment GitOps is an implementation preview under [ADR-568](adr/568-environment-gitops-contract.md). It adds an explicit Git source and field ownership contract to registered project environments. Complete environment enforcement and workload activation are still under development.

## Current scope

| Area | Implemented | Remaining before complete environment enforcement |
| --- | --- | --- |
| Git source | Immutable definition review, approval, polling and separate source freshness | Full outage and rollback journey with serving workloads |
| Ownership and adoption | Reviewed adoption plans, stable resource identities, stale-plan rejection and scoped ownership guards | Scoped binding execution and inherited non-image provenance |
| Drift reporting | Opt-in continuous reports against the last approved definition, durable runs and restart recovery | Production report-mode acceptance for the complete API/worker/queue graph |
| Workload preparation | Held image, pinned source and function candidates; atomic private workload reservations; private HTTP graph execution primitive | Qualified binding delivery and inherited non-image provenance |
| Qualification evidence | Immutable attempt-bound capture receipts and a graph evidence assessment that reports missing proof | Isolated smoke, restored readiness and native capture publication |
| Native execution | Fenced journals, persistent disk staging and publication intent, original-process API control, internal pause/create/freeze/resume and exclusive four-object publication | Production adapter wiring, artifact-generation/cleanup receipts and real capture/restore acceptance |
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

Finishing a report preserves a pending wakeup when intent changed after its final
observation. The next worker poll can claim that source immediately, including
after an observation failure. Changes included in the final observation keep the
normal reporting cadence. A plan using a temporary override schedules its next
check no later than that override's expiry; an override that expires during the
run triggers a fresh check at the next poll. Completed reports remain historical
observations, and do not certify changes made after their observed intent version.

## Next implementation steps

1. Complete inherited non-image source provenance and private binding transport.
   Bind the transport to the original environment UUID, candidate graph, target
   identity and authorization policy. Binding candidates remain excluded from
   dispatch until this adapter exists.
2. Complete qualification receipts, smoke checks and capture/restore evidence,
   including native snapshot publication and dedicated KVM recovery acceptance.
3. Add graph activation and serving convergence evidence, then qualify the
   continuous enforcement worker against complete API/worker/queue environments,
   superseded revisions, source outages, override expiry and process restarts.

## Workload preparation contract

An explicit source can reserve a missing workload under the internal enforce
executor. Reservations are private apps with no deployment or serving VM, use
the original environment UUID and logical name, and commit the complete cohort
with its mappings, scoped intent, ownership and progress journal. Quota or name
collisions roll the cohort back. An existing mapped identity that disappears
requires reviewed recovery; it is never replaced silently. Report mode creates
no resources, and the customer enforcement gate remains closed.

Function sources declare a supported runner and a Git directory:

```yaml
workloads:
  transform:
    source:
      kind: function
      runtime: node22
      directory: functions/transform
    runtime:
      port: 8080
```

The directory comes from the exact approved commit archive. Candidate creation
freezes the runner, source revision, build handoff and original app identity.
Changing an existing app's type or function runner requires a separate reviewed
decision; a scoped source cannot change that shared metadata.

Service-binding intent stores each logical target together with its mapped app
ID in the original environment. That identity is frozen into held candidates.
Binding keys share the variable/reference quota across the app's environments
and cannot occupy an existing variable or secret-reference key. Enforced
bindings reserve those keys against ordinary writes. Shared app bindings and
cross-environment aliases cannot substitute for scoped targets.

An internal scheduler graph window now keeps the complete claimed qualification
cohort alive in dependency order. An explicitly configured node-local bridge
injects private HTTP binding URLs into guest boot inputs. The guest listener
derives the original caller from a fresh network-slot lookup and checks both
original execution frames, current leases, the reviewed graph, fresh runtime
acknowledgements and frozen target caller policies on every call. Revocation
cancels active requests. It forwards to the exact qualified target and supplies
no ordinary service wake, replica retry or serving fallback. Ordinary HTTP,
service TCP and service discovery reject qualification callers. Every admitted
VM is retired when the graph callback returns, including failure paths.

This primitive is not wired into production qualification dispatch. It supports
HTTP/1 binding calls after runtime publication, explicit reviewed target ports and
acyclic prepared dependency cohorts. Private HTTPS DNS/certificate delivery,
dependency calls during pre-readiness startup, binding delivery receipts,
capture/restore and the transition to serving binding URLs remain outstanding.
The existing verified `.internal` HTTPS contract is preserved; an HTTPS policy
blocks this HTTP-only adapter before graph VM effects.
HTTP/2 and gRPC targets are also rejected before graph boot and private routing.

## Qualification and activation evidence

A capture receipt records the original attempt, instance, node, wake, immutable
artifact, fresh runtime input acknowledgement and private capture namespace.
It is immutable and retryable after a lost commit response. Retirement must
identify the same native generation and kernel boot that produced the capture;
the receipt remains historical after retirement.

The internal graph assessment reports prepared artifacts and current capture
counts separately. A partial cohort, changed artifact or stale runtime input
cannot borrow another member's evidence. Even a complete capture cohort remains
unqualified until isolated smoke and restored readiness are proven. Activation
and serving convergence need their own committed receipts. No capture receipt
releases a held deployment, emits an ordinary deployment-ready event, or
advances the applied Git revision.

## Execution gates

Production qualification polling, graph activation and continuous enforcement are not enabled by this preview. Native process recovery remains an explicit vmmd opt-in (`native_process_recovery`, default `false`). The native capture backend rejects unsupported snapshot publication before pause or capture effects.

The native primitives have portable tests and Linux compile checks, but those do not establish Firecracker capture/restore acceptance. The user authorized the internal nested KVM node for this hardening work; its privileged test results are recorded separately from bare-metal evidence. Keep these gates closed until native publication, complete graph qualification, recovery and serving evidence pass.

Private writable drives and capture outputs default to requiring their data and
native journal on the same filesystem. An explicit startup-only disk adapter,
`WithNativeImageStagingRoot` configured before `WithNativeProcessRecovery`, can
instead own their temporary names in a pre-existing private `0700` disk directory
on the original ext4/XFS/Btrfs filesystem, outside the jail. It persists an
exclusive original inode/epoch claim before creating a link and holds a separate
daemon ownership lock. Ordinary preparation removes the link and claim before
returning; producer death retains them for original-owner retirement.

Same-boot recovery requires the original image journal. A missing journal,
substitution, symlink, additional alias, changed directory or unknown entry
blocks cleanup. After a different kernel boot, the disk claim permits only
removal of its original temporary name; it supplies no VM, capture or permission
authority. Recovery validates the complete disk directory before changing it.
Without that opt-in, the tmpfs/disk mismatch is still refused before preparation.
This adapter is not wired into production vmmd configuration. Passing ownership
and output-read fixtures does not enable production native capture.

The Linux snapshot control primitive pins the original process with a pidfd,
checks the API socket's kernel PID/UID/GID credentials before sending bytes,
and sends each pause, create or resume request once. Snapshot creation requires
the original private drive and both original writable output bindings. A lost
response supplies no success or retry authority. The internal capture producer
uses these controls in one original pause/create/freeze/resume interval and
publishes the complete four-object cohort synchronously after confirmed resume.

Optional exclusive storage writers stream GCS capture data and compression
without named scratch files, or copy into an anonymous local disk inode and
publish only the completed file. Both refuse replacement. Routing retains the
selected canonical backend; the cache wrapper skips its named spool path.
Unsupported delegates refuse the capability without falling back to ordinary
writes. An internal adapter connects original memory/device-state readers to
these writers and retains their ownership locks through publication. The native
capture entry point remains gated pending artifact receipts and real VM
capture/restore acceptance. Publication errors may follow an
uncertain commit, so they supply no overwrite or deletion authority.

`WithNativeSnapshotPublicationRoot`, configured before native recovery, adds a
separate pre-existing private disk directory for immutable capture intent. The
original successful begin pins the storage backend and persists the complete
attempt, physical process, lease and four logical object keys before output
writes. Writes require that original capability and unchanged intent inode.
Startup validates old intents without replaying or deleting them. Losing the
jail journal or rebooting cannot create publication or cleanup authority from
an intent. Backend-specific object receipts and artifact retirement remain
outstanding, along with restore/smoke evidence. The internal producer begins
intent before output preparation, retains the original VM memory fence and
publishes memory, device state, the frozen private drive and backing identity.
Live output preparation validates the original process's jail inode and VM
UID/GID through a retained pidfd. Uncertain controls are never replayed; partial
objects remain retained without granting cleanup authority.

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
Deployment cancellation, destruction and missing history also retain the source
reservation, because another Terraform deployment may manage the same scoped
source. Release that reservation explicitly during a reviewed handoff to Git.

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
