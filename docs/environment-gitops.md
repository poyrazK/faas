# Environment GitOps preview

Environment GitOps is an implementation preview under [ADR-568](adr/568-environment-gitops-contract.md). It adds an explicit Git source and field ownership contract to registered project environments. Complete environment enforcement and workload activation are still under development.

## Current scope

| Area | Implemented | Remaining before complete environment enforcement |
| --- | --- | --- |
| Git source | Immutable definition review, approval, polling and separate source freshness; PostgreSQL serving-graph outage and reviewed rollback journey; reviewed workload retirement retaining original identities and accepted work | Dedicated native outage/rollback acceptance for live workloads |
| Ownership and adoption | Reviewed adoption plans, stable resource identities, stale-plan rejection, scoped ownership guards and same-repository GitHub build provenance; GitLab.com OAuth source identity, protected-branch review evidence and project/commit-bound source builds | Dedicated native GitLab workload acceptance; incomplete or conflicting build provenance |
| Drift reporting | Opt-in continuous reports against the last approved definition, durable runs and restart recovery; PostgreSQL apid acceptance for an API/worker/queue graph through a source outage and controller restart | Live report-mode qualification on a deployed control plane |
| Workload preparation | Held image, pinned source, function and job candidates; frozen identity and reviewed contract for each active owned queue binding; atomic private workload reservations; same-repository inherited GitHub source, Dockerfile and function intent; private HTTP/HTTPS service graphs and standalone HTTP graphs | Dedicated Linux acceptance with configured node-local service proxies; unsupported or unverified source-provider provenance |
| Qualification, activation and serving | Immutable attempt-bound guest API-env acknowledgement, restored framework-ready, capture, restore and smoke receipts; HTTP candidates use explicitly owned `runtime.healthz` and `runtime.port`; push and pull workers plus HTTP functions can use bounded reviewed `queue_smoke` payloads; jobs use reviewed argv, timeout, variables and scoped secret references with version-fresh runtime receipts and a sanitized exit receipt; request/service routes and named push-worker, pull-worker and HTTP-function consumers have separate serving receipts; request/service, queue-backed workers and scheduled Jobs can pin request/service targets inside the reviewed graph and release; image and reviewed source/Dockerfile schedules get a durable paused Gregale Job binding and post-activation scheduled-run proof | Dedicated Linux acceptance; Job queue bindings remain unsupported |
| Native execution | Fenced journals, persistent disk staging and publication intent, original-process API control, exclusive four-object capture, durable writer receipts, private Manager restore orchestration, version-checked load, verified anonymous restore inputs, private load/resume/entropy-hook and process-bound restored platform channels; restore-target config/runtime receipts and receipt-gated route selection in both stores; native-retired and native-effects-absent proofs; schedd's periodic, node-bound abandoned-execution recovery; smoke-authorized conditional GCS retirement with durable host tombstones and an opt-in vmmd retry loop | Dedicated Linux capture/restore acceptance; production qualification polling remains gated until those pass |
| Enforcement | Transactional ownership, override and execution fences; lease-bound activation and serving of supported request/service graphs including pinned scoped service bindings, push and pull workers, queue-bound HTTP functions, and scheduled Jobs with a successful post-activation occurrence | Production qualification polling remains gated; dedicated Linux acceptance and unsupported production execution adapters |

Approved Git content, observed intent, qualification, activation and serving state are separate facts. A reviewed or adopted definition does not mean that a deployment is ready or that the environment is serving that revision. Unqualified owned source, runtime, secret-reference, service-binding and queue-binding fields prevent the applied revision from advancing.

An immutable `source.kind: image` may set `source.full_rootfs: true` when the image is not layered on Gregale's runtime base. The choice is part of the reviewed source contract and is frozen with the candidate deployment; other source kinds reject it. Omitting the field keeps the existing dispatch policy, including inherited settings on adopted workloads.
For an adopted workload with live deployments, a conflicting inherited full-rootfs setting blocks preparation until the live deployment policy and reviewed source agree.

The PostgreSQL journey serves an API and queue worker, observes a Git source outage without losing that serving evidence, applies a reviewed queue-contract change, then rolls back to the earlier approved revision. The rollback must obtain fresh preparation, qualification, activation, route and queue-serving evidence; an accepted pending invocation keeps its binding identity throughout. These receipts are submitted through state APIs, so dedicated native guest and live-adapter acceptance remains required. Removing the worker requires reviewed retained workload retirement under ADR-1331.

The opt-in apid reporter has a separate PostgreSQL journey for API variables, routes and policies alongside a worker queue binding. It retains durable drift reports across a Git source outage and controller restart while leaving console values and the queue contract unchanged. This validates report mode, not live workload activation or enforcement.

## Wait for a merged commit to serve

After normal review, approval and merge, CI can wait for recorded serving proof
for that exact merged commit:

    gregale projects environments gitops wait shop production \
      --commit "$MERGED_SHA" --timeout 15m --json

The commit must be a full lowercase 40-character SHA from the environment's
bound repository. Use the merged commit, which can differ from the proposal head
after a squash or merge commit. A project_environments:read credential suffices.
This command only reads status; normal controllers and approval policy must
perform the rollout separately.

Success requires the requested commit's current approved graph, exact source,
environment, generation, intent and definition identities, complete artifact
preparation, qualification, activation and whole-graph serving, complete member
progress and fresh verified source checks. An old serving release, a polled
branch head, a completed build or historical applied revision cannot pass.
Scheduled Jobs require the successful current post-activation occurrence.
Missing or contradictory evidence remains unproven. Older APIs without the
current graph's commit identity cannot satisfy the wait; upgrade both API and CLI.

The wait pins the initial source binding. Once it observes the requested graph,
it also pins that approved revision and generation. Binding or approval changes
end as superseded. Before observing the target graph, a different commit remains
waiting; the command does not infer commit ancestry or whether the target
previously deployed. Normal intent changes can progress only with fresh evidence
for the same approval.

Text mode prints changed progress and workload actions to stderr, with a final
receipt on stdout. Explicit --json and global JSON emit one final JSON receipt
without progress text for rollout outcomes. Request, validation and output
errors use the normal CLI error channel. The overall deadline defaults to 15m and is bounded at
24h. --poll-interval defaults to 2s and accepts 1s–1m; each read is bounded by 30s
and the remaining deadline. Transient transport, rate-limit and server failures
retry within that budget. Permanent request/authentication/scope errors stop.

| Exit | Outcome |
| --- | --- |
| 0 | Requested commit has current serving proof |
| 1 | Invalid request, permanent API error or output error |
| 2 | Current rollout failed, or reconciliation is inactive/report-only |
| 3 | Deadline expired without success |
| 4 | Source binding or observed target approval was superseded |
| 130 | Interrupted; server work is unaffected |

