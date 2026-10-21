# ADR-20261019120008: Atomic project push-worker handoff

Date: 2026-10-13
Status: Accepted; native runtime qualification pending

## Context

Verified project releases previously admitted HTTP workloads only. Queue routing
already resolves the active project graph, but worker pool reconciliation selected
the newest live deployment independently. A mixed release needs a common admission
boundary and protection against stale reconciliation during cutover and rollback.

## Decision

`gregale deploy --project --atomic --verify --workers` admits HTTP workloads and
exactly one existing production worker with one enabled named-production push
queue binding and one matching queue consumer. It composes with `--config-file`
and `--migrate`. Other worker/consumer shapes remain refused. Binding identity,
configuration hash, previous deployment and bounded stop grace are captured in the
plan and durable submission. The retained deployment must itself resolve to worker
settings with the captured grace budget; a desired-manifest change cannot convert
an older HTTP revision with pinned settings into a worker rollback target. Legacy
unpinned deployments retain the existing app-settings fallback. Old servers cannot silently
accept this mode.

Persist candidate ownership before build notification. Candidates may cold boot
for qualification, but platform production queue delivery continues through the
previous graph. Cutover requires a running worker with matching runtime
configuration fence and deployment input receipt. This proves startup and
configuration delivery; it is not an application-specific business smoke test.
HTTP critical probes retain the existing verification contract.

Graph publication and paired configuration publication share the existing store
transaction. The graph selects both HTTP deployments and worker admission.
Standalone and preview worker selection retains its existing behavior. Project
worker reservation checks the current graph under the project lock, preventing a
stale scheduler from admitting a retired live revision. Isolated candidates remain
held while their durable release is building within its deadline.

Before stopping a project worker, reconciliation claims a persisted drain intent
under the project lock. It refuses instances selected by the current graph,
changed targets and held candidates. Cutover readiness excludes claimed instances,
so an older reconciliation cannot drain the instance used to justify a newer
cutover. The existing schedd stop path signals and retires the old generation
within its configured grace: default 30 seconds, accepted maximum 60 seconds.
Interrupted jobs retain existing lease/retry semantics. No new VM lifecycle owner
or exactly-once job guarantee is introduced.

Transactional deployment-change outbox notifications request worker reconciliation
on cutover and rollback. Periodic reconciliation also repairs project singleton
workers without a scaling policy. Binding changes and operator graph changes
fail closed. Healthy HTTP probes cannot finalize a release whose worker startup
proof has been lost. Rollback restores the retained graph and paired variables;
completed jobs, external effects and migration database effects are preserved.

## Qualification and enablement

APID admission is disabled by default. Set
`FAAS_ATOMIC_WORKER_RELEASES_ENABLED=1` only after native acceptance on an x86 KVM
host. Control-plane tests cover startup failure/pending proof, pre-cutover queue
routing, cutover, worker loss, rollback, binding/operator conflicts, persisted
candidate isolation, stale drain/admission and CLI recovery without resubmission.

Native acceptance must exercise actual candidate startup/configuration delivery,
production push-message isolation before cutover, delivery after cutover, old
worker shutdown within the grace budget, probe-triggered rollback, restart recovery
and leakcheck. The managed development container lacks `/dev/kvm` and visible
native loop devices, so these tests are not claimed as passed here. Arbitrary
customer code can cause external effects during startup; isolation covers platform
production queue admission, not those external systems.

The implemented native qualification is `TestProjectWorkerReleaseMetal` in
`cmd/e2e/project_worker_release_metal_test.go`, selected by the deploy phase and
the blocking `project-worker-release-only` dispatch lane. It covers controller
restart during isolated startup/cutover, actual guest queue delivery and variables,
probe-driven rollback, an unlaunchable worker and ignored-SIGTERM retirement.
See [the native acceptance runbook](../ops/project-worker-release-native-acceptance.md).
This addition does not record native runtime qualification or enable admission.
