# ADR-20261019120013: Staged project canary publication and transactional promotion

## Status

Implemented behind explicit `--progressive` and the default-off
`FAAS_PROJECT_CANARIES_ENABLED=1` qualification gate. Worker releases also retain
ADR-20261019120008's separate disabled-by-default admission gate. Native qualification is pending.

## Context

ADR-20261019120010 defined progressive policy; ADR-20261019120011 and ADR-20261019120012 added durable job pins and
retained worker recovery. Publication still immediately replaced the active graph.
A staged rollout must keep production's baseline graph and configuration head active
while serving candidate cohorts, then switch graph, configuration and checkpoint together.

## Decision

`gregale deploy --project --atomic --verify --progressive --config-file release.json`
requests progressive publication. The first contract requires paired non-secret
configuration snapshots; including `--workers` additionally requires `--worker-health`.
Existing complete-project, source, readiness, backward-compatible migration, binding,
retention and immutable runtime configuration guards remain prerequisites. Scan policy,
apply identity and recovery journals bind the progressive requirement while excluding
mutable stage percentage and observation generation. An older server omitting that
requirement is rejected before apply.

Publish a qualified candidate graph inside the existing guarded submission transaction,
retain it as an inactive graph, restore the baseline default, and persist an operational
canary checkpoint before committing. No intermediate graph activation is visible outside
the transaction. At 5% and 25%, the configuration head stays at the previous version;
candidate deployment snapshots already contain the target values. Each staged graph is
retained through its current verification deadline plus the release's compatibility TTL.

New unpinned HTTP requests use a server-generated cohort key. Named production worker
jobs use their ledger-owned replay root at first claimed delivery, persist the selected
graph, and reuse it on retries/replays. The same key chooses the same whole graph across
applications. Canonical release headers propagate through managed calls. Async HTTP
ingress also captures the selected graph before persisting its invocation. Explicit
release/revision pins keep their existing compatibility semantics.

The controller collects stage-specific HTTP and worker evidence and uses ADR-20261019120010's
generation/checkpoint fencing. Each stage requires two fresh, separated healthy rounds.
Baseline worker configuration proof remains necessary for safe restoration. Unknown
evidence and low queue traffic cannot promote a stage. Worker deployments participating
in a live canary receive ADR-20261019120012's bounded recovery plans even before any job chooses
them; eligibility, admission and last-resident drain guards share the same ownership
and retention conditions. Existing account, placement and concurrency limits remain enforced.

At 100%, run the same current project membership, live member, retention, GitOps and
binding validation as ordinary publication, activate the existing candidate graph ID,
append the target configuration version, and commit the new observation checkpoint and
public receipt together. Previously admitted pins therefore keep their graph ID. Full
traffic receives another independent verification stage before success is reported.

Before full activation, stopping a canary preserves the baseline graph/head. After full
activation, regression restores a fresh baseline graph and the previous configuration
values in one transaction. Candidate jobs and explicit compatibility pins retain their
original graph until its existing retention bound; they are never silently retargeted.
Stopping a rollout prevents new unassigned candidate admissions, not intentional use of
an existing retained version pin. Unconfirmed baseline health requires operator action.
Expired preactivation checkpoints admit only the unchanged baseline; expired full-traffic
checkpoints fail closed for unassigned work until recovery. A newer operator graph or
configuration version cannot be overwritten, including a configuration ABA change.

## Persistence and recovery

`project_release_canaries` stores account/project/attempt identity, the private policy
checkpoint, optional worker identity and status. It contains no configuration values or
job payloads and is classified as operational data for environment clones. A partial
unique index permits one running canary per project; routing has a separate bounded
project/status index. Receipt/project locks plus generation CAS prevent concurrent
controllers from counting one observation twice or appending a configuration twice.

Lost responses and controller restarts read the existing graph, stage and generation.
The checkpoint is committed with graph/configuration changes; no external workflow or
process-local promotion counter supplies authority.

## Validation and qualification

Memory and migrated PostgreSQL tests exercise staging, whole-graph selection, 5/25/100
promotion, immutable baseline configuration, early stop, full rollback, operator ABA
guards, stale observations, worker cohort eligibility and pinned retries after abort.
A PostgreSQL trigger forces checkpoint failure after graph/configuration activation;
all three changes roll back. Removing it and restarting the store completes promotion
once, and retrying the old observation does not append another configuration version.
CLI tests reject missing/changed server policy and accept stage progress without changing
journal identity. Gateway tests cover ingress selection and explicit-pin precedence.

Production admission stays off until native x86_64 KVM acceptance verifies mixed routing,
worker loss/recovery, actual job outcomes, controller interruption, graph/configuration
rollback and resource cleanup. Container/unit evidence cannot replace that qualification.