Timeout receipts describe the last observation, not a fresh readiness guarantee.
Inspect current blockers with gitops explain and rerun with the same explicit
commit if appropriate. Neither waiting nor its exit code grants deployment
authority or enables production gates. Exclusive native VM/leak acceptance
remains pending. See [ADR-20261017190812](adr/20261017190812-gitops-wait-for-serving-commit.md) and the
[post-merge CI examples](../examples/gitops-ci/README.md#wait-after-merge).

## Try review and reporting

Use `gregale projects environments gitops explain shop production` for a
read-only explanation of the current recorded state, or add `--json` for
automation. The existing status API includes the same assessment under
`explanation`, and the GitOps dashboard shows its workload progress and actions.
Observed Git commits, approved revision IDs, historical applied revision IDs,
and currently proven serving revision IDs are shown separately. They are not
interchangeable: an applied ID alone never establishes current serving.

Each current graph member reports artifact/build preparation, qualification,
active release membership and serving proof. Stages are `complete`, `pending`,
`failed` or `unknown`. A recorded successful build does not satisfy native smoke
or a scheduled occurrence. Individual serving remains unknown until the exact
whole graph has current convergence evidence; this does not assert that every
individual workload has stopped. Missing candidate rows or missing current
graphs stay unknown rather than becoming ready. Stale generation, revision,
environment or intent evidence is excluded.

Source freshness requires recent polling and verification timestamps; absent
or future timestamps remain unknown. A provider outage can coexist with valid
serving proof, which is reported as degraded. Report mode explicitly records
drift without applying it. Blockers name a responsible customer, controller or
operator and a safe next action. Unrecognized upstream error text is redacted.
Reading an explanation returns exit status zero even when progress is blocked;
automation must inspect the diagnostic status. This command neither retries
work nor approves, adopts, activates, rolls back or enables production gates.

Use an existing GitHub-linked project and registered environment. Start in `report` mode with manual approval and pruning disabled:

```sh
gregale projects environments gitops bind shop staging \
  --manifest-path environments/staging.yaml --mode report --approval-policy manual
gregale projects environments gitops review shop staging \
  --commit "$REVIEWED_COMMIT" > revision-review.json
```

For a GitLab.com source, an account owner first connects GitLab on the
dashboard account page. The control plane needs a GitLab OAuth application
with `read_api` and `read_repository` access. Set
`FAAS_GITLAB_OAUTH_CLIENT_ID`, `FAAS_GITLAB_OAUTH_CLIENT_SECRET`, and
`FAAS_GITLAB_OAUTH_REDIRECT_URI` on apid; register the exact HTTPS redirect
URI ending in `/v1/gitlab/callback` in GitLab.com. The account connection is
sealed at rest and can be disconnected on the account page. A GitLab source
may be bound to a Gregale project without a GitHub installation:

```sh
gregale projects environments gitops bind shop staging \
  --provider gitlab --repository group/subgroup/service \
  --ref refs/heads/main --manifest-path environments/staging.yaml \
  --mode report --approval-policy manual
gregale projects environments gitops review shop staging \
  --commit "$REVIEWED_COMMIT" > revision-review.json
```

GitLab.com supports manual approval or `approval_policy: protected_branch`.
The automatic profile requires GitLab Premium/Ultimate policy API access, one
project-wide approval rule, approvals reset on push, and author/committer
approvals and per-MR overrides disabled. Every matching branch rule must deny
direct pushes and force pushes. Missing or unsupported evidence blocks automatic
approval; manual report mode remains available. Review evidence binds the latest
MR diff and its approving humans to the exact merged definition. See
[ADR-20261017190801](adr/20261017190801-gitlab-reviewed-gitops.md) for the supported profile.

Source/Dockerfile/function candidates use the approved GitLab commit and the
same archive checks as GitHub. Their provenance also includes the immutable
numeric project ID and canonical path; another provider or project cannot be
adopted through a matching repository name. Uploader-supplied source URLs do
not establish trust: unverified tarball baselines remain blocked. Existing
image workloads can be adopted and then rebuilt from the reviewed GitLab source.
Production qualification and enforcement remain gated on exclusive native acceptance.

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

GitOps status includes `workload_evidence` only when a candidate graph matches
the current approved revision, generation, environment intent version and plan.
It reports the graph phase, capture/restore/configuration/smoke/readiness counts,
and remaining blockers. A stale graph is omitted. Qualification, activation,
and serving are reported separately. `activated` is true only when the current
active release set selects the exact reviewed candidate or retained deployment
for every graph member, and each selected deployment is live and unheld. This
read-only check does not release a candidate or route traffic to it. A separate
lease-bound activation transaction waits for complete qualification, builds a
whole-project release set, preserves unmanaged members from the active set (or
requires a safe 100% fallback when no set exists), and changes candidate
hold/status and the active release set in one transaction. Candidates enter
that set at zero weighted traffic. A separate serving phase sends candidate
request/service targets to 100% and their live siblings to 0% under the current
GitOps lease. It preserves traffic weights for retained deployments and only
reports them serving when their selected route already receives traffic. It
stores one route generation per app, retries notifications
after restart, and records a receipt after every registered gateway acknowledges
every route. A gateway roster change requires fresh generations and acks.
The PostgreSQL completion guard checks the exact registered gateway names, not
only the acknowledgement count. A stale roster supplied to preparation is
rejected; a registry change before completion leaves the receipt pending until
preparation resets its route generations and obtains fresh acknowledgements.
Database guards require the current lease and exact prepared graph member;
activated candidate artifacts cannot be rewritten. Generic deployment promotion,
direct live-status changes, rollback,
traffic rebalancing, minimum-instance edits, and source-metadata edits reject
GitOps-managed workloads after activation. Generic release-set publication
must preserve each active GitOps member, and generic deactivation or environment
promotion cannot remove that graph; only qualified graph activation can replace
its members. A graph that is already selected while its qualification is
incomplete reports `environment_activation_before_qualification` as a safety
violation. A worker with an enabled push or pull binding and no current
queue-smoke receipt reports `environment_worker_queue_smoke_contract_missing`.
Push probes exercise the frozen trigger route and check the synthetic item
acknowledgement. A pull consumer never listens, so its probe is a lease: Gregale
arms the reviewed message for the exact candidate execution and the candidate's
own consumer leases it from the node-local qualification proxy, at the
`GREGALE_QUEUE_LEASE_URL` Gregale sets in the candidate. The ack is the
acknowledgement, a nack fails the smoke, and the message must be settled within
60 seconds. The Node and Python SDK consumers honor the variable; other pull
clients must use it in place of the public API and send no credential
([ADR 20261022155812](adr/20261022155812-qualification-pull-queue-smoke-leases.md)).
Both run directly against the attempt-bound private candidate; neither creates a
customer invocation or queue row. HTTP functions continue to
use push bindings only. Push and pull workers may activate dark after
qualification; Gregale reports them serving only after a successfully completed
named-queue or generic pull invocation is atomically recorded against the
selected live deployment. The invocation must complete after that active
release was published; an older delivery or acknowledgement cannot establish
serving for a later release. Queue-bound HTTP functions use the same durable
push-consumer acknowledgement, scoped to a reviewed function and
`workload_class: http`; they require both the queue acknowledgement and their
request-route acknowledgements. A gateway route acknowledgement cannot prove
queue consumption. Workers can also carry reviewed service bindings to
request/service members in the same graph and release; their queue smoke and
queue-serving acknowledgement remain required. Worker and Job workloads cannot
be service-binding targets. A
scheduled Job can activate from the reviewed graph, but the graph is not
reported serving until that exact active Job completes a successful scheduled
occurrence after release publication.
For image or reviewed source/Dockerfile Job workloads with reviewed variables and environment-scoped
secret references, but without queue bindings or unsupported runtime overrides, apply now
creates or reconciles a deterministic, paused Gregale `jobs` record and stores
its ID on the scoped workload intent. Database checks require that link to
match the reviewed immutable image, schedule and variables; the Jobs API cannot
edit or delete a linked record. Removing the schedule pauses and retires the
managed Job. Its definition accepts a reviewed
`schedule` for `execution_mode: job`: a five-field cron expression, IANA
timezone (UTC by default), and Gregale's schedule and failure policies. That
contract is frozen and observed. Before activation, Gregale verifies that the
linked Job still matches the reviewed image, schedule and variables, and that
the image materializer has produced the matching immutable artifact. Job
qualification freezes the observed scoped alias map, resolves only those
references to sealed ciphertext and source versions, and waits for guest-init
to acknowledge the exact secret-key set before releasing the held command. A
reference or version change before release invalidates that attempt. The same
transaction that publishes the project release opens that Job. Serving remains
pending until a successful scheduled occurrence for the current schedule revision
starts and completes after the release, and its run snapshots match the active
Job's image artifact, command, resources, retry and timeout policy, and
environment. This also accepts an empty command snapshot when the image
supplies its own entrypoint. An unrelated or pre-activation public `JobRun`
therefore cannot prove GitOps execution.
Job workloads without a schedule remain blocked by
`environment_job_production_adapter_missing` until Gregale links their reviewed
workload to a production Job execution path. GitOps planning also blocks an
enabled Job queue binding with
`environment_job_queue_binding_execution_unsupported`, before it can enable a
consumer without a Job execution adapter or retain an already-enabled Job
consumer. A reviewed disable can retire the existing consumer, and a disabled
binding may be reserved; neither qualifies or activates the Job.
Qualification still uses a separate isolated `job_smoke`. Jobs report
`environment_job_execution_smoke_contract_missing` when no reviewed `job_smoke`
is present. A reviewed job contract now runs through a private one-shot VM path
with its exact argv and 1–300 second timeout. Gregale records only a sanitized
passing exit receipt after native retirement; it creates no customer queue
item, `JobRun`, task, or output manifest. Until that receipt exists, status
reports `environment_job_execution_smoke_evidence_missing`. Jobs with queue
bindings report `environment_job_queue_binding_execution_unsupported`.
Scheduled Job callers may now bind to request/service members in the same
reviewed graph. Qualification executes dependencies first, boots the Job held,
publishes its attempt-bound runtime identity, records the guest's attempt-bound
configuration acknowledgement, validates each private route while the command
remains held, then releases it. The sanitized smoke receipt is tied to the same
request, attempt, graph, instance and policy, and storage rejects it unless the
matching configuration receipt exists after native retirement. Activation and
serving compare the exact frozen binding map with the linked Job intent and
post-release occurrence. Reviewed non-secret Job variables are frozen in the
workload intent, private qualification runtime, linked Job, activation graph and
scheduled-run proof. Scheduled Jobs also freeze the alias-to-secret map from the
reviewed definition and require it to match the current environment references
before creating a candidate. Qualification seals only those references, checks
their source versions before releasing the held command, and requires the guest
to acknowledge the exact secret-key set. Queue bindings remain unsupported;
service-bound Jobs may target only request/service
workloads in the same graph and release. Dedicated Linux acceptance still gates
production qualification polling and enforcement.

Source/Dockerfile schedules use the same exact-commit archive verification and
held builder candidates as services for both GitHub and GitLab. Source-backed schedules require an explicit reviewed `runtime.entrypoint` argv
(for example, `["/usr/local/bin/node", "/app/report.js"]`), separate from
`job_smoke.command`. Each Job remains paused through build and qualification. Participating apps
retain the existing release-set requirement to opt into revision retention
(`revision_pin_ttl_seconds`) before activation. Release activation binds its exact
qualified deployment layer; occurrences reuse that layer without fetching source
or rebuilding. Internal `gregale-source://` and `gregale-deployment:` identities
are source/deployment references, not OCI images or content digests. OCI
materializer APIs cannot publish them. Schedd verifies frozen source provenance,
layer identity and the active release before boot and before releasing the held
command. A changed artifact increments the schedule revision. Serving still
requires a matching successful occurrence after activation. See
[ADR-20261017190802](adr/20261017190802-gitops-source-scheduled-jobs.md).

At scheduled execution, schedd requires the VM client's explicit held-start
release capability before booting a managed Job. It persists the Job's runtime
network identity and rechecks captured variables, secret references and secret
versions before releasing the guest command; an older client fails the attempt
before VM boot instead of leaving a held guest stranded.

An operator must opt apid into continuous reports with `FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED=true`. Reporting is disabled by default and never claims enforcement work or performs workload activation. Git source polling defaults on; `FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED=false` disables it. A source outage retains the last approved definition and is reported separately from drift.

Finishing a report preserves a pending wakeup when intent changed after its final
observation. The next worker poll can claim that source immediately, including
after an observation failure. Changes included in the final observation keep the
normal reporting cadence. A plan using a temporary override schedules its next
check no later than that override's expiry; an override that expires during the
run triggers a fresh check at the next poll. Completed reports remain historical
observations, and do not certify changes made after their observed intent version.

## Preview a pull or merge request

Preview a candidate before approving it, including on a binding that has no
approved revision yet:

```sh
gregale projects environments gitops plan shop staging \
  --commit "$CANDIDATE_SHA" --format markdown --fail-on-blocked
```

Gregale verifies the commit through the bound provider and plans against current
scoped intent and ownership. It returns the commit, digest, source generation,
observed intent version and plan hash. Before/after values are omitted. It
creates no revision, ownership claim, workload reservation or run. Its candidate
hash cannot authorize approval or adoption. Refresh after source or environment
changes; a passing check does not prove qualification or serving readiness.

JSON is the default. `--fail-on-blocked` prints the result before exit 2 for
ownership conflicts and unsupported operations. Exit 1 means the request or
response could not be validated. See the
[GitHub Actions and GitLab CI examples](../examples/gitops-ci/README.md) for
summaries, artifacts and read-only `project_environments:read` credentials.

Before adoption, unmanaged edits can change the plan hash while the managed
intent counter remains zero. The hash includes the actual observed fields;
candidate plans always require a fresh request and grant no write authority.

## Prepare a promotion proposal

Preview an immutable image promotion from a currently proven serving release:

```sh
gregale projects environments gitops promotion-plan shop production \
  --from staging --target-commit "$TARGET_COMMIT" --fail-on-blocked \
  > promotion-plan.json
```

`TARGET_COMMIT` identifies the commit containing the production manifest. The
planner verifies that manifest through the production binding and reads both
environments on one database snapshot. The output identifies the approved source
commit, graph, active release and selected deployments. Export requires current
source verification and matching qualification, activation and serving evidence.
Both environments must belong to the same project and verified provider
repository. Missing or incompatible artifact evidence is an explicit blocker.

Immutable image sources and retained signed source-build artifacts can be proposed. Production
variables, secret references, runtime settings, routes, policies, schedules,
queue/service bindings, app mappings and additional workloads are preserved.
The proposal and target field plan omit configuration values and secret-reference
targets. Source, Dockerfile and function promotion requires explicit matching
`app` mappings and the same source recipe/runner. The proposal adds
`build_artifact_id` and `build_artifact_hash`, selecting the existing signed
application-layer producer. The target manifest commit is distinct from the
original code commit shown in the artifact evidence.

On normal Git approval, the reference pins the retained layer before preparation.
Preparation rechecks current publisher trust and base lineage, then creates a
held target candidate with the same code bytes and no new build. Configuration,
secret aliases and qualification belong to the target. Revoked publishers,
missing/deleting artifacts, changed recipes and cross-app reuse are rejected.
Source and Dockerfile scheduled Jobs support these references with explicit matching
app mappings and schedules on both origin and target. Target cron/timezone,
production argv, smoke command and configuration remain unchanged. Sidecars and
function schedules remain unsupported. See [ADR-20261017190804](adr/20261017190804-gitops-built-artifact-reuse.md)
and [ADR-20261017190808](adr/20261017190808-gitops-scheduled-job-artifact-promotion-and-rollback.md).

For example, a reviewed production source can pin the producer returned by the
promotion plan (use the actual ID/hash, not these placeholders):

```yaml
workloads:
  api:
    app: shop-api
    source:
      kind: source
      directory: apps/api
      build_artifact_id: <producer UUID>
      build_artifact_hash: <producer input SHA256>
    variables:
      MODE: production
```

Pins retain code and provenance; they do not copy snapshots or grant serving
permission. Fresh target qualification and activation are still required. Full
native VM/leak acceptance remains pending on an exclusive physical x86_64 host.

For automatic reuse across approved commits, opt into an isolated build context:

```yaml
source:
  kind: source
  directory: workspace/apps/api
  reuse_unchanged_build: true
  build_context: workspace
```

The builder receives only `workspace`, with repository-relative paths preserved.
Put all shared libraries and lockfiles inside that context. Configuration in
`environments/` can change without changing the build bytes. Omitting
`build_context` selects the entire repository, including the manifest. Contexts
currently support regular files and directories; links and special files inside
them are rejected. Opting in accepts reuse of the previous dependency resolution
for unchanged inputs; a hit does not prove network dependency freshness.

Reuse requires an exact context and recipe match, the current builder/base, and
an approved signed export from the same mapped app. Missing, revoked, corrupt or
stale evidence causes a normal build. Each commit still gets its own held
candidate, current provenance and signature, target conversion and fresh
qualification. `gitops explain` reports the recorded cache decision; it does not
turn a cache hit into serving proof. This optimization uses the node-local cache.
An explicit artifact pin takes precedence. See [ADR-20261017190809](adr/20261017190809-gitops-unchanged-build-context-reuse.md).

Review the plan and reuse its `plan_hash` in a fresh request before exporting:

```sh
gregale projects environments gitops promotion-plan shop production \
  --from staging --target-commit "$TARGET_COMMIT" \
  --expected-plan-hash "$REVIEWED_PLAN_HASH" --format manifest \
  --manifest environments/production.yaml > proposed-production.json
```

Manifest export checks the complete local base digest before changing any source
contract, then verifies the proposed digest. It emits canonical JSON, which is
also valid YAML; formatting and comments are normalized. The command writes only
to stdout. Review and commit the resulting manifest through the usual Git flow.
`--format patch` exports an RFC 6902 proposal envelope instead. Apply that
envelope's operations only after checking its `base_definition_digest` against
the parsed target manifest. Both export formats require the reviewed hash and
refuse blocked proposals. JSON/Markdown previews can use `--fail-on-blocked` to
print the assessment and exit 2; invalid or stale observations exit 1.

Release, artifact, binding, ownership or target intent changes invalidate a saved
observation. Neither a proposal nor its hash authorizes deployment. Target review
and fresh environment-specific qualification remain required; production
enforcement and dedicated native acceptance retain their existing gates. See
[ADR-20261017190803](adr/20261017190803-gitops-promotion-proposals.md).

### Open a promotion pull or merge request

After reviewing a `promotion-plan`, use its hash and the current target branch
commit to preview a draft proposal:

```sh
gregale projects environments gitops promotion-pr shop production \
  --from staging --target-commit "$TARGET_COMMIT" \
  --expected-plan-hash "$REVIEWED_PLAN_HASH"
```

Add `--yes` to create the draft PR/MR and `--json` for its portable receipt:

```sh
gregale projects environments gitops promotion-pr shop production \
  --from staging --target-commit "$TARGET_COMMIT" \
  --expected-plan-hash "$REVIEWED_PLAN_HASH" --yes --json
```

Authenticate `gh` for GitHub.com or `glab` for GitLab.com first, using an account
allowed to create repository branches and pull/merge requests. Gregale's platform
credential needs only the existing GitOps read scope. The repository, manifest
path and base branch come from the verified target binding. Tag bindings are
unsupported; the target commit must still be that branch's exact head.

The command checks a fresh plan and target binding before writing. It commits
only the reviewed workload source replacements, preserves target configuration,
and includes origin release, selected artifact provenance and qualification
requirements in the draft description. YAML comments outside replaced source
nodes are retained; YAML formatting can normalize. JSON output remains canonical
JSON. Neither the local working tree nor its Git index is used.

Retries use a branch derived from the complete plan hash. A matching open draft
returns its existing URL; a valid branch left by an interrupted attempt can
finish opening the draft. Closed, merged, edited, ambiguous or foreign requests,
unexpected files/ancestry and changed proposal branches are rejected. The command
never overwrites a branch or automatically retries an uncertain write. If a
request times out, retry the exact command. If the target base or reviewed plan
changed, request and review a new plan. A proposal branch or draft can remain
after a race; it is not deleted automatically.

Creating the draft does not merge, approve or deploy it, pin artifacts, or extend
retention. Normal Git review/approval, current artifact trust and fresh target
qualification, activation and serving evidence remain required. Use `rollback-pr`
below for retained-release recovery with its own reviewed rollback plan.
Native acceptance and production execution gates remain unchanged. See
[ADR-20261017190810](adr/20261017190810-gitops-promotion-review-requests.md).

## Prepare a reviewed code rollback

Optionally protect recovery artifacts through reviewed Git intent:

```yaml
recovery_retention:
  releases: 3
  seconds: 86400
  max_bytes: 536870912
```

The policy takes effect at approval. It protects the newest completed serving
releases, including the active served release, within all three bounds. Limits
are five releases, seven days and 1 GiB per source; account byte reservations
across sources are capped at 2 GiB. Main and sidecar keys are deduplicated within
each release and conservatively charged again across releases. A budget that
cannot fit the newest release protects no release; older smaller releases do not
skip ahead. Protected storage remains part of ordinary storage accounting.

The catalog reports the effective approved policy and total charged bytes across
all pages. Each row shows artifact protection separately from rollback eligibility
and routing compatibility. The newest served release has no deadline while it
is current. Once a newer release first completes serving, the replaced release
stays protected for `seconds` from that moment, so a long-running release is
still recoverable right after a bad deploy. Listing, retrying or gateway handoffs
never renew a deadline. Count and byte bounds, policy reduction/removal, or
explicit source detachment can release protection earlier. Suspension preserves it. Existing trust checks and fresh target
qualification remain mandatory. Approval cannot resurrect an artifact claimed
by garbage collection, and protected provenance cannot be erased by cleanup.
See [ADR-20261017190807](adr/20261017190807-gitops-bounded-recovery-retention.md).

Find retained releases and current eligibility for your target manifest:

```sh
gregale projects environments gitops releases shop production \
  --target-commit "$TARGET_COMMIT"
```

Add `--json` for automation, or `--before UUID` using `next_before` for older
history. A successful listing exits zero even when releases are blocked; automation
must inspect each release's `eligibility` and `blocking_reasons`. The GitOps
dashboard offers the same explicit recovery check. Each page
verifies the target commit once and assesses all releases on one database snapshot
using the rollback planner. Only completed serving history of the current binding
is listed; expired release compatibility windows remain visible. The release
window is a routing deadline, not an artifact retention guarantee. Listing grants
no pins or execution authority. Blockers explain current artifact trust, mapping,
source freshness and ownership failures. Request a fresh individual plan before
reviewing/exporting. See [ADR-20261017190806](adr/20261017190806-gitops-recovery-catalog.md).

Select a release previously served by this Git source in this environment. The read-only plan identifies its manifest commit, graph, exact
image/build artifact and historical serving time:

```sh
gregale projects environments gitops rollback-plan shop production \
  --to-release "$RELEASE_ID" --target-commit "$TARGET_COMMIT" --fail-on-blocked
```

Review the result, then request a fresh observation with its `plan_hash` before
exporting a patch or manifest:

```sh
gregale projects environments gitops rollback-plan shop production \
  --to-release "$RELEASE_ID" --target-commit "$TARGET_COMMIT" \
  --expected-plan-hash "$REVIEWED_PLAN_HASH" --format patch > rollback-plan.json
```

`TARGET_COMMIT` contains the current target manifest, not the old code commit.
`--format manifest --manifest environments/production.yaml` validates the complete
local base digest and writes canonical JSON to stdout. Patch output carries the
base digest and source test/replace operations; verify the full base digest
before applying it. JSON and Markdown previews can report blockers; exports refuse
a blocked or changed plan. `--fail-on-blocked` exits 2 for a printed blocked plan.

Only compatible workload sources change. Current configuration, variables,
secret references, routes, policy, queue/service bindings, schedules and additional
workloads are preserved. Removed workloads and changed app mappings block export.
Source/Dockerfile/function reuse requires compatible recipes and explicit matching
app mappings. Missing, revoked or subsequently replaced artifacts block recovery. Preview does not pin
artifacts or guarantee indefinite retention. Normal Git approval retains selected
build artifacts under the existing GC fence before preparation.

Historical serving receipts are selection evidence, not current readiness.
Rollback requires normal Git review and fresh qualification/activation/serving;
it does not restore data or undo database migrations. Scheduled Jobs can select
retained signed source/Dockerfile artifacts for future runs, preserving the target
schedule, command and configuration. Every promoted or recovered release needs
fresh Job qualification and a successful post-activation occurrence; completed
executions are never replayed or reused as proof. Schedd rechecks current publisher
trust before boot and before releasing the held command. Unscheduled Jobs and
sidecars remain excluded. Native VM/leak acceptance and production gates remain
unchanged. See [ADR-20261017190805](adr/20261017190805-gitops-reviewed-rollback-proposals.md) and
[ADR-20261017190808](adr/20261017190808-gitops-scheduled-job-artifact-promotion-and-rollback.md).

### Open a recovery pull or merge request

Choose an eligible release from `gitops releases`, then review its fresh
`rollback-plan`. Preview the recovery draft with that plan's hash:

```sh
gregale projects environments gitops rollback-pr shop production \
  --to-release "$RELEASE_ID" --target-commit "$TARGET_COMMIT" \
  --expected-plan-hash "$REVIEWED_ROLLBACK_HASH"
```

Add `--yes` to create the draft PR/MR, and `--json` for a receipt that includes
the selected `release_set_id`:

```sh
gregale projects environments gitops rollback-pr shop production \
  --to-release "$RELEASE_ID" --target-commit "$TARGET_COMMIT" \
  --expected-plan-hash "$REVIEWED_ROLLBACK_HASH" --yes --json
```

The release UUID is required; the command never chooses a release automatically.
Use an authenticated `gh` or `glab` session as described for `promotion-pr`.
Repository, manifest and branch come from the active target binding, and the
target commit must still be the branch's exact head. Promotion hashes cannot
authorize a recovery proposal. A fresh rollback observation checks the selected
historical release, current release, artifact eligibility and current target
intent before each write. A changed observation requires a fresh reviewed plan.

The draft identifies the historical serving time/graph, code provenance and
current target base. It changes only compatible workload source contracts while
preserving current configuration, secret references, routes, policies, schedules,
commands and bindings. Normal Git review/approval, current artifact trust and
fresh target qualification, activation and serving evidence remain required.
Creating the draft does not pin artifacts or extend their retention.

Retries use `gregale-rollback-<plan hash>` and recover a matching open request or
valid branch left by an interrupted attempt. Closed/merged, edited, foreign or
ambiguous requests and changed branches are rejected. No branch is overwritten,
request reopened or uncertain write retried automatically. A race can leave a
branch or draft; inspect it and retry the same command only while its reviewed
plan and base remain current.

Recovery does not restore data or undo database migrations. Completed Jobs are
never replayed; scheduled Jobs require fresh qualification and a successful
post-activation occurrence for future runs. Production execution gates and
exclusive native acceptance requirements remain unchanged. See
[ADR-20261017190811](adr/20261017190811-gitops-rollback-review-requests.md).

## Receive drift, source and rollout notifications

Subscribe an account receiver explicitly to `environment.gitops.changed`:

```sh
printf '%s' "$WEBHOOK_SECRET" | gregale webhooks account add \
  --target-url https://alerts.example.test/gregale \
  --event environment.gitops.changed --from-stdin
```

The `drift` axis reports `in_sync`, `drifted`, `blocked` or `unavailable` for
current observations. The independent `source` axis reports `verified`,
`blocked` or `unavailable`; an outage preserves the last approved definition.
An initial healthy result establishes a silent baseline. Failures and later
recoveries notify; unchanged status stays quiet. Stale reports cannot change
the current baseline. New receivers get future transitions; inspect GitOps
status for the current report. Drift reporting retains its operator opt-in; source
notifications follow completed polls. Existing fleet stale-poll alerts remain
separate from confirmed access loss.

Signed payloads include a transition UUID, approved revision/commit when known,
generation, observation version, field paths/actions, stable error/blocker codes,
and a dashboard link. They omit values, secret-reference targets and credentials.
Field and blocker details are limited to 100 each; `details_truncated` identifies
shortened payloads. Baselines and all receiver deliveries commit together and
survive restart and report pruning. Existing retries, receiver controls and
account quotas apply. Native qualification and enforcement keep their gates.

The independent `rollout` axis reports whole-environment outcomes:

| Status | Meaning |
| --- | --- |
| `serving` | The exact approved commit has current complete preparation, qualification, activation and serving evidence for every workload, plus fresh successful source verification. |
| `failed` | A current graph or workload has recorded failure for that exact approved revision. |
| `superseded` | A pending rollout was replaced by another approval or stopped by a source control change. The payload identifies the previous rollout. |

The controller assesses rollout outcomes when a current reconciliation report
finishes. It uses the same evidence rules as `gitops wait`; a completed build or
historical applied revision is insufficient, and scheduled Jobs need the current
post-activation occurrence. Stale reports cannot notify. Report mode does not
start rollouts. Pending progress stays quiet. Each failure and serving outcome
is emitted at most once per approval generation, surviving controller restart
and missing or flapping evidence. A failed rollout can later notify serving
recovery. A completed outcome is not subsequently reported as superseded.
This is rollout completion feedback, not continuous availability monitoring or
a guarantee that every transient failure was observed. The shared assessor
currently supports full 40-character Git SHAs; other Git identities stay
unassessed.

Rollout payloads add `definition_digest`, `graph_id` and `plan_hash` when known.
Failures include up to 100 mapped `diagnostics` with stable codes, an affected
workload where known, and operator guidance. Unknown errors use
`unrecognized_blocker`; upstream messages and build logs never enter these
details. Pending supersession preserves the previous revision's last observed
generation and intent, rather than borrowing the new revision's identity.
Approval/control changes, the notification baseline and all receiver deliveries
commit atomically. No migration or separate notification worker is required.

Deliveries are signed and **at least once**. Deduplicate retries using the
payload's `transition_id`; all receivers for one outcome share that ID. New
receivers receive future outcomes without replaying already recorded ones.
See [ADR-20261017190813](adr/20261017190813-gitops-rollout-outcome-notifications.md).

## Isolated enforcement acceptance

The real apid enforcement worker can run only in an isolated development
acceptance process with all three settings set to `1`: `FAAS_DEV`,
`GREGALE_GITOPS_ACCEPTANCE`, and
`FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT`. The production control-plane
role rejects the dev-only enforcement setting at boot. Customer enforcement
remains closed by default.

Against a disposable PostgreSQL cluster, run the subprocess journey with:

```sh
GREGALE_GITOPS_ACCEPTANCE=1 DATABASE_URL="$DISPOSABLE_DATABASE_URL" \
  FAAS_PGTEST_TEMPLATE_DATABASE=1 go test ./cmd/e2e \
  -run '^TestEnvironmentGitOpsAcceptanceE2E$' -count=1
```

This journey covers HTTP console writes and ownership rejection, approved
intent in PostgreSQL, the real apid worker, repair, and durable partial status.
It intentionally has no serving candidate. Dedicated native x86_64 KVM
capture, restore, guest, gateway and rollback acceptance is still required
before production qualification polling or enforcement can be enabled.

The opt-in native graph journey includes an image API and a push worker
with a reviewed queue binding for each of GitHub and GitLab. It requires an
isolated designated x86_64 KVM
host, a disposable PostgreSQL database, a qualified kernel, guest-init built
from the same checkout, and registry access to three pinned base images. Run
`make test-environment-gitops-native` with `DATABASE_URL`, `FAAS_TEST_KERNEL`,
`FAAS_GUEST_INIT`, `GREGALE_GITOPS_NATIVE_HOST_CLASS=native`, and these explicit
`repository@sha256:...` image references:

- `GREGALE_GITOPS_NATIVE_BUILDER_REF`: the real builder image with Railpack and BuildKit.
- `GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF`: the matching Node source runtime base.
- `GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF`: the minimal runtime base for the scratch Dockerfile fixture and full-rootfs image workloads.

Imaged stages these through its verified base-producer path in private test
storage. No supplied ext4 is imported as image provenance. Each checkout and
temporary volume needs at least 32 GiB free for base conversion, builder drives,
exports and compilation. The guarded runner requires a clean
checkout, refuses active Gregale services, checks leaks before and after, and
builds fresh daemon/fixture binaries rather than reusing a caller's prebuilt
directory. It
requires both named metal tests, both providers and every required scenario
to pass rather than skip. Set the host class to
`nested-diagnostic` only for troubleshooting; that result cannot qualify a
production gate. A passing journey must prove API serving through the gateway,
a completed scoped customer queue delivery for each applied generation, and
repair of a console change after its temporary override expires while the
graph is serving. It observes activation before sending the delivery needed to
prove mixed-graph serving; it never waits for an applied revision before
admitting that first customer message.

The resilience cases inject source unavailability through the real githubd RPC
client or GitLab.com HTTPS client, restart apid during that outage and while an
image pull is held, and
supersede the held revision through HTTP review and approval. A later case
restarts schedd after activation with an accepted customer delivery in flight.
The reviewed rollback must obtain a fresh graph and native qualification at a
new generation, preserve accepted queue identities, and serve another customer
delivery before convergence. Slow gateway requests continue throughout these
transitions; a failed request or a response from the superseded image fails the
journey. The guarded runner allows up to 300 minutes for the complete provider
and source-kind matrix and retains its pre-run and
post-run leak checks. These cases still require execution on the exclusive
native host; no passing dedicated native result has been established.

### Native source-backed scheduled Jobs

The same guarded runner additionally requires
`TestEnvironmentGitOpsSourceJobsNativeMetal` for GitHub and GitLab, each with
`source` and `dockerfile` workloads. These use the real builder microVM,
imaged, native Job qualification, release activation, scheduler cron admission,
guest command and durable task logs. The real builder base is staged by imaged
from its pinned reference, with the checkout's guest-init. Shared host artifacts
remain unchanged. GitLab's fixture proxy, private CA and OAuth keys apply only
to apid; builder and image downloads keep their normal transport trust.
The source fixture uses Railpack Node; the
Dockerfile fixture copies a checkout-built static customer executable through
BuildKit inside the builder VM. Real source builds may need their normal tool
and runtime downloads on the exclusive acceptance node.

Each case requires a paused schedule during the first build, successful native
smoke qualification and then an actual scheduled occurrence. Guest output must
contain that build's commit and the occurrence's own run ID exactly once;
qualification smoke output cannot substitute for this proof. A second
occurrence reuses the exact deployment layer without adding a build. Restarting
schedd and apid between completed occurrences must preserve the Job identity,
schedule revision, cursor and artifact. Another case restarts apid with a source
build in progress, supersedes it through HTTP review/approval, and rejects any
scheduled run or serving receipt for the superseded commit. Reviewed rollback
requires a fresh candidate, qualification graph and successful occurrence.
These cases do not claim recovery of an in-flight scheduled Job after a
scheduler crash.

This harness has not established a passing exclusive native result. Local
archive/report tests and metal-tagged compilation do not qualify the native
execution or leak gates. Production settings remain unchanged.

### Native qualification evidence

The runner creates a new private directory outside the checkout and always
writes `report.json`, including on a blocked preflight or failed journey. Set
`GREGALE_GITOPS_NATIVE_REPORT_DIR` to a new directory path to choose the location;
an existing run is never overwritten. The report and raw logs have private
permissions. `make test-environment-gitops-report` validates this evidence
contract without a VM or database.

The `gregale.dev/gitops-native-acceptance/v2` report binds the run to a clean
commit, host OS/architecture/kernel and virtualization observation, pinned Go,
Firecracker/jailer and PostgreSQL client versions, and SHA-256/size fingerprints
of the kernel and guest-init, plus all three pinned base-image references.
It rechecks these inputs
after execution and fingerprints the private JSON event stream and diagnostic
logs. The JSON does not contain database URLs, OAuth credentials, fixture paths,
or raw test output.

For both providers it requires structured Go test pass events for baseline
serving, source outage/restart, held-revision supersession, scheduler restart
with accepted work, reviewed rollback, override repair and retirement/recovery.
It also requires every provider/source-kind case for source Job qualification,
artifact reuse, scheduler restart, held-build supersession and rollback.
A missing scenario, skip, failure, malformed/contradictory event stream, nonzero
runner exit, changed inputs, or failed leak check prevents a pass. Human-readable
`PASS` strings are not evidence. The post-run leak observation also runs when an
attempt is interrupted; it never removes host resources to make the gate pass.

`result: passed` and `native_acceptance_passed: true` require an exclusive native
host and the complete matrix. `diagnostic_passed` always leaves native acceptance
false. This is a local operator record, not a signed attestation or production
authorization; no production gate consumes it or changes automatically.

Provider boundaries are local fixtures. GitLab uses the fixed production HTTPS
origin with a daemon-scoped loopback CONNECT proxy and test CA, plus sealed
account OAuth credentials and immutable project/commit archive reads. TLS
verification remains enabled; the proxy cannot dial arbitrary origins. All
journeys use manual approval. Live GitHub/GitLab access and protected-MR
review qualification remain outside this local-fixture coverage. The v2
`coverage` object lists image workloads and both source-backed Job kinds;
`source_scheduled_jobs_passed` can become true only with the complete successful
exclusive native matrix. Older image-only v1 reports cannot qualify this matrix.
The report still grants no production authorization.

The manual [GitOps native CI lane](ops/gitops-native-ci.md) runs this complete
matrix on an exclusive runner at the exact `main` dispatch commit. It verifies
the private v2 record against the raw events and artifact fingerprints before
publishing a sanitized CI summary. The lane still requires an eligible host;
adding the workflow does not establish an executed native acceptance pass.

## Next implementation steps

1. Pass dedicated Linux capture/restore acceptance. The scheduler requests
   cleanup only after the original and restored VM journals prove retirement
   and a persisted smoke receipt is available. vmmd deletes exact receipt-bound
   GCS generations and records durable retirement intent. With the experimental
   publication root configured, startup and a recurring worker retry only those
   already-authorized deletes. The root remains unset in production templates;
   keep production polling gated until native lifecycle acceptance passes.
2. Pass dedicated Linux capture/restore acceptance before enabling production
   qualification polling. Schedd now has a 30-second, node-bound durable graph
   poller behind `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1`; it is off
   by default and must remain off until that acceptance passes. Each poll uses
   a bounded page and durable attempt leases, and scans recover after process
   restarts or superseded cursors. The separate cleanup-only recovery worker
   continues to retire abandoned executions without qualifying candidates.
   The internal graph primitives cover HTTP/HTTPS service graphs, standalone
   HTTP health graphs, push-worker queues, HTTP-function push queues, and
   service-bound scheduled Jobs;
   successful current attempts are not redispatched. Production service-bound
   dispatch uses a transport-specific, node-bound service-proxy resolver. A
   node-local schedd must set `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTP_URL`
   to its private `:10081` listener and, when HTTPS bindings are used,
   `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTPS_URL` to its
   private `:443` listener. The resolver rejects a graph placed on another
   node and rejects a missing transport URL before VM admission. A central
   fleet schedd cannot route to arbitrary tenant bridges. Service-bound
   production dispatch remains gated on dedicated Linux acceptance.
3. Pass dedicated Linux acceptance for service-bound scheduled Jobs. The held
   qualification path now executes dependencies first, records the Job's
   attempt-bound guest configuration acknowledgement, validates pinned private
   routes before releasing the command, and persists smoke evidence only after
   native retirement. Activation and serving bind the same frozen service
   bindings to the managed Job and its post-release occurrence. Acceptance must
   exercise route timeout, target failure, supersession, restart recovery, and
   a changed graph binding before production qualification polling is enabled.
4. Pass dedicated Linux acceptance for request/service route cutover, scheduled
   Job execution, and each production execution adapter. Exercise timeout,
   recovery, gateway-roster change, supersession and rollback before enabling
   continuous enforcement.

## Workload preparation contract

An explicit source can reserve a missing workload under the internal enforce
executor. Reservations are private apps with no deployment or serving VM, use
the original environment UUID and logical name, and commit the complete cohort
with its mappings, scoped intent, ownership and progress journal. Quota or name
collisions roll the cohort back. An existing mapped identity that disappears
requires reviewed recovery; it is never replaced silently. Report mode creates
no resources, and the customer enforcement gate remains closed.

Adoption can recover an existing GitHub build's source, Dockerfile or function
intent when every live deployment records the same immutable repository and
commit from the environment's repository. It accepts Gregale's pinned
`github://owner/repository@commit` form and the pinned
`https://codeload.github.com/owner/repository/tar.gz/commit` URLs written by
GitHub webhook builds. The commit column and source root must agree across all
live deployments. Incomplete, cross-repository, mixed-commit and non-GitHub
provenance remains blocked for review rather than guessed.

Enabled push-worker and HTTP-function bindings require a reviewed `queue_smoke`
input. Gregale sends that JSON body directly to the frozen consumer's
`/_triggers/esm/<trigger-id>` route on the private candidate VM. It requires a
complete 2xx response and rejects a `batchItemFailures` entry for the synthetic
message ID. The bounded response body is inspected only in memory; the receipt
stores digests of the policy and sanitized outcome. A harmless test message is
important because the candidate runs its actual handler. The message never
reaches the customer queue or a live deployment.

```yaml
workloads:
  orders-worker:
    app: orders-worker
    queue_bindings:
      orders:
        queue_name: orders
        mode: push
        workload_class: worker
    queue_smoke:
      orders:
        payload:
          type: qualification-probe
          idempotency_key: gitops-qualification
```

An HTTP function can use the same private push probe with `workload_class: http`
when its reviewed source has `kind: function`. HTTP pull bindings and HTTP
queue bindings on non-function apps remain invalid.

Job qualification accepts a separate reviewed argv contract:

```yaml
workloads:
  daily-report:
    runtime:
      execution_mode: job
    job_smoke:
      command: [node, scripts/smoke.js, --qualification]
      timeout_seconds: 30
```

The command is frozen as argv and is never passed through a shell. The private
one-shot runner executes it against the candidate artifact, waits for the
matching lease's terminal result, and writes a sanitized passing receipt only
after native retirement. It discards guest output and creates no customer
`JobRun`, task, or queue record. The receipt is qualification evidence only;
the candidate remains held until graph activation and serving convergence are
implemented and enabled.

Pull workers use the generic queue invocation contract for private smoke; this
does not make the external pull consumer or its production acknowledgement part
of the qualification receipt. Jobs need isolated process-completion evidence,
which is separate from the shared production job-run path. The native
queue-binding projection reports pull consumers as `external`; qualification
does not synthesize a push trigger for them. The native job boot path requires a run/task identity and
terminal guest exit envelope; GitOps qualification needs a separate identity
and receipt that cannot mutate `job_runs` or `job_tasks`.

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
injects private HTTP or HTTPS binding URLs into guest boot inputs. HTTPS uses
the workload's one-label `.internal` alias, the existing wildcard certificate
and guest trust bundle; DNS only makes the alias discoverable. The guest
listener derives the original caller from a fresh network-slot lookup and
checks both original execution frames, current leases, the reviewed graph,
fresh runtime acknowledgements and frozen target caller policies on every
call. Revocation cancels active requests. It forwards to the exact qualified
target and supplies no ordinary service wake, replica retry or serving
fallback. Ordinary service requests and service TCP reject qualification
callers. Every admitted VM is retired when the graph callback returns,
including failure paths. Before capture or restored smoke, the dispatcher checks
that every scoped binding resolves to the exact live caller and dependency
instance, with the reviewed target port and graph lease. A mismatched route
aborts the attempt without capture or smoke evidence. Restored graph smoke
visitors must return one passing,
per-resource result bound to each exact restored instance, with a policy ID and
SHA-256 digests for policy and sanitized result. Missing, duplicate, failed or
cross-instance reports create no smoke receipts. The state layer derives the
only accepted policy from explicitly owned frozen `runtime.healthz` and
`runtime.port` values and the candidate's immutable app identity. A missing
port remains unsupported, even when an inherited default exists. It hashes a
versioned HTTP GET contract (2xx response, three-second timeout) and rejects
reports that name another policy or digest. Inherited health paths and TCP
startup readiness cannot stand in for this app-level policy. The scheduler now
has source and restored graph visitors that execute this exact contract. The
source visitor must pass before capture but records no qualification evidence.
The restored visitor binds a sanitized result digest to each restored target.
The private HTTP visitor discards response content and caps each response at
1 MiB and 256 body frames; exceeding either bound fails the graph without a
smoke receipt. Push-worker and HTTP-function queue visitors use the same limits,
parse only the single synthetic item acknowledgement, and never persist the
response. Pull workers use a separate generic invocation probe and discard its
bounded response. This bounds qualification work even when a candidate streams
continuously. Schedd's opt-in graph poller invokes these visitors for eligible
HTTP, HTTP-function push, and push/pull worker graphs; the default remains
disabled pending native acceptance. Jobs use the separate reviewed one-shot
contract above, but remain gated on native acceptance.

The graph visitors are wired to schedd's gated production poller. They support
HTTP/1 binding calls after runtime publication, explicit reviewed target ports and
acyclic prepared dependency cohorts. HTTPS requires the existing gateway TLS
listener and guest service CA to be configured; the scheduler refuses an HTTPS
graph unless its resolver supplies the verified internal listener. The private
route still verifies the exact graph and binding on every request. Dependency
calls during pre-readiness startup, production dispatch wiring, production
capture/restore evidence and the transition to serving binding URLs remain
outstanding.
HTTP/2 and gRPC targets are also rejected before graph boot and private routing.
The node-local listener URL and each frozen graph transport are preflighted
against the selected node before instance admission or VM boot.

Qualification guests use a dedicated drop-by-default network policy. They can
reach only the node-local scoped service proxy (`10081`, with `443` available
only when its private CA is configured) and pinned DNS. App and operator CIDR
exceptions, generic egress ports, static egress, private-network routes,
service-address shortcuts and the legacy proxy do not carry into a candidate.
Live app egress and private-network reconciles exclude qualification instances,
so later policy updates cannot widen the assessment boundary.

## Qualification and activation evidence

A capture receipt records the original attempt, instance, node, wake, immutable
artifact, fresh runtime input acknowledgement, Firecracker version and private capture namespace.
It is immutable and retryable after a lost commit response. Retirement must
identify the exact capture receipt, native generation and kernel boot that
produced the capture; the receipt remains historical after retirement. The
captured version travels through the private vmmd wire contract and is
mandatory for both memory and PostgreSQL stores, so legacy or incomplete
capture receipts cannot qualify as restore inputs. Native target claim and
load now reject a mismatch. The production
Manager restore producer still needs to pass vmmd's detected running version
to both guards.

Restore runtime publication is tied to the distinct, durably dispatched target
frame and its original capture reservation. The target gets its own runtime
input receipt; it cannot borrow the retired capture instance's receipt. Private
service resolution selects the current restored reservation when one exists and
fails closed until both caller and dependency have current runtime receipts. The
memory and PostgreSQL stores enforce this boundary, including migration replay
and direct-write guards. This records state-store evidence only: production
scheduler-to-VMMD restore dispatch, guest configuration delivery, application
acknowledgement and readiness are still required before a restored workload can
qualify.

Fresh qualification boots now wait for guest-init receipts for the main
workload and every prepared sidecar before publishing runtime inputs. The main
receipt checks the canonical digest of the non-secret API env and a MAC of the
selected secret-key set. Each sidecar receipt MACs the exact staged sidecar env
and shared API env. Receipts contain no configuration values, secret values,
or secret names; the per-attempt MAC key stays out of them. Guest-init removes
both internal controls before constructing application process environments.
These receipts prove guest configuration loading, not application behavior,
smoke, restore, or activation. In the internal qualification graph path, the
scheduler also persists an immutable digest of each exact API environment after
vmmd confirms the attempt-bound guest receipt. That digest includes injected
scoped binding URLs but stores no values. Both source and restored-target
acknowledgements are required before a restore receipt is accepted, and the
graph assessor reports them separately. Production qualification dispatch is
still gated.

The internal graph assessment reports prepared artifacts, capture, restore,
guest configuration acknowledgement, framework readiness, application smoke,
and retained workloads separately. A partial cohort, changed artifact or stale
runtime input cannot borrow another member's evidence. Every changed candidate
needs its own qualification receipts. An unchanged HTTP or worker member counts
only when its exact deployment is still live, unheld, and selected by the
environment's current active release set; an ID stored in old graph JSON is not
proof. Job members still need a private one-shot execution receipt. The
activation and serving blockers remain after qualification. The dedicated
restore receiver records framework readiness from the exact private target
without publishing ordinary serving readiness. Activation and serving
convergence still need their own committed receipts and production adapters. No
qualification receipt releases a held deployment, emits an ordinary
deployment-ready event, or advances the applied Git revision.

## Execution gates

Production qualification polling, graph activation and continuous enforcement are not enabled by this preview. The qualification graph poller is available behind `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1`, which must remain unset until dedicated Linux capture/restore acceptance passes. Service-bound graphs additionally require a node-local schedd and its private HTTP `:10081` and/or HTTPS `:443` URL in `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTP_URL` and `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTPS_URL`; unresolved or non-local placement fails before VM admission. Schedd also recovers abandoned qualification VM executions using the durable original frame; this performs native cleanup only and does not retry VM creation or advance qualification. Native process recovery remains an explicit vmmd opt-in (`native_process_recovery`, default `false`). The native capture backend rejects unsupported snapshot publication before pause or capture effects.

The user-authorized internal nested KVM node passes actual Firecracker capture,
original-VM resume/retirement, and a private dedicated target's paused load,
resume, entropy hook and independent retirement under its normal RAM fence.
The separate ordinary restore regression, ownership/recovery fixtures and
leakcheck also pass. This is scoped nested-node evidence. An internal scheduler
runner now executes capture, complete source retirement, distinct same-node
restore admission/runtime publication, and a second graph visitor with bindings
routed to restored targets. It records a durable restore receipt for each
successfully restored and retired candidate. After the complete restored graph
visitor succeeds and every target retires, it records one immutable smoke receipt
per member. The graph assessor clears each evidence blocker only when every
candidate has the corresponding receipt. Smoke receipts must name a
deterministic HTTP health policy derived from the candidate's explicitly
Git-owned `runtime.healthz` path and `runtime.port`; inherited paths or ports
and TCP startup readiness are not accepted. The policy digest fixes the
resource, app identity, port, method, status range and timeout. The
restored-graph visitor now executes the contract over vmmd's node-routed HTTP
stream, accepts only a complete 2xx response, discards headers and body bytes,
and emits a sanitized digest tied to each restored instance. The dispatcher
records receipts only after the whole restore cohort retires. Schedd's opt-in
production poller uses this path, but remains disabled until dedicated native
acceptance is complete.
The scheduler also persists a digest of the exact guest API environment only
after vmmd returns with its attempt-bound guest-init acknowledgement; restore
evidence requires acknowledgements for both source and restored runtimes. The
native Manager and vmmd RPC now orchestrate a restore into a distinct private
target, validate the Firecracker version before native effects, stage the
receipt-verified capture inputs, open only the registered qualification
channels, and wait for the target's guest configuration acknowledgement.
Restored framework readiness is recorded separately. When the schedd poller is
explicitly enabled, it invokes this capture/restore/smoke flow for eligible
graphs; no complete production qualification has passed dedicated host
acceptance yet. Owner-authorized artifact retirement, dedicated Linux
capture/restore acceptance, activation and serving convergence remain
outstanding; production gates stay closed.

Private writable drives and capture outputs default to requiring their data and
native journal on the same filesystem. An explicit startup-only disk adapter,
`WithNativeImageStagingRoot` configured before `WithNativeProcessRecovery`, can
instead own their temporary names in a pre-existing private `0700` disk directory
on the original ext4/XFS/Btrfs filesystem, outside the jail. It persists an
exclusive original inode/epoch claim before creating a link and holds a separate
daemon ownership lock. Ordinary preparation removes the link and claim before
returning; producer death retains them for original-owner retirement. Live
snapshot outputs keep only their original links through the joined one-shot
handoff into the original VM mount namespace, then retire them before pause.
That helper receives pinned descriptors, exact original root/namespace/process
and output-epoch identities, and a strict capture-derived scope. No recovered
frame or caller-supplied pathname grants a second handoff.

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
capture entry point remains gated pending durable recovery for owner-authorized
artifact retirement and dedicated Linux capture/restore acceptance.
Publication errors may follow an uncertain commit, so they supply no overwrite
or deletion authority.

`WithNativeSnapshotPublicationRoot`, configured before native recovery, adds a
separate pre-existing private disk directory for immutable capture intent. vmmd
can opt into it with `native_snapshot_publication_root` only when
`native_process_recovery` is enabled. The root is unset in shipped production
configuration until native acceptance passes. The
original successful begin pins the storage backend and persists the complete
attempt, physical process, lease and four logical object keys before output
writes. Writes require that original capability and unchanged intent inode.
Startup validates old intents without replaying or deleting them. Losing the
jail journal or rebooting cannot create publication or cleanup authority from
an intent. Original successful writers now append per-object receipts bound to
that intent inode. All four receipts are rechecked before the cohort succeeds.
Receipt-bound reads pin the recorded GCS generation or local inode observations
and verify source size and SHA-256 to EOF. Stored accounting uses original
encoded/allocated bytes, excluding the backing sidecar. These reads bypass caches
and fallback stores. GCS offers generation-conditional retirement; local
retirement is explicitly unsupported because stat followed by unlink can remove
a replacement. The scheduler-to-vmmd retirement path now requires persisted
restore and smoke evidence, and vmmd conditionally retires the exact original
GCS generations under both native retirement journals. A persistent retiring
marker blocks restore before the first delete; a completed tombstone makes
retry idempotent. Local conditional retirement remains unsupported. Durable
startup recovery rediscovers in-progress markers and retries them before native
admission. A vmmd loop retries those same markers every minute when the
publication root is configured, in pages of at most 64 records. Its cursor
advances past failed deletes so later captures are not starved; after reaching
the end it wraps and retries failures again. Failed deletes retain their marker.
This loop creates no retirement authority and does not retry ordinary candidate
or serving cleanup. Shipped production configuration leaves this root unset,
and native qualification polling remains outstanding.
An internal restore-input barrier now requires the complete
four-receipt cohort and its matching capture completion. It verifies each source
to EOF, joins reader Close, checks the bounded backing sidecar against the
original identity and supplies only private anonymous read-only disk descriptors
to a synchronous consumer. All descriptors close before return; a partial copy
creates no named file or cleanup authority. An internal native staging adapter
clones those descriptors onto the configured persistent disk, verifies the
original receipt digest again, and records the prepared target's exclusive
image epoch and disk claim before creating any temporary source link. Memory
and device-state binds are read-only; the drive is a separate writable clone.
Permission changes and guest writes cannot reach the verified source inputs.
Only the original daemon and its prepared target can stage this cohort; recovered
records cannot create a producer. An uncertain first effect blocks a retry until
original-owner retirement joins its retained epoch. A separate internal store
contract now reserves one charged restore target after the captured producer
has retired. Its immutable execution retains the original capture ID and owns
distinct placement, wake and cleanup authority. Admission and dispatch require
the current reviewed attempt and fresh captured inputs; replay, generic capture
dispatch and borrowing the producer's retirement are refused. Unretired restore
targets block attempt replacement and remain recoverable after parent purge.
The existing capture wire and native incoming journal reject restore frames.
A separate internal native restore profile now joins the immutable target frame
to the original completed capture after complete producer retirement. Both
profiles and generic UUID callers share the same admission lock. A lost physical
publication retains the target's planned lease across restart; duplicate delivery,
profile substitution, changed capture evidence and generic cold fallback are
refused. The target owns a distinct native generation and retirement receipt.
Its first live producer can read the four original receipts and stage private
image epochs while holding target authority through the complete operation.
Recovered records grant cleanup and allocation evidence only. Capture also
retains the original kernel/base jail names, digests, sizes and native source
references. The target's first producer verifies both backing copies as private
anonymous disk descriptors before staging separate read-only image epochs with
those captured names. Current candidate paths supply bytes only. Missing or
changed evidence, partial staging replay and additional read-only workload
drives are refused. This profile and staging path are internal and do not yet
have a scheduler dispatch or RPC entry point. Its internal loader verifies all
five target-owned image epochs and original content receipts, loads paused,
then records separate resume and mandatory entropy/clock-hook effects. Lost
responses and recovered records cannot replay those effects. Its original live
context carries a one-shot load permit; losing the journal cannot reset it.
Verified target clones retain a v3 disk claim after dropping their temporary
source names. Same-boot inventory preserves that claim until the original
anchor retires and cannot recreate missing native authority from it.
The loader retains read-only descriptors for the original target cgroup and
normal RAM limit, and checks exact process membership and credentials before
each effect. A replacement cgroup or raised limit stops the sequence. The load
journal supplies no qualification, serving or graph readiness. The live producer
can open a separate private group of platform receivers after its acknowledged
resume hook. Every stream carries the original target frame and checks the
original Unix peer, process, cgroup, normal RAM limit, jail/socket inodes, image
epochs and load evidence around I/O. It never uses captured CID lookup or
ordinary serving callbacks. A lost hook acknowledgement, revoked/expired target
or daemon restart cannot recreate the channel producer. Streams are bounded to
64 per port and five seconds, capped by the original target deadline. Endpoint
metadata changes use pinned descriptors; hosts without the required Linux
`Fchmodat2` support refuse preparation. Closed sockets retain their names for
original jail retirement, so a late close cannot unlink a replacement endpoint.
These receivers grant transport identity only. Fresh reviewed runtime inputs,
scoped service binding reinstallation, original Firecracker provenance and
isolated graph smoke remain outstanding. The restore path now has a distinct
handler registry that cannot fall back to serving callbacks. It accepts only
the guest configuration receipt; dynamic app environment, runtime secrets and
workload identity are explicitly unavailable to qualification candidates until
reviewed scoped projections exist. No production Manager/RPC path opens these
receivers yet.

The internal capture producer begins
intent before output preparation and publishes memory, device state, the frozen
private drive and backing identity. Actual buffered snapshot writes require the
existing temporary snapshot headroom policy. The producer pins and journals the
original cgroup, raises its limit only after confirmed pause, and requires durable
restoration plus exact normal-limit readback before resume or publication.
A bounded original-owner cleanup capability can restore after the effect
deadline; it grants no resume or publication. Unfinished scopes block resource
acknowledgement and generation replacement until their original cgroup is
retired. Controller repair and the legacy pathname widening helper are refused.
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
