# Gregale Run and Agents roadmap

**Status:** working product and engineering plan

**Date:** 2026-11-14

**Audience:** product, CLI and SDK, control plane, runtime, billing and operations

Gregale Run provides disposable task execution for agent-powered applications.
Customers submit a task and receive a receipt, structured results, artifacts and
usage. It works independently of Gregale Agents, and becomes a task primitive
for customers who also adopt the hosted agent service.

## Product boundaries

| Product | Customer job | Lifecycle |
| --- | --- | --- |
| Run | Execute a bounded source or OCI task and return results | Fresh disposable VM per task |
| Sandboxes | Give an external agent commands, files and a reusable workspace | Multi-command session with explicit persistence and expiry |
| Agents | Host customer-supplied agent logic and deliver its results | Event-driven slices or a hosted harness, with durable orchestration |

Images, artifact access, identity, policy, usage and execution lineage should be
shared where their contracts match. Each product keeps its own lifecycle owner.
Conversation state, timers and approvals belong to Agents and the existing
workflow foundation. Interactive terminals and persistent workspaces belong to
Sandboxes. Existing CLI aliases and APIs remain compatible during naming work.

The first target customer is a team building an agent-powered SaaS product.
The initial product promise is **disposable compute for agent tools, with
structured results, artifacts and per-customer controls**.

## Baseline

- Source Runs are preview; image runs and Agents remain internally gated.
- This branch replaces the source Run response cache with atomic 24-hour
  admission identities. Runs-only replay keys remain scoped by API-key family.
- Mainline already checks unsuccessful terminal Run statuses in JSON wait mode
  and covers them in `TestRunRegressionSourceRunJSONFailureExit` (#7066).
- Sandbox sessions, SDK adapters, E2B shims, templates and workspace persistence
  have implementation. Publication and deployed qualification remain separate
  milestones; see [sandbox rollout](adr/20261027090013-sandbox-sessions.md#rollout).

## Prioritized releases

| Priority | Increment | Release gate |
| --- | --- | --- |
| P0.1 | Source CLI retry key, local validation, uncertain-submission guidance and regression tests | A lost receipt can be retried explicitly with the same wire key and unchanged request; ordinary invocations keep distinct keys |
| P0.2 | Durable source admission identity | Admission and deduplication commit atomically; concurrent and lost-acknowledgement retries recover one Run without consuming quota or artifact grants twice |
| P0.3 | Deployed lifecycle qualification | Source and OCI journeys cover cancellation, expiry, restart, reconnect, cleanup and usage reconciliation on native KVM |
| P1.1 | Useful task environments and outputs | Versioned profiles/templates, an optional longer execution class, stored artifacts and standalone result schemas complete three representative customer tasks |
| P1.2 | Sandbox beta and adoption | Published SDKs support a deployed clone/install/edit/test/private-preview journey; stdin/PTY, persistence and compatibility semantics are tested |
| P2.1 | One complete hosted Agents journey | An event-driven agent submits work, releases its compute, resumes on completion and delivers under a documented deduplication contract |
| P2.2 | Customer controls and economics | Run, Sandbox and Agent work share tenant attribution, enforceable execution/model/tool budgets, scoped access and inspectable usage |
| P3 | Gregale-native contexts | Versioned source/dependency contexts and permitted access to apps, logs and deployments drive repeated production usage |

### P0.1 client retry identity

The CLI exposes `gregale run --idempotency-key KEY` for source Runs and uses
the existing SDK request context and atomic source admission. It validates keys
before source or stdin reads, keeps keys off status reads, and gives human
callers inspection guidance when submission has an uncertain outcome. JSON
errors retain their existing machine-readable envelope.

The flag does not add automatic resubmission, extend replay retention or change
the image-run attachment contract. Source-only flags must not consume a flag
passed to the command inside an OCI image.

### P0.2 durable admission identity

Implemented locally; pending review and rollout. Source Run identities now
commit with quota checks, grant redemption, sealed payload, receipt and initial
event. Replays resolve the current receipt before preparing another request.
Persistence failures fail closed. The 24-hour retention window remains.

The [admission ADR](adr/20261113010000-atomic-source-run-admission.md) defines
scope, fingerprinting, key rotation, legacy cache fencing, and the additive
migration. Apply its cutover requirements before promising atomic replay to
customers. Native runtime qualification remains P0.3.

Acceptance must cover concurrent clients, lost commit acknowledgements,
process restart, quota saturation, terminal receipts, key-family isolation and
single-use artifact grants on both memory and PostgreSQL stores. A replay must
recover the original Run before checking quota or redeeming grants again.
External effects still require the destination's own idempotency contract.

### P0.3 lifecycle qualification

The [functional acceptance report](ops/evidence/20261113-run-lifecycle/report.json)
records a passing exact-source run on `gregale-internal-test-1` in
`gregale-prod`: all 11 Node 24 source scenarios, three OCI journeys including
pipes/PTY/disconnect cancellation, and initial/final resource leakchecks passed
with race instrumentation. Source cleanup was also checked before harness
shutdown. The [operator lane](ops/run-lifecycle-acceptance.md) reproduces this
bounded matrix. Qualification found and fixed failed-result normalization and
missing boot-base preparation for the first OCI Run on an empty store.

The [warm/OOM follow-up](ops/evidence/20261113-run-warm-oom/README.md) passed
all seven Node 24 standard-profile scenarios on the same node: payload-free
capture, repeated isolated restores, real kernel OOM on cold and restored
guests, recovery after OOM, corruption fallback for memory and VM state, and
payload/usage reconciliation. Native wake logs prove the warm cases restored;
all three physical cleanup checks passed. Guest changes add the pristine
resume fence and kernel-backed OOM attribution. That historical run used a
trusted operator fixture to publish its template.

The [production-template follow-up](ops/evidence/20261113-run-template-clones/README.md)
passed nine template/clone scenarios and repeated all seven warm/OOM scenarios
on the same internal node. Schedd's opt-in worker now captures a new,
payload-free guest through vmmd, verifies memory, VM state, private drive and
backing identity, and publishes an immutable format-2 catalog entry pinned to
the Firecracker version. Private artifacts cannot be overwritten by the
publisher; catalog-aware GC protects them. Four simultaneous restored Runs
had separate process credentials, jail roots, writable drives, capped cgroups,
scratch and RNG output. Cancelling one clone preserved its live peer. All
physical cleanup checks passed. This qualifies the local producer and bounded
same-node behavior with fixture image admission; production enablement and
approved runtime image provenance remain separate gates.

The [native-process follow-up](ops/evidence/20261114-run-native-recovery/README.md)
passed all 26 scenarios on the same node: ten native crash/restart cases,
nine template/clone cases and seven warm/OOM cases. This profile enables
`native_process_recovery`, `native_launch_owner_records` and
`native_runtime_capture`. Durable private flights retire interrupted template
guests before admission reopens; incomplete captures are never promoted.
Published templates survive owner restart. Interrupted customer Runs execute
once, retain their original receipt, and physically retire after vmmd or
schedd death. All 27 customer Runs reconcile one usage entry and erase their
payload; template accounts record zero Runs. The lane also verifies KVM access
without TUN for every native wake and repeats concurrent clone isolation.

P0.3 remains open. This GCP host reports Google virtualization, which does not
satisfy the repository's native acceptance requirement. The earlier source
restart lane uses version-7 adoption; the new lane separately exercises native
recovery. Default quarantine-only recovery is not qualified for unattended
retirement.

The [release preflight and rollback tooling](ops/run-release-preflight.md)
now prepares hash-pinned plans, checks both binary reader manifests, validates
the candidate and writer-disabled rollback configurations with vmmd's real
loader, inventories retained roots without mutation, and distinguishes
production enablement from a compatible rollback. It does not supply native
qualification or activate a service. Production preflight deliberately refuses
the functional-only internal-node evidence. Next gates, in order:

1. Qualify the intended production recovery configuration on a supported
   non-virtualized x86_64 KVM host, including compatible native authority/device
   readers and rollback gates before enabling the new writers.
2. Repeat template publication, clone isolation and warm/OOM qualification on
   the supported native host using approved runtime images. Add the remaining
   language/profile cohorts. The native producer is now implemented and
   functionally exercised; production enablement remains gated by that evidence.
3. Verify deployed reconnect, host reboot, cross-node recovery and complete billing
   reconciliation. The isolated-schema lane verifies the source execution
   ledger; it does not qualify public usage rollups or invoices.

## Task usefulness and distribution

Keep a short default and qualify the optional five-minute execution class under
measured capacity. Report queue, startup and execution timing separately. Reuse
immutable template/image versions between Run and Sandboxes.
The current feature increment implements stored file inputs/outputs outside
inline JSON, with checksums, key-family access, scoped grants, independent
storage quotas and 24-hour retention. Hobby/Pro/Scale allow 8/32/64 MiB per file,
combined input and combined stored output per Run. Standalone input and result
schemas reuse the workflow validator; a result violation preserves the receipt
and causes CLI waits and dependent workflow steps to fail. API, CLI and SDK
support is implemented behind `FAAS_EXECUTION_V2_ENABLED=1`, default off.
The [file/contract verification](ops/evidence/20261114-run-files-contracts/README.md)
records the passing bounded Node 24 real-VM journey, unit/race matrix and lint.
The [file/contract ADR](adr/20261114030000-run-stored-files-and-contracts.md)
defines the contract; the [acceptance lane](ops/run-lifecycle-acceptance.md#stored-files-and-standalone-contracts-cohort)
defines the real-VM gate. This increment does not widen production eligibility.

The next preview increment implements `limits.execution_class=extended` with a
300-second admission-to-result budget and separate bounded dispatch capacity.
Immutable dependency environments build hash-pinned Python wheels or locked
npm packages in isolated builder VMs, retain producer and vulnerability evidence,
and give each Run a fresh writable guest. API, CLI and SDK selection share the
same scoped immutable identity. Both new writer gates default off. The
[class/environment ADR](adr/20261115100000-run-extended-class-and-environments.md)
defines limits and rollout; the
[acceptance cohort](ops/run-lifecycle-acceptance.md#five-minute-class-and-dependency-environments)
requires real builds, imports, the full five-minute deadline, cancellation,
retirement and leak checks before widening availability. Production image
qualification and measured fleet capacity remain separate gates.
The [2026-11-15 verification](ops/evidence/20261115-run-extended-environments/README.md)
passes all eight bounded real-VM scenarios on the internal node, including
actual npm/Python builds and the full deadline. The host's Google virtualization
does not confer native production eligibility.

The following increment adds bounded environment diagnostics with durable
cursors, explicit retention gaps and CLI/Go/Node/Python watches. Completion
atomically enters the existing account webhook delivery ledger as
`execution.finished`, with metadata-only receipts, signed retries, dead letters
and redrive. Agents can release their compute while work runs and resume from a
verified, deduplicated completion event. Account receiver management remains
separate from runs-only credentials. See the
[diagnostics and completion ADR](adr/20261115160000-run-diagnostics-and-completion.md)
and [usage guide](executions.md#build-diagnostics). New subscriptions retain the
Run API/V2 preview gates; disabling writers preserves reads and pending deliveries.
The hosted Agents service still needs its own persisted resume and deduplication
journey using these primitives.
The [nine-case verification](ops/evidence/20261115-run-diagnostics-completion/README.md)
passes live diagnostics, signed callback retries across an actual scheduler
restart and zero-leak cleanup on the same internal node. Preview gates remain off
by default; this does not widen native production eligibility.

Publish bounded trial access, focused TypeScript/Python guides and three useful
examples: a data report, a schema validator and an agent tool with an artifact
handoff. Document the tested E2B-compatible subset, including differences in
timeouts, pause, snapshots, process handles and stdin. Preserve customers'
choice of agent framework and model provider.

## Qualification and commercial gates

- Activation target: a useful result or artifact within ten minutes of signup.
- Publish warm and cold startup latency separately, alongside queue delay and
  the load under which each was measured.
- Exercise failure recovery and prove VM/resource cleanup before widening
  availability. Use native KVM and leak checks for lifecycle changes.
- Measure infrastructure failure rate separately from customer-code failures,
  budget refusals and provider errors.
- Track repeat usage and total cost per successful customer task, including
  retries, artifact storage and retained state.
- Validate measured-memory pricing, floors and pause exclusions against the
  financial model before publishing a rate or savings claim.

GPU fleets, desktop computer use and broad compatibility expansion follow
customer demand. Product availability continues to come from the capability
registry; completing a roadmap item does not enable an installation.

The task catalog and Agent continuation increment is implemented locally. Explicit immutable versions, sealed definitions, schema and resource contracts, CLI/SDK invocation, endpoint delegation and durable RAP completion now compose the existing Source Run lifecycle. The [functional KVM journey](ops/evidence/20261115-run-tasks/README.md) passed across process restarts, retirement and writer rollback. See [task contract](run-tasks.md) and [ADR](adr/20261115180000-versioned-run-tasks-and-agent-continuations.md). Writers remain off pending review and controlled rollout.

Customer/agent attribution and execution allowances are implemented for source
Runs and named tasks. Trusted key-family and Agent-endpoint bindings, atomic
customer/task concurrency and daily invocation/compute limits, metadata history
and durable threshold alerts now compose task invocation. The
[Run controls guide](run-controls.md) defines admission, reservation settlement
and rollback; the [verification](ops/evidence/20261116-run-controls/README.md)
passes the exact-source real-VM continuation and five Linux lint groups. Preview
writers remain off. Sandbox sessions, OCI/Job Runs and model-token spending keep
their respective admission paths; native production runtime eligibility remains
separate from this functional evidence.

## Named task pipelines and bounded retries (2026-11-16)

Implemented behind the default-off pipeline gate: version-pinned task DAGs,
16-item batch expansion, inherited customer/task budgets, stored artifact handoffs,
bounded infrastructure retries with separate Run IDs, durable cancellation fences,
and absolute pipeline deadlines. See [the contract](run-pipelines.md) and
[the decision](adr/20261116160000-run-task-pipelines-and-bounded-retries.md).
Qualification status is recorded separately from implementation status.

## Whole-pipeline completion and Agent continuation (2026-11-16)

The implemented preview increment adds an immutable pipeline completion manifest,
transactional `execution.pipeline.finished` account notifications, and one durable
RAP `gregale_pipeline` result for the complete graph. Delegated tasks retain live
endpoint attribution and customer/task budgets on each physical attempt. Scoped
manifest and Run/artifact reads connect the completed work to the waiting Agent
turn; the Node starter supplies the adapter. See [the guide](run-pipelines.md#whole-pipeline-completion)
and [the decision](adr/20261116200000-run-pipeline-completion-and-agent-continuation.md).
Scoped Linux domain, memory/PostgreSQL, API recovery, contract inventory, SDK and
five package lint checks pass on the internal test node. See
[the qualification evidence](ops/evidence/20261116-run-pipeline-completion/README.md).
The feature remains default-off. No production deployment or activation has been performed.

## Pipeline progress and failed-branch reruns (2026-11-17)

The next default-off preview increment adds a durable observed progress journal,
resumable CLI/SDK watches and app-scoped Agent progress reads. Explicit reruns
create a new workflow, preserve the parent and reuse intact healthy outputs;
failed branches and their affected descendants receive new physical attempts.
Previously started affected steps require explicit repeat acknowledgements.
New attempts retain task, artifact, customer budget and cancellation fences.
See [the guide](run-pipelines.md#live-progress) and
[the decision](adr/20261117090000-run-pipeline-progress-and-reruns.md).
[Scoped qualification](ops/evidence/20261117-run-pipeline-progress/README.md) passed
Linux memory/PostgreSQL, API recovery, CLI, contract inventory, SDK and seven lint
groups. No deployment or activation has been performed.

## Durable artifact delivery (2026-11-17)

The next increment promotes a selected successful, contract-valid output into an
existing app-owned upload route. Explicit delivery keys deduplicate storage writes
independently of execution. The existing object journal, accounting and exact
provider recovery own pending outcomes. Destination-authorized receipts and
short-lived download capabilities remain usable after source TTL or Agent thread
revocation. See [the guide](run-artifact-delivery.md) and
[the decision](adr/20261117150000-run-artifact-delivery.md). This increment changes
no VM lifecycle and remains behind existing preview writers; qualification and
deployment are separate from implementation.

## System tools and task SDK (2026-11-17)

The implemented preview slice adds exact Wolfi system package recipes, packages-only
environments and a worker lane that N-1 builders cannot claim. TypeScript and
Python task handles compose immutable input staging, explicit invocation identity,
result decoding, observation, cancellation and durable artifact delivery. The
[PDF report example](../examples/gregale-run/pdf-report/README.md) demonstrates the
customer contract. See [the decision](adr/20261117183000-run-system-packages-and-task-sdk.md).
The [verification record](ops/evidence/20261117-run-system-tools/README.md) separates
completed contract/SDK checks from pending real-VM qualification. Private registry
authentication remains a later increment. Writers stay gated.

## Task authoring and CI (2026-11-18)

The CLI validates draft manifests and JSON fixtures offline, generates
version-pinned TypeScript and Python bindings, and checks committed generated
files for drift. An authenticated smoke test submits the draft as a Source Run
using its declared environment and limits. CI can check the result contract,
declared artifacts and fixture-specific output before publishing the version.
See the [authoring guide](run-tasks.md#author-and-check-a-draft) and the
[PDF report CI example](../examples/gregale-run/pdf-report/README.md).
