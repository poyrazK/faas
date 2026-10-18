# Gregale CI internal control plane

Gregale CI is **internal and disabled by default**. This release validates and
stores pipeline revisions and durable run receipts. Opt-in GitHub integration
can admit blocked receipts and publish Checks explaining compute unavailability.
The deployed control plane does not execute commands, pull CI images, check out
repositories, upload artifacts, or create CI Jobs/VMs.

The repository also contains the internal execution service described below:
fresh Jobs admission, a bounded restart-safe dispatcher, and status/log/artifact
commands. Every production flag remains off. Native KVM and real GCS acceptance
must pass before a compute pilot. Blocked receipts retain their existing behavior.

## Gates

| Setting | Default | Effect |
|---|---|---|
| `FAAS_CI_INTERNAL_ENABLED` | `0` | Only the exact value `1` permits internal metadata access. |
| `FAAS_CI_INTERNAL_ACCOUNTS` | empty | Comma-separated account UUIDs; an empty or malformed list admits nobody. |
| `FAAS_CI_GITHUB_ENABLED` | `0` | Enables GitHub binding routes and verified trigger capture/admission for allowlisted accounts. |
| `FAAS_CI_GITHUB_CHECKS_ENABLED` | `0` | Permits githubd Check publication; real execution Checks additionally require their own gate. |
| `FAAS_CI_EXECUTION_CHECKS_ENABLED` | `0` | Exact opt-in in apid for fresh Jobs execution Check intent and in githubd for execution progress/results. Also requires the GitHub Checks gate. Old executions never backfill. |
| `FAAS_CI_AUTOMATION_ENABLED` | `0` | Explicit opt-in in githubd and apid for new verified push/PR capture and admission through saved profiles. Requires an active automation policy and all normal execution gates. |
| `FAAS_CI_DISPATCH_ENABLED` | `0` | Additional gate for fresh Jobs execution admission and CI-owned scheduler launches. |
| `FAAS_CI_WORKER_ENABLED` | `0` | Starts the bounded apid execution worker; also required for fresh HTTP admission. Retain during cancellation/output drain. |
| `FAAS_CI_TRANSPORT_ENABLED` | `0` | Mounts guest transfers and authenticated artifact downloads; additionally required by scheduler launches and grant issuance. |
| `FAAS_CI_RETENTION_ENABLED` | `0` | Starts bounded storage retirement independently of transport/admission. |
| `FAAS_CI_GCS_BUCKET` | empty | Existing dedicated private GCS bucket; required when transport, retention or the execution worker is enabled. |
| `FAAS_CI_RUNTIME_URL` | empty | Credential-free HTTPS origin, identical in apid and schedd; empty refuses transport and CI launch. |

All settings are read at daemon boot. Keep the account allowlist and admission
gates consistent across apid, githubd and schedd. Generated units retain all flags off
and the allowlist empty. This change enables no setting in a fleet.
Enabling metadata access never enables compute. The dispatch gate alone starts
no worker; keep execution flags off until native runner/transport/provider
qualification has passed and an internal pilot is authorized.

The CI migrations must be applied through the normal release path before internal
API testing. All routes require authenticated active accounts and admin scope;
mutations and every execution route also use the existing MFA gate. Execution
routes additionally require the configured HTTPS origin. Disabled and unlisted requests return
404. An operator session grants no cross-account access.

With admission disabled, the entire account-facing CI API returns the ordinary
unregistered-route 404 before authentication and method matching, including
anonymous requests and unsupported methods. The customer CLI, product capability
catalog, generated capability matrix, and public API specification do not advertise
this service. Internal `gregalectl ci` tooling and this operations guide remain
available. A separately enabled runtime transport can finish previously granted
guest output/stop recovery while admission is disabled; HTTPS and per-run proof
checks still apply, and account-facing metadata and downloads remain hidden.

Apply the Jobs adapter migration even with CI disabled: ordinary Jobs retry and
replay guards read its ownership journal. Roll back binaries that read that
journal before dropping it. The migration refuses rollback while Jobs execution
records remain; reconcile compute and retained output before removing recovery
authority.

## Internal health and metrics

`gregalectl ci health` reads `GET /v1/internal/ci/health`. It requires internal
opt-in for the account, admin authentication, the existing MFA policy, and the
configured HTTPS origin. Worker, dispatch, transport, retention, automation and
Check publication can remain off. It reports account-scoped work/usage and an
anonymous fleet worker summary; no GitHub/GCS/native call occurs. An unavailable
health query returns an error rather than empty healthy evidence.

```sh
gregalectl ci health
```

Read `gates` as the responding apid's boot configuration, independently of
`health.workers`. Remote githubd configuration is represented by its observed
workers. Unseen workers are `unknown`; configured-off workers are `disabled`;
missing wired dependencies are `blocked`. A group with fresh heartbeat but no
cycle progress for 120 seconds is `stalled`. The latest failed cycle is
`degraded`, and a completed successful or empty poll is `healthy`. A group which
has not completed a cycle starts as `starting`. A heartbeat older than 45 seconds
is `stale`. Nothing here establishes native compute qualification or enables
public compute capabilities.

Worker evidence uses at most eight groups per kind (48 total rows). apid owns
dispatcher, metadata/automatic admission and retention observations; githubd owns
metadata/execution Check observations. A dispatcher group contains its four local
loops. Pulses run every ten seconds independently of work, and completion samples
coalesce between pulses. Role status prefers an available healthy group; inspect
all per-condition counts for partial failures. Crashed slots remain stale until
reclaimed after 90 seconds. Token fencing prevents an old process overwriting its
replacement. Claim/pulse retries preserve slot identity and progress after a lost
database acknowledgement. If all eight slots for a kind are fresh, additional groups still
run but cannot report; this is not a complete fleet replica census.

Queues include executions, metadata/automatic admission, both Check outboxes,
unapplied Check retry intent and retirement. Conditions overlap: a stale
in-flight claim can also be due. `due` means time eligibility; configured gates,
authority, retry budgets and native safety still apply. Retirement includes
expired/orphan/deleting candidates that may still be unsafe to retire. Oldest
item age uses item creation, so a newly dirty old Check can have a large age;
it is not delivery latency or duration in the current state.

Quota headroom uses existing limits: detailed executions 32, active executions 4,
dispatcher leases 2, archives 128, profiles/revisions 64 each, metadata receipts
256 and combined source events 256 per account. Live storage bytes/objects and
I/O are separate from the permanent object-journal allowance. Archived detail
leaves its 32-slot budget but consumes archive allowance. Retired tombstones and
authenticated source deduplication evidence remain charged. Counts never include
another account's work or qualification executions in Jobs usage.

The private apid/githubd metrics listeners expose these families with their
usual daemon prefix:

| Suffix | Meaning |
|---|---|
| `_ci_health_enabled` | Observation is configured; all-flags-off emits zero without querying/pulsing CI state. |
| `_ci_worker_enabled{worker}` | Current boot configuration for workers owned by this daemon, including explicit zero when disabled; independent of database reads. |
| `_ci_health_up` | This scrape read durable health successfully; zero on read error, omitted when disabled. |
| `_ci_queue_items{queue,condition}` | Fleet pending/in-flight/dead/due/expired-lease counts. |
| `_ci_queue_oldest_item_age_seconds{queue}` | Oldest retained pending/in-flight item age since creation. |
| `_ci_resource_used{resource}` | Fleet usage; per-account limits are not multiplied into a fleet limit. |
| `_ci_resource_limit{resource}`, `_ci_resource_headroom{resource}` | Existing global limits only: active executions, dispatch leases and storage budgets. |
| `_ci_worker_replicas{worker,condition}` | Bounded anonymous worker-group observations. |

All labels use fixed values; no account, repository, commit, execution, host or
raw error labels are emitted. Failed scrapes omit dependent queue/quota/worker
evidence. Gate health alerts on observation being enabled, then investigate
unavailable reads, stale/stalled workers, dead deliveries, old pending items and
low headroom. Disabled workers with retained backlog explain paused work and do
not indicate a crash. The fleet gauges repeat on each daemon/replica; select a
single scrape target or deduplicate gauges (`max` usage, `min` headroom), rather
than summing them, for fleet dashboards.

Internal access or an independently enabled worker, retention or GitHub Check
publication path activates observation. This supports draining with admission
off. All shipped flags remain off and the allowlist empty; no new flag is added.
Apply the health migration through the normal release path before using this
surface. Its rollback drops only diagnostics, so subsequent health reads fail
until the migration is restored; execution intent and recovery leases survive.

### Internal CI dashboard and warnings

The private Grafana dashboard `/d/faas-internal-ci` shows read status, worker
configuration/availability, queue and Check backlog, expired leases, global
quota usage/headroom, storage bytes and anonymous worker-condition counts.
The canonical JSON is `deploy/grafana/internal-ci.json`, mirrored into the
existing Grafana role. All status/count tables read instant samples and show
Unknown when evidence is missing. Historical charts keep gaps.

The `faas_internal_ci` Prometheus group normalizes readable fleet snapshots
from apid and githubd. Use its `faas_ci:*` recordings instead of adding raw
per-replica gauges. Max counts/usage and min limits prevent double-counting;
headroom is derived conservatively. Fresh owner-worker switches distinguish
intentional pauses from missing heartbeat evidence. All-flags-off emits only
zero configuration samples and performs no health query or heartbeat write.

Five internal warning alerts cover unavailable reads, enabled workers without
a healthy/starting group, expired leases, dead Check delivery (five minutes),
and global quota usage at or above 80 percent (ten minutes). Work-specific
warnings require the owning path configured on. Existing daemon-down alerts
cover lost scrape targets, whose CI configuration becomes unknown. Retained
backlog remains visible during a pause. Oldest-item age alone does not alert.

See [the CI operational runbook](../runbooks/FaasInternalCIOperations.md) for
triage, safe Check retry and permanent quota accounting. These assets do not
enable a flag, grant native readiness or perform remediation. Validate them with
`make test-ci-observability`; a pinned external promtool can be passed through
`PROMTOOL=/absolute/path/to/promtool`. The existing deployment roles provision
the dashboard/rules only when the normal release process is run.

## Internal execution service (disabled)

Fresh executions use a separate API and CLI namespace. `ci run` and existing
blocked GitHub receipts never start this worker's native work. After native
qualification, an operator can explicitly opt in one account using all admission,
GitHub, dispatch, worker and transport flags plus the qualified bucket/origin.
Use matching internal/dispatch/transport policy in schedd and its normal Jobs
configuration. `FAAS_CI_GITHUB_CHECKS_ENABLED` is optional and still reports the
separate blocked-admission workflow. Public compute capabilities remain false.

### Saved execution profiles

Save a profile once to reuse a verified binding, immutable revision and exact
Jobs template mapping. Profile setup requires internal/GitHub opt-in, the
account allowlist, admin/MFA authentication and the configured HTTPS origin.
Worker, dispatch, transport and storage may remain disabled during setup.
Templates must already be account-owned and materialized as described below.

```sh
gregalectl ci profile put --name checks-v1 --revision REVISION_UUID --binding BINDING_UUID --template test=JOB_UUID --template build=BUILD_JOB_UUID
gregalectl ci profile list
gregalectl ci profile status --id PROFILE_UUID
```

The profile pins the binding generation and materialized image identities as
well as the revision. Names are permanent and configuration is immutable:
identical PUTs return the same profile; changed inputs require another name.
There are at most 64 retained profiles/account, including disabled profiles.
Profiles expose template UUIDs and a revision digest, without private image
locations or commands. Saving one creates no execution or native work.

After the normal compute gates are qualified and deliberately enabled, submit
with only the profile, exact commit and fresh request key:

```sh
gregalectl ci execution submit --profile PROFILE_UUID --commit FULL_LOWERCASE_COMMIT_SHA --key smoke-profile-001
gregalectl ci profile disable --id PROFILE_UUID
```

The API rechecks repository authority, current binding generation and template
images during admission. A changed binding or rematerialized template requires
a new validated profile. Do not combine `--profile` with `--revision`,
`--binding` or `--template`. Execution status retains `profile_id`, and stable
request retries reuse the same execution. Profile disable is permanent and
prevents new submissions through that profile; existing executions continue
under their frozen configuration and can be cancelled normally. Read/list and
disable remain available when new GitHub/compute admission is disabled.
Profiles do not enable automatic GitHub execution or change blocked receipts.

| Method | Path |
|---|---|
| PUT | `/v1/internal/ci/profiles/{name}` |
| GET | `/v1/internal/ci/profiles` |
| GET | `/v1/internal/ci/profiles/{id}` |
| DELETE | `/v1/internal/ci/profiles/{id}` |

PUT accepts `revision_id`, `binding_id` and a `templates` job-to-UUID object.
The existing execution POST accepts `profile_id` plus `commit_sha` and the
Idempotency-Key header, or its existing explicit configuration. See
[ADR-1655](../adr/1655-internal-ci-execution-profiles.md).

### Pass artifacts to dependent jobs

A job can declare named files from its direct `needs` dependencies:

```yaml
needs: [build]
inputs:
  - name: app
    job: build
    path: dist/app
steps:
  - name: integration
    run: chmod +x .gregale-ci-inputs/app && .gregale-ci-inputs/app --self-test
```

The producer must select `dist/app` in `artifacts`, succeed, seal its output and
finish native cleanup before this input is usable. Inputs are literal files,
limited to eight/job, 32 MiB/file and 64 MiB combined. Each consumer receives its
own account/execution's frozen producer attempt, digest and length. Inputs do not
carry across reruns or executions. Missing required output fails the consumer
with exit 125 and skipped steps before a native run is created.

The guest downloads verified files under `.gregale-ci-inputs/NAME` before any
commands. Keep that reserved directory absent from source. Copies have mode 0600;
set executable mode explicitly when needed. No archive extraction or source
replacement occurs. Download/checksum/cancellation errors prevent steps from
starting. Controller/API recovery keeps the same producer identities. Normal
retirement protects input bytes until the execution and native consumers finish.

Input jobs require manifest version 2, or version 3 when they also configure
environment/working directories, and a newly qualified pinned runner image.
Unconfigured jobs without inputs keep version 1. Upgrade readers/runners before using `inputs`, drain
input executions before a binary downgrade, and keep admission off during binary
rollback. Retained input definitions require updated readers before fresh
admission. Existing CI flags remain off. Native
and provider verification stays with the separate qualification agent. See
[ADR-1588](../adr/1588-internal-ci-dependency-artifact-inputs.md).

### Run named matrix variants

Declare literal variants to share steps across pinned toolchain images or
environment configurations:

```yaml
image: registry.example/ci@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
matrix:
  alpha:
    env: {TEST_SUITE: alpha}
  beta:
    image: registry.example/ci@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    env: {TEST_SUITE: beta}
steps:
  - name: unit
    run: make test
```

For a job named `test`, planning creates `test-alpha` and `test-beta`. A matrix
has one to eight variants; all concrete jobs combined must fit the existing
16-job limit. Job/variant/combined names must fit CI identifier rules; collisions
with declared or expanded names are rejected. The base image remains required.
Variant values override job environment; step values override variants. Existing
non-secret environment bounds and working-directory behavior apply to each job.

`needs: [test]` waits for both variants, including confirmed native cleanup.
`needs: [test-beta]` selects one. Do not combine overlapping logical/concrete
dependencies. Artifact inputs must name the concrete producer, such as
`job: test-beta`; they never choose a variant implicitly. Failed variants skip
their dependents while independent siblings continue. Execution cancellation
still cancels all active variants and leaves waiting jobs unstarted.

Use concrete names as the keys in execution/profile `templates` maps. Each
template must match its variant image. Same-image variants can share an eligible
template, subject to existing native capacity controls. CLI/API plans, status,
logs, artifacts/reports and the pipeline GitHub Check summary identify each
variant separately. Restarts retain attempts; reruns retain configuration with
fresh identities.

Matrix expansion happens before dispatch. Concrete jobs use the existing v1/v2/v3
manifest matching their actual features; no matrix declarations reach guests.
Upgrade definition readers/controllers before use and keep admission off during
rollback. Existing feature flags remain off and native qualification remains
separate. See [ADR-1590](../adr/1590-internal-ci-named-matrix-jobs.md) and the
[matrix example](../../examples/internal-ci/matrix.yaml).

### Configure step environments and working directories

Jobs and steps accept literal `env` string maps and checkout-relative
`working_directory` paths:

```yaml
env:
  BUILD_MODE: test
working_directory: packages/api
steps:
  - name: unit
    env:
      BUILD_MODE: unit
      OPTIONAL_FEATURE: ""
    run: make test
  - name: root-check
    working_directory: .
    run: test -d "$GREGALE_CI_WORKSPACE/packages/api"
artifacts: [packages/api/reports/**]
```

Step values override job values, including empty strings. Omitted step directory
inherits the job directory; explicit `.` overrides it with the checkout root.
All paths are relative to that root. Directories must exist when a step starts;
an earlier root-scoped step may create one. Missing directories, links (including
contained links), special files and traversal fail that step with exit 125 and
skip later steps. Selected directories are pinned through command start.

`env` is plain revision/execution metadata: use non-secret literals. YAML values
must be strings; quote numbers and booleans. Values are not interpolated, and
each step receives a fresh environment without runner/host inheritance.
Runner-managed `PATH`, `HOME`, `LANG`, `CI`, workspace/runtime identity variables,
shell startup settings and loader variables are reserved; the full list is in
[ADR-1589](../adr/1589-internal-ci-step-configuration.md). Custom declarations and
merged step environments are limited to 32 names, 64 bytes/name, 1,024 bytes/value
and 8 KiB combined entries including separators/NULs. Directory paths are at most
256 bytes. Existing definition/native manifest limits also apply.

`HOME` and `GREGALE_CI_WORKSPACE` always point to the checkout root. Artifact/JUnit
selectors and dependency inputs keep that same root; access inputs from a nested
step as `"$GREGALE_CI_WORKSPACE/.gregale-ci-inputs/NAME"`.

Configured jobs require manifest version 3 and an updated qualified pinned runner
image. Input-only jobs keep version 2; unchanged jobs keep version 1. Upgrade
readers/runners before using configuration, drain configured executions before a
binary downgrade, and keep admission off during rollback. Flags remain off and
native qualification remains with the separate agent.

### Rerun an execution explicitly

After compute qualification and deliberate opt-in, rerun a terminal execution
whose native tasks and transfers have finished:

```sh
gregalectl ci execution rerun --id ORIGINAL_EXECUTION_UUID --key STABLE_RERUN_KEY
gregalectl ci execution status --id NEW_EXECUTION_UUID
gregalectl ci execution reports --id NEW_EXECUTION_UUID
```

Rerun restarts the complete frozen pipeline at its original commit with fresh
execution, attempt, log, artifact, report and Check identities. Status, live
history and archive receipts include `rerun.parent_execution_id` and
`rerun.root_execution_id`. Original results remain intact. The current account,
repository, binding generation, active profile and materialized images must
still match. Changed images or disabled profiles require a fresh validated
profile and the existing explicit submission workflow.

The API is `POST /v1/internal/ci/executions/{id}/rerun`, with body `{}` and
`Idempotency-Key`. It accepts no configuration/job/attempt overrides. Retries
with the same parent and key return the same execution; the key is shared with
ordinary execution submission and cannot identify a different request. Receipt
replay makes no provider call and consumes no quota or new command attempt.
New keys intentionally create new executions under normal quotas.

`ci_rerun_not_ready` (409) means the parent is active or native/output/transfer
cleanup is incomplete. Retained payloads and pending remote Check delivery do
not prevent rerun. An archived parent cannot create a new rerun because its
private configuration was compacted. A retained child's existing receipt can
still replay after parent archival; an archived child returns
`ci_execution_archived` (410) while keeping its key reserved.

Reruns use the same internal/admin/MFA/HTTPS, worker, transport, storage and
dispatch gates as fresh submissions. All production defaults remain off. A
rerun of an automatic PR/push execution is explicit manual work and retains
lineage without replaying webhook admission or inheriting automatic
supersession. Jobs retry remains zero and controller recovery never invokes
rerun. See [ADR-1584](../adr/1584-internal-ci-execution-reruns.md).

### Explicit template submission

The existing explicit submission form remains available:

Use existing metadata commands to save a revision and verify its GitHub binding.
Create one account-owned materialized Jobs template per pinned image, with
command `[/usr/local/bin/gregale-ci-runner, v1]`, 512 MiB, one task/parallelism,
zero retries and no environment overrides. The template image must equal the
planned digest; wait for materialization to report ready. Then:

```sh
gregalectl ci execution submit --revision REVISION_UUID --commit FULL_LOWERCASE_COMMIT_SHA --binding BINDING_UUID --key smoke-execution-001 --template test=JOB_UUID --template build=BUILD_JOB_UUID
gregalectl ci execution list
gregalectl ci execution status --id EXECUTION_UUID
gregalectl ci execution logs --id EXECUTION_UUID --attempt ATTEMPT_UUID --offset 0 --limit 16384
gregalectl ci execution artifact --id EXECUTION_UUID --job test --path reports/unit.xml --output ./unit.xml
gregalectl ci execution cancel --id EXECUTION_UUID
```

Logs return one bounded retained-prefix page. Text mode writes raw bytes and a
cursor/completion/truncation receipt to stderr; `--json` retains the base64 byte
representation. Jobs output becomes available after guest finalization and native
stop, so this release does not stream live step logs. Artifact downloads verify
size and SHA-256 against frozen execution metadata, refuse redirects and existing
output paths, and publish a private file only after complete verification.

| Method | Path |
|---|---|
| POST / GET | `/v1/internal/ci/executions` |
| GET | `/v1/internal/ci/executions/{id}` |
| POST | `/v1/internal/ci/executions/{id}/cancel` |
| GET | `/v1/internal/ci/executions/{id}/attempts/{attempt}/logs?offset=0&limit=16384` |

All routes require account authentication, internal allowlisting, admin scope,
the existing MFA session gate, HTTPS and `no-store`. Existing bearer-key semantics
are unchanged. Status omits physical image keys and raw logs. No installation
token, signed URL or provider credential is returned. Qualification IDs return 404.

Admission allows four active executions/account, sixteen globally, and retains
at most 32 detailed Jobs executions/account. Replays remain available when capacity is
full. Four workers share database-fenced leases, with at most two leases/account.
They reconcile every two seconds; errors back off durably to sixty seconds.
Restart resumes the same attempt/run; controller recovery never retries user
commands. Storage cleanup alone does not free detailed-history slots; explicit
terminal archival, described below, reclaims them while retaining identity.

To stop new work and drain, disable `FAAS_CI_DISPATCH_ENABLED` in apid and schedd
while keeping the worker, storage and guest transport configured. Restart through
the normal deployment workflow. The worker cancels executions removed from its
policy and collects their final output after native stop. Status/log/cancel remain
available for internally allowed accounts. Turning the worker off pauses
reconciliation; it does not cancel running compute. Removing transport prevents
guest finalization. Observe terminal CI status and actual stopped native task
receipts before disabling drain infrastructure. Provider failures retain durable
intent and storage reservations for retry; do not delete journals or clear leases.

`make test-ci-service` runs portable dispatcher/API/CLI checks with PostgreSQL and
synthetic compute. `make test-apid RUN='^TestInternalCI' RACE=1` checks authenticated
route/worker wiring. These are separate from native qualification. See
[ADR-1654](../adr/1654-internal-ci-execution-service.md).

## Automation path filters (disabled)

Optional policy fields `include_paths` and `exclude_paths` filter the complete
pipeline for new authenticated push/PR events. Existing empty filters run every
eligible source without a comparison call. Manual submissions and execution
reruns always run their full frozen pipeline independently of these filters.
After native qualification and deliberate opt-in, configure them with:

```sh
gregalectl ci automation put --binding BINDING_UUID --profile PROFILE_UUID --push-branches main --include-paths 'src/**,go.mod,go.sum' --exclude-paths 'src/docs/**'
gregalectl ci automation events --binding BINDING_UUID
gregalectl ci automation status --binding BINDING_UUID --event EVENT_UUID
```

Each list accepts 16 unique patterns, 256 bytes each. Matching is case-sensitive:
`*` and `?` stay within a component; `**` as a whole component matches zero or
more components. No include patterns means all paths; exclusions take precedence
for each path. Any eligible path runs CI. Both rename/copy paths and deletions
count. Absolute paths, traversal, escapes, character sets, braces and negations
are rejected. CLI lists are comma-separated; use the API for literal commas.
Omitting both lists on a policy replacement removes filters and increments the
generation if configuration changed.

Push comparisons pin webhook before/head SHAs; PR comparisons pin the freshly
verified base/head and their merge base. Missing or invalid pins, unavailable
providers/RPCs, pagination, 100 or more commits, 300 or more files, names above
1024 bytes, malformed responses and the 2 MiB response cap all cause CI to run.
Diverged/force-push comparisons run conservatively. A complete comparison with
no eligible changed files records `path_decision.outcome=skip`, reason
`paths_filtered`, status `ignored`, and creates no execution. Matched changes
record `paths_matched`; incomplete information records `changes_unavailable`.
Decisions retain SHAs and counts, without filenames, patches or provider errors.
Skipped sources cannot be promoted through admission recovery or redelivery.

With latest-only, other unsettled same-generation push sources cause a run
with `prior_source_unsettled` so unadmitted code cannot be stranded. Older
sources already observed as obsolete also require a replacement execution
before filtering can resume.
A skipped push leaves older automatic code work running, including under
`latest_only`. A cumulative filtered PR head follows the existing verified
supersession and cleanup flow before settling. Existing access, generation,
profile/image and fresh-observation fences still apply.

With both existing Check gates enabled at the skip, githubd publishes a
`completed` / `skipped` Check from an atomic source-only outbox. Its external ID
is `gregale-ci-source:EVENT_UUID`; it has no execution ID or native success.
Existing Check worker health includes this queue. Delivery uses eight bounded
attempts and response-loss recovery; failure never converts the skip into work.
Use the skipped-source Check status and recovery commands below for exhausted
feedback. Admission recovery cannot promote the source.

All production gates remain off. Policy setup needs the existing internal/GitHub
account/admin/MFA/HTTPS controls and can happen with compute disabled. See
[ADR-1585](../adr/1585-internal-ci-automation-path-filters.md).

## GitHub push automation (disabled)

After saving an execution profile, configure its binding's exact automatic push
branches. These must be a subset of the binding's push filters. Policy setup
uses the same admin/MFA, allowlist and HTTPS boundary as profiles, plus GitHub
opt-in; compute and automation flags can remain off while saving configuration.

```sh
gregalectl ci automation put --binding BINDING_UUID --profile PROFILE_UUID --push-branches main
gregalectl ci automation list
gregalectl ci automation events --binding BINDING_UUID
gregalectl ci automation disable --binding BINDING_UUID
```

There is one policy per binding. Identical saves preserve its generation;
changes or re-enablement advance it and invalidate pending old-generation work.
Saving a policy creates no execution. Branches are explicit operator trust;
the service does not inspect GitHub branch-protection rules. Push and PR event
types require their respective explicit policy filters; omitted types retain
their metadata-only behavior.

Keep `FAAS_CI_AUTOMATION_ENABLED=0` in both githubd and apid until deliberately
enabling a qualified internal account. Automated admission also needs every
existing execution prerequisite: internal/GitHub, dispatch, worker, transport,
managed storage and trusted HTTPS. Use matching capture/admission settings.
New matching App-signed sources capture their exact SHA, binding, policy/profile and
generation before webhook acknowledgement. apid alone admits execution.

Redelivery cannot promote a logical event already captured as metadata. Disabling
then re-enabling policy cannot replay ignored automatic events. Turning off the
global automation flag pauses already captured automatic intent; disable the
policy to revoke pending work. Disabling a profile or binding also invalidates
pending claims. Existing admitted executions continue and can be cancelled with
the normal execution command. The event history links admitted execution UUIDs.

The original and automatic journals share 256 retained events/account. Existing
active/global execution quotas, the 32 detailed Jobs executions/account bound,
eight source-admission attempts and capped retry timing remain in force. A dead
event has no automatic redrive. Eligible never-admitted events can be explicitly recovered
as described below. Snapshot/history retention still consumes quota;
automatic event journals are not pruned by execution archival. Inspect safe `last_error` classifications
and execution state before changing policy. Retain the automation journal during
binary rollback; terminal events remain deduplication authority.

| Method | Path |
|---|---|
| PUT | `/v1/internal/ci/github/automation/{binding}` |
| GET | `/v1/internal/ci/github/automation` |
| DELETE | `/v1/internal/ci/github/automation/{binding}` |
| GET | `/v1/internal/ci/github/automation/{binding}/events` |

PUT accepts `profile_id`, `push_branches`, `pull_request_branches` and the push
`latest_only` setting. No template overrides or remote
pipeline configuration are accepted. Events admitted to execution do not receive
the separate blocked-admission Check. See
[ADR-1656](../adr/1656-internal-ci-github-automation.md).

### Branch concurrency and latest-only policies

Automatic push runs share one slot per account, binding and exact branch. Waiting
sources retain their admission failure budget and poll again after two seconds.
Manual submissions and other branches remain independent under normal quotas.

Opt in to supersession explicitly when saving a policy:

```sh
gregalectl ci automation put --binding BINDING_UUID --profile PROFILE_UUID --push-branches main --latest-only
```

`latest_only` defaults false. Omitting the flag selects false; changing it
advances the policy generation and invalidates old pending claims. Latest-only
uses a freshly observed GitHub branch head, rather than webhook delivery order.
An unavailable or invalid head lookup never cancels existing work. A missing
branch ignores only the candidate. New captures and observations invalidate
older in-flight head responses.

Older unadmitted events from the same policy/branch become permanently ignored.
Older admitted automatic executions of the same policy generation receive the
normal cancellation request. Their replacement waits for terminal execution,
confirmed native stop, collected output and finished runtime transfers. Retained
artifacts do not delay the next run. Previous-generation executions continue
until completion or explicit operator cancellation; manual runs are independent.

History/status reports `superseded_by`, `superseded_sha`, `superseded_at` and
`branch_wait`, `head_changed` or `branch_deleted` classifications. Supersession
links retained source evidence; it never edits an execution's pinned SHA or
revives a previously deduplicated source. Explicit admission recovery cannot
revive superseded work. Re-pushing an already superseded SHA remains deduplicated;
use a fresh manual submission if that exact commit must run again.

Policy setup creates no compute intent, and all shipped flags remain off. Native
stop/provider qualification is separate. See
[ADR-1581](../adr/1581-internal-ci-branch-concurrency.md).

### Same-repository pull-request execution

PR execution requires explicit base-branch filters in both the binding and its
automation policy. Existing push-only policies have no PR filters and do not
change. The GitHub App also needs `Pull requests:read` permission and
`pull_request` webhook delivery. After saving the binding and a profile for its
current generation:

```sh
gregalectl ci automation put --binding BINDING_UUID --profile PROFILE_UUID --pr-branches main
gregalectl ci automation events --binding BINDING_UUID
```

Either `--push-branches` or `--pr-branches` must be nonempty; both may be given.
Omitting one removes that policy's filters for that event type. Lists are exact,
case-sensitive and limited to 16 entries each, within the corresponding binding
filters. Policy/profile setup admits no execution or native work. Existing
internal, allowlist, admin, MFA and HTTPS guards apply; all compute admission
and publication gates remain separate and default off.

App-signed open, non-draft, same-repository PR `opened`, `reopened`, `synchronize`
and `ready_for_review` events may capture execution intent. A base-branch retarget
also captures through `edited`; title/body edits do not. Existing logical source
deduplication prevents action redelivery and mode changes from launching another
execution. PR head SHAs are pinned; merge refs and fork execution are excluded.
Already blocked/ignored/settled logical sources cannot be promoted or replayed.

Before admission, githubd verifies the account's installation and freshly reads
the PR's state, draft flag, head SHA, base branch and both repository identities.
The controller also checks current repository access and frozen profile/binding
authority. Incomplete responses and provider failures retry within existing
budgets; HTTP 404 is not proof of closure. Closed, draft or changed-target/repository
candidates become ignored with `pr_closed`, `pr_draft` or `pr_changed`.

Each account/binding/PR number has one automatic execution slot, independent of
its base branch. Other PRs, pushes and manual submissions retain independent
slots under normal quotas. PRs always verify the current head; `--latest-only`
continues to control push supersession only. Current PR candidates supersede
older queued sources and cancel admitted automatic work from the same policy
generation. Replacements wait for normal cancellation, stopped native instances,
collected output and completed transfers. Prior-generation executions hold their
slot until completion or explicit cancellation. Retained artifacts do not block it.

Fresh observations are fenced before RPC and rechecked under the account lock.
New source captures or another observation invalidate older responses. A retarget
to an unlisted base invalidates an existing PR observation without creating work.
`pr_wait` defers without consuming the failure budget. GitHub can still change
after any RPC; this is observed state, not a cross-system atomic guarantee.
An admitted execution retains its pinned commit if the PR later closes; no
background PR polling or close-only cancellation is introduced.

History/status adds `kind` and `pr_number`; execution and archive provenance
also retain PR identity. Existing Checks, JUnit reports and recovery apply.
Recovery records intent only; the worker freshly checks PR state again. Native
results remain authoritative. Limits in `pkg/api/limits.go` additionally bound
the provider response to 256 KiB and JSON depth to 32. The PR journal is bounded
by the existing 256 retained source events/account.

Upgrade all readers and apply the migration before opting in. Keep admission
off during binary rollback; rollback refuses retained PR authority. All shipped
flags remain off and release remains paused. See
[ADR-1583](../adr/1583-internal-ci-pull-request-automation.md).

### Recover an exhausted automatic admission

Inspect the original event after resolving provider availability or capacity:

```sh
gregalectl ci automation status --binding BINDING_UUID --event EVENT_UUID
gregalectl ci automation retry --binding BINDING_UUID --event EVENT_UUID --generation CURRENT_RETRY_GENERATION --key STABLE_RECOVERY_KEY
```

Retry accepts only a dead event that exhausted eight admission attempts due to
`unavailable` or `capacity`, with no admitted execution or active lease. It
preserves the original commit, profile, source event and execution identity.
Ignored events and metadata receipts cannot recover through this operation.
Current policy/profile/binding/image authority must still match the authenticated
capture; changed, disabled or re-enabled policies cannot revive old authority.
The request freshly checks repository access, then rechecks local authority
under the account lock.

Use the current `retry_generation` from status for `--generation`, and retain the
same `--key` when repeating an uncertain HTTP response. The same request returns
the original receipt with `Idempotency-Replayed: true`, including after admission.
A different identity for the same key or a stale generation returns
`ci_automation_retry_conflict` (409). Each event permits three lifetime recoveries,
each with eight admission attempts; exhausting that allowance returns 429.

The receipt and reset to pending commit together. The receipt preserves the
previous error/attempts and requested generation/time. No execution or native
intent is created by retry. The ordinary worker later admits the same event
only when all existing gates, quotas and source checks pass. Disabling a policy,
profile or binding continues to revoke recovered pending work.

Status and event history include `retry_generation`, `max_retry_generations`,
`retryable`, `last_retry`, and applicable next-attempt/lease-expiry times.
`retryable` describes stored-state eligibility; it does not establish current
permissions, capacity, worker readiness or native qualification. No lease token,
raw idempotency key, digest, provider response or command is exposed.

| Method | Path |
|---|---|
| GET | `/v1/internal/ci/github/automation/{binding}/events/{id}` |
| POST | `/v1/internal/ci/github/automation/{binding}/events/{id}/retry` |

POST accepts only `expected_retry_generation` and requires `Idempotency-Key`.
Recovery needs internal/GitHub/automation opt-in, the account allowlist, admin
scope, MFA and trusted HTTPS. Worker/dispatch/transport can stay off while the
operator records intent. Reads keep their existing internal/MFA/HTTPS protection
and remain available with new admission disabled. All shipped flags remain off.
Receipts retain their lifetime quota evidence and are not pruned by archival;
the migration refuses rollback while recovery evidence exists. See
[ADR-1592](../adr/1592-internal-ci-automation-admission-recovery.md).

## Execution progress and results in GitHub

Keep `FAAS_CI_EXECUTION_CHECKS_ENABLED=0` in apid and githubd until an internal
pilot is deliberately enabled. Both it and `FAAS_CI_GITHUB_CHECKS_ENABLED`
must equal `1` in apid when admitting a fresh execution to capture Check intent.
githubd needs both flags, internal/GitHub enablement and the account allowlist
to publish. Manual, profile and automatic Jobs executions use the same path.
Enabling publication does not backfill older executions or qualification runs.

One stable external identity, `gregale-ci-execution:<execution UUID>`, follows
the execution from queued through in-progress to a completed success, failure,
cancelled or timed-out conclusion. Cancelling remains in-progress until native
cleanup and execution settlement finish. Intermediate states can coalesce.
Summaries contain execution/job states, step counts and configured aggregate
JUnit counts only, bounded to 8 KiB;
commands, logs, artifact paths, credentials and provider details are excluded.

`ci_execution_checks` is the delivery journal; `ci_executions.version` is its
durable desired-state authority. Inspect `status`, `attempts`, `last_error`,
`check_id` and `delivered_version` when diagnosing delayed publication. A sent
record with a newer execution version is automatically eligible again. Lease-
only changes with identical output are acknowledged without another API call.
Delivery locks its own row during the 15-second provider budget; execution
updates and cancellation proceed independently. Claims use 60-second leases,
eight consecutive attempts and capped backoff. `ignored` means access was
revoked; `dead` means retries were exhausted. Explicit operator recovery is
available as described below. Retain
this journal during binary rollback; migration rollback refuses nonempty data.

Disabling a profile, automation policy or binding does not suppress results for
an execution already admitted. githubd still verifies active account and current
installation/repository ownership before publication. Disabling publication or
removing an account from the allowlist pauses its delivery; later re-enablement
reconciles the latest state of previously captured records only.

Retries discover the stable remote identity after a lost create response, and
read remote identity/version before updates. GitHub has no conditional update
or idempotent create API, so late requests after process/network loss remain a
provider limitation. Portable tests use PostgreSQL and fake GitHub HTTP. Real
App publication and native compute qualification remain separate verification.
See [ADR-1657](../adr/1657-internal-ci-execution-checks.md).

### Structured JUnit test reports

Jobs can opt into JUnit diagnostics using literal paths already selected by
their artifact settings. For example, add these fields to a job that writes
`reports/unit.xml` inside its workspace:

```yaml
artifacts:
  - reports/**
junit:
  - reports/unit.xml
```

Submit a fresh immutable revision/profile using the normal internal workflow;
existing revisions do not change. After an authorized compute pilot produces
an execution, inspect its report projection:

```sh
gregalectl ci execution reports --id EXECUTION_UUID
```

The corresponding read is `GET /v1/internal/ci/executions/{id}/reports`.
It uses existing account/admin/MFA/HTTPS/internal guards and remains available
with compute admission and publication flags off. It performs no storage or
GitHub call. Results include execution/commit/job/attempt identity and each
report's artifact path, digest, byte count, diagnostic status and totals.
Failed/error cases have bounded names, class, suite, ordinal, message and detail.
These fields can contain private test data and should stay in operator tooling.

Counts come from actual cases, ignoring suite totals, properties and captured
stdout/stderr. `parsed` means XML collection completed, including an empty suite.
`missing`, `invalid`, `limit_exceeded` and `uncollected` contribute to
`report_problems`; totals include parsed reports only. Collection states are
`pending`, `not_run`, `complete` or `incomplete`. `truncated` indicates bounded
failure detail while preserving exact totals. Reports cannot change native job
status: test commands must return nonzero to fail an execution. GitHub Checks
receive aggregate integers only, with their existing native conclusion.

Limits in `pkg/api/limits.go`: two literal paths/job; 1 MiB/report; 5,000 actual
cases; depth 16; 100,000 XML tokens; 64 attributes/element; 20 retained failures;
256-byte names/classes/types, 512-byte messages and 1,024-byte details; 32 KiB
escaped JSON/report. Invalid XML exposes no partial counts. DTDs, XML directives,
non-declaration processing instructions and unsupported encodings are refused.
Over-limit artifacts are checksum-verified without retaining XML in memory.

Reports collect after native stop using the sealed attempt output and existing
artifact checks. Output collection errors for missing provider bytes, changed
checksums or foreign keys remain errors. Normal storage retirement and detailed
history retention apply; archival drops failure details and retains only per-job
report totals. Report reads then return the existing archived-detail response.
No report backfill or command retry is introduced. All production flags remain
off. Upgrade all revision/execution readers before using `junit`; keep admission
off during binary rollback. See [ADR-1582](../adr/1582-internal-ci-junit-reports.md).

### Inspect and recover Check delivery

```sh
gregalectl ci checks list
gregalectl ci checks status --id EXECUTION_UUID
gregalectl ci checks retry --id EXECUTION_UUID --generation 0 --key STABLE_RECOVERY_KEY
```

Use the current `retry_generation` from status for `--generation`. Keep the same
key and generation when repeating a request whose response was lost. The API
returns the same retry receipt and `Idempotency-Replayed: true`. A new key cannot
admit another request while recovery is pending. Keys are account-scoped and
cannot be reused for a different execution or retry generation.

| Method | Path | Result |
|---|---|---|
| GET | `/v1/internal/ci/checks` | Bounded account delivery list |
| GET | `/v1/internal/ci/checks/{execution}` | Delivery and latest retry receipt |
| POST | `/v1/internal/ci/checks/{execution}/retry` | Durable recovery receipt |

POST accepts only `expected_retry_generation` and requires `Idempotency-Key`.
All routes require internal account access, admin scope, MFA, trusted HTTPS and
no-store responses. Reads work with publication and compute off. Retry admission
requires GitHub enablement and both existing Check publication flags; it freshly
verifies account/installation/repository access. Compute admission, worker,
transport and managed storage are not needed for this operation.

Status includes attempts, safe errors, next attempt and lease expiry, desired
and delivered versions, Check ID, retry generation and latest receipt.
`version_pending` includes lease-only changes that need no new remote output.
`not_requested` identifies executions admitted without Check intent; recovery
cannot create it later. `retryable` reports stored-state eligibility, while
request-time authorization and publication flags still apply.

Only exhausted `dead` delivery can be retried. Access-denied `ignored` delivery
remains suppressed. Active publishers, stale generations and pending recovery
return `ci_check_retry_conflict` (409). Reuse of a durable key with a different
execution or generation also conflicts. Three explicit recovery cycles are
allowed per execution for its lifetime; the fourth returns the standard quota
response. Each cycle uses the existing eight-attempt publication budget.

apid records retry intent and a durable audit receipt. githubd applies it to the
existing delivery, preserving execution and Check identity, then reconciles the
latest execution state. `retry_pending` remains true until githubd applies it;
`applied_at` confirms application rather than successful publication. Disabling
publication pauses pending requests. Existing Check identity/version guards
also apply after recovery. Keep all feature flags off outside a deliberately
authorized internal pilot.

Inspect `ci_execution_check_retries` only when deeper internal diagnosis is
needed. Receipts retain hashed request identity and request/application times;
they survive restart and prevent duplicate recovery. Retain them during binary
rollback; migration rollback refuses a nonempty journal. Recovery publishes
the existing result without creating Jobs runs or executing commands. See
[ADR-1577](../adr/1577-internal-ci-check-delivery-recovery.md).

### Inspect and recover skipped-source Checks

```sh
gregalectl ci checks list --binding BINDING_UUID
gregalectl ci checks status --binding BINDING_UUID --event EVENT_UUID
gregalectl ci checks retry --binding BINDING_UUID --event EVENT_UUID --generation 0 --key STABLE_SOURCE_RECOVERY_KEY
```

Use the captured binding/event UUIDs and current feedback `retry_generation`.
These selectors cannot be mixed with execution `--id`. Event history also
includes `check_delivery`, independent of the event's admission retry fields.
The source remains `ignored/paths_filtered` with its original skip decision.

| Method | Path | Result |
|---|---|---|
| GET | `/v1/internal/ci/github/automation/{binding}/checks` | Bounded skipped-source delivery list |
| GET | `/v1/internal/ci/github/automation/{binding}/events/{event}/check` | Feedback status and latest retry receipt |
| POST | `/v1/internal/ci/github/automation/{binding}/events/{event}/check/retry` | Durable source Check recovery receipt |

The same internal authorization, strict retry body, hashed key identity,
publication gates and fresh installation/repository verification apply. Reads
remain available with publication off. Retry admission needs an active account,
but does not need compute admission, a paid compute plan, an enabled current
policy/profile/binding, worker transport or storage. githubd verifies access
again before publishing.

Only an existing transient `dead` delivery exhausted after eight attempts can
be retried. `not_requested` means no feedback was captured; recovery cannot
create it. Successful, active and access-denied deliveries remain ineligible.
Each source allows three lifetime recovery generations, each granting the same
eight-attempt budget. Use the same key and generation after response loss;
different keys conflict while recovery is pending. Keys cannot redirect to
another source or generation.

apid records intent without changing the outbox or source. githubd applies it,
preserving `gregale-ci-source:EVENT_UUID`, the remote Check ID and the immutable
`completed/skipped` projection. `retry_pending` and receipt `applied_at` track
application, not publication success. The `check_retries` health queue includes
pending source requests. Keep `ci_source_check_retries` and source Check
generation evidence during rollback; the migration refuses to discard it.
Do not retry admission to repair feedback. No execution, Jobs run or compute
capability is created. See
[ADR-1586](../adr/1586-internal-ci-source-check-recovery.md).

## Archive terminal execution history

```sh
gregalectl ci execution archive --id EXECUTION_UUID
gregalectl ci execution archives
gregalectl ci execution archive-status --id EXECUTION_UUID
```

Archival frees one of the 32 detailed Jobs execution slots. It retains a compact
receipt under a separate 128-archive/account limit; each receipt is bounded to
16 KiB. Repeating archive after response loss returns the existing receipt and
`Idempotency-Replayed: true`. Archive itself needs no new request key.

| Method | Path | Result |
|---|---|---|
| POST | `/v1/internal/ci/executions/{execution}/archive` | Archive safe terminal detail; accepts empty body or `{}` |
| GET | `/v1/internal/ci/archives` | Bounded account archive list |
| GET | `/v1/internal/ci/archives/{execution}` | Compact terminal receipt |

These routes require internal allowlisting, admin scope, MFA, trusted HTTPS and
no-store responses. Compute, publication, transport and retention flags can be
off when their work has already drained. All shipped feature flags stay off.

An archive requires terminal execution/job state, confirmed task and instance
cleanup, no live runtime transfer, and provider-confirmed payload retirement.
Retention expiry alone is insufficient: pending, ready or deleting objects,
including lost retirement acknowledgements, block the operation. The normal
storage retention worker must finish its work first. Archival makes no provider
call and cannot certify native cleanup.

Captured Checks must be sent at the final execution version or permanently
ignored for lost authority. Pending, sending, dead or outdated sent delivery
blocks archive, as does an unapplied operator retry. Recover delivery through
the Check commands before archiving. An active publisher is rejected promptly
without waiting for its network call. Executions without Check intent can
archive once the other safety gates pass. Unsafe requests return
`ci_archive_not_ready` (409) and retain all detail.

The receipt contains IDs, pipeline/commit, final state, definition digest,
profile/automation identity, times, job states/attempt IDs and safe Check/retry
audit. Detailed snapshots and CI attempt source/output are compacted; stopped
runtime grants are removed. Logs, commands, artifact paths, credentials and
private image keys are excluded. Ordinary Jobs/instance records and the CI
attempt fences that block replay/retry remain under their existing lifecycle.

Execution IDs and admission keys stay reserved, authenticated GitHub event
deduplication is retained, and Check/retry identities survive. Detailed reads,
mutations and admission using an archived key return `ci_execution_archived`
(410), subject to earlier source/profile admission authorization. Use archive
status to read the receipt. No archive operation promotes an old event or
creates another execution.

The archive allowance is permanent in this internal release. At 128 archives,
further archival returns the standard quota response and leaves the detailed
execution intact. Accounts can therefore retain 32 detailed plus 128 compact
Jobs identities. The 256-event journal, revision/profile quotas and permanent
storage-tombstone budgets remain independent limits; archival does not reset
them. Check retry generations remain lifetime bounded, including after archive.
Account deletion removes owned archive receipts; managed storage tombstones
continue to survive. Database rollback refuses archives because compacted
detail cannot be reconstructed. See
[ADR-1578](../adr/1578-internal-ci-execution-archival.md).

## Definition and offline planning

Start with [the example](../../examples/internal-ci/gregale-ci.yaml). Replace
its example image digests with the intended immutable CI images. Definitions
contain executable source text; keep credentials out of them. There is no
secret configuration in version 1.

```sh
gregalectl ci validate --file examples/internal-ci/gregale-ci.yaml
gregalectl ci plan --file examples/internal-ci/gregale-ci.yaml
```

Both commands are local, require no login and never execute the steps or fetch
their images. The plan contains a stable definition digest and topologically
ordered concrete jobs after optional named `matrix` expansion. Each job has a pinned image, named shell steps, optional `needs`,
optional artifact patterns and named dependency `inputs`, literal job/step `env`
and `working_directory`, and `timeout_seconds`
(default 600, maximum 3600).
Steps within a job preserve declaration order. The qualification engine can
advance independent jobs; the metadata API does not dispatch those plans.

The parser rejects unknown fields, duplicate keys, multiple documents, YAML
anchors/aliases, dependency cycles, invalid timeouts, and absolute/traversing
artifact paths. Images must be pinned with `@sha256:`. A repository descriptor
does not grant fetch authority or prove source availability.

## Internal metadata workflow

On a deliberately opted-in internal instance, use the existing
`gregalectl auth` login/session workflow, then:

```sh
gregalectl ci capabilities
gregalectl ci put --name checks --file examples/internal-ci/gregale-ci.yaml
gregalectl ci revisions
gregalectl ci revision --id REVISION_UUID
gregalectl ci run --revision REVISION_UUID --commit FULL_LOWERCASE_COMMIT_SHA --key smoke-001
gregalectl ci runs
gregalectl ci status --id RUN_UUID
gregalectl ci events --id RUN_UUID
gregalectl ci cancel --id RUN_UUID
```

The put response supplies the immutable revision UUID. Reusing its pipeline
name with changed content creates another revision. Admission requires that
explicit revision UUID, a full commit SHA, and a stable request key. Retrying
with the same key and inputs returns the same run; changing inputs returns 409.

Every new receipt is `blocked` with `blocked_reason=compute_unavailable` and
retains its complete planned job graph. Cancellation is idempotent and records
one terminal event. Retrying submission after cancellation returns that
cancelled receipt. Capabilities always report `compute_available=false` and
`dispatch_available=false`.

There are at most 64 revisions and 256 retained runs per account, including
cancelled runs. Exhaustion returns `ci_metadata_limit` with limit and observed
values. Existing receipt/revision recovery still works at the ceiling. This
internal release has no pruning or public retention contract.

## HTTP routes

All paths begin with `/v1/internal/ci`.

| Method | Suffix | Contract |
|---|---|---|
| GET | `/capabilities` | Internal metadata and compute availability. |
| POST | `/validate` | Validate `{ "definition": "YAML_OR_JSON_TEXT" }` without saving. |
| PUT | `/pipelines/{name}` | Save the same definition envelope as an immutable revision. |
| GET | `/pipelines` or `/pipelines/{name}` | List revision metadata. |
| GET | `/revisions/{id}` | Read the exact definition and digest. |
| POST | `/runs` | Submit `{ "revision_id": "UUID", "commit_sha": "SHA" }` with Idempotency-Key. |
| GET | `/runs` or `/runs/{id}` | List receipts or read one complete plan. |
| GET | `/runs/{id}/events` | Ordered blocked/cancelled history. |
| POST | `/runs/{id}/cancel` | Cancel retained intent without allocating compute. |
| PUT | `/github/bindings/{name}` | Verify and configure an account-owned source binding. |
| GET | `/github/bindings` | List the account's retained bindings. |
| DELETE | `/github/bindings/{name}` | Disable a binding and suppress pending trigger/Check work. |

## Internal GitHub workflow

Use the existing GitHub App connection workflow first. Select the exact
account-owned installation and repository IDs from the repository catalogue.
Save a pipeline revision whose `repository` matches that repository, then:

```sh
gregalectl ci github bind --name checks --revision REVISION_UUID --installation INSTALLATION_ID --repository-id REPOSITORY_ID --push-branches main --pr-branches main
gregalectl ci github bindings
gregalectl ci runs
gregalectl ci status --id RUN_UUID
gregalectl ci github unbind --name checks
```

Binding routes additionally require `FAAS_CI_GITHUB_ENABLED=1`. The bind request
contains `revision_id`, `installation_id`, `repository_id`, `push_branches` and
`pull_request_branches`. Branch lists are explicit, exact and case-sensitive;
PR filters refer to the base branch. Omit one list to disable that event type.
At most 16 filters per type, 255 bytes per branch, and 16 retained bindings per
account are admitted. Binding PUT retries are idempotent; changed configuration
advances its generation. Unbind disables rather than deleting history.

CI requires GitHub's App-level webhook secret and the existing durable inbox.
Legacy installation-scoped private senders do not enter CI. A verified event
captures its current binding revision before HTTP acknowledgement. An apid
worker then verifies active account status, durable installation ownership and
live repository access before atomically creating one blocked run when no
eligible automation policy is captured. No source
checkout is performed. `status` detail includes the run's GitHub provenance.

Accepted events are branch pushes and open, non-draft, same-repository PR
`opened`, `reopened`, `synchronize` and `ready_for_review` events, plus `edited`
when the base branch changes. Forks, missing
PR repository identities, closed/draft PRs, tags, deleted refs and zero SHAs are
ignored. Push `after` and PR head SHAs are pinned. PR merge refs are not used.

The same binding, trigger kind, commit, branch/base branch and PR number produce
one receipt, including across changed delivery IDs and restarts. Repeated PR
actions on the same head reuse that event. A revision edit after capture cannot
reroute a retry. Unbind suppresses pending work; re-enabling it does not replay
already suppressed events. Existing blocked receipts remain inert.

There are at most 256 retained trigger snapshots per account, bounded to 64 KiB
each. Trigger and Check workers use 60-second leases, 15-second provider call
budgets, and up to eight attempts with capped backoff. Authority loss suppresses
work; provider/database errors retry. Inspect `ci_github_triggers` and
`ci_github_checks` status and redacted `last_error` codes for internal recovery.
Terminal rows retain quota slots; no pruning or dead-letter redrive interface
is provided in this qualification release.

Blocked-admission Checks require the publication flag in githubd, in addition to every
admission gate. Their names contain the pipeline and binding UUID, with a stable
per-run external ID. They report **action required: compute unavailable**.
They describe blocked admission and do not certify execution success or mirror
later metadata cancellation. Publication retries discover and update the same
external identity after a lost acknowledgement; GitHub's API does not guarantee
exactly-once creation. Disabling a binding suppresses pending publication;
already in-flight requests may finish.

The synthetic acceptance suite exercises signed HTTP ingress, memory/PostgreSQL
admission, access checks and Check response-loss recovery without publishing
to GitHub:

```sh
go test -race ./pkg/ci/... ./pkg/githubd/... ./pkg/apid/ciapi/... -run 'Test(GitHubSources|GitHubEvents|GitHubFlags|CI)'
```

See [ADR-1572](../adr/1572-internal-ci-github-sources.md) for ownership, gate and
delivery contracts.

## Execution engine qualification

`pkg/ci.Engine` reconciles a separate, account-owned `ci_executions` ledger.
Explicit Go qualification admission creates records with mode `qualification`;
there is no endpoint, command, or running worker that starts them. Its test
backend is synthetic. The Jobs adapter below requires separate fresh admission
with mode `jobs` and refuses qualification records.
Existing blocked receipts cannot be passed to the engine or promoted.

Executions progress through `queued`, `running`, `cancelling` and terminal
`succeeded`, `failed`, or `cancelled`. Jobs wait for successful dependencies;
descendants of failed dependencies are skipped. Independent branches continue.
One durable attempt per job retains ordered step progress and its immutable
deadline. Controller recovery uses the same attempt ID, including when a Start
acknowledgement was lost. It does not retry user commands as new attempts.

A 30-second lease, monotonic backend fences and versioned transactional
updates reject stale results. PostgreSQL provides lease time. Cancellation
invalidates in-flight results and cancels every durable start intent, including
ones not yet sent. Cancellation and timeouts await a confirmed backend stop;
an unavailable backend leaves them pending. A crashed controller may require
lease expiry before takeover. Terminal results remain immutable.

| Qualification bound | Value |
|---|---|
| Retained executions/account | 32 |
| Durable snapshot | 4 MiB |
| Retained logs/attempt | 64 KiB prefix, explicit truncation |
| Log page | Up to 16 KiB, byte offsets |
| Artifact manifest/job | Up to 16 entries |
| Artifact size / total per job | 32 MiB / 64 MiB |

Logs are raw bytes, JSON base64 encoded. Resume at `next_offset`; a complete
page at the retained end closes the stream and may also report truncation.
Terminal backend results can drain pending logs without restarting the task.
Failure and cancellation retain terminal artifact metadata. Artifact paths must
match declared segment globs (`**` spans whole path segments); manifests store
only relative paths, SHA-256 digests and sizes. Qualification output is retained
inside the bounded aggregate. The separately gated storage/download routes below
serve only verified Jobs output. Qualification metadata has no pruning or public retention
promise.

Run qualification against the local test database:

```sh
go test -race ./pkg/ci/... -run 'TestExecutionQualification|TestBackendObservation|TestArtifactSelector'
```

The engine suite uses `pgtest` isolation and the complete production migrations.
Confirm its PostgreSQL test ran rather than skipped when recording evidence.
See [ADR-1571](../adr/1571-ci-execution-engine-qualification.md) for the backend
contract and recovery evidence.

## Jobs adapter

`ci.AdmitJobs` and `ci.JobsBackend` provide the Go integration boundary used by the disabled service.
Admission requires the internal, GitHub and dispatch gates, an allowlisted active
account, a freshly verified source binding, and account-owned materialized Jobs
templates declaring the CI runner protocol. Each attempt creates one retry-free,
512 MiB Jobs task with stable identity and immutable source/image/command
snapshots. Existing Jobs capacity admission and VM ownership remain in force.
Blocked receipts and synthetic qualification records never enter this adapter.

The adapter stages a full-SHA GitHub archive through the existing githubd RPC,
checks its returned commit and SHA-256, and stores at most 32 MiB under the
account/execution/attempt prefix. It never extracts archives or runs repository
commands on the host, and never forwards GitHub tokens to a guest.

Cancellation before Start is durable. A source fetch cannot create a late Jobs
run after cancellation, a newer fence, authority/template changes or deadline
expiry. Terminal Jobs tasks remain pending in CI until schedd records the
instance as stopped. Cancel and Observe keep working with dispatch disabled.
An active controller lease may need to expire before cancellation takeover.
Ordinary Jobs replay and task retry cannot re-execute CI tasks with their old
attempt identity.

The final result collector verifies bounded, account-scoped log and artifact
objects, including results from failed/cancelled tasks, before retaining their
metadata. It checks every payload size and SHA-256 and freezes verified output
before paged replay. This contract uses a separate result object rather than the
ordinary Jobs success-only artifact manifest. Negotiated live progress uses
the scoped runtime protocol described below. A stopped task without a guest seal
settles as an explicit platform failure. Corrupt sealed output requires repair.

### Live step progress and log following

```sh
gregalectl ci execution logs --id EXECUTION_UUID --attempt ATTEMPT_UUID --follow
gregalectl ci execution logs --id EXECUTION_UUID --attempt ATTEMPT_UUID --follow --offset NEXT_OFFSET
```

The runner negotiates `progress_enabled` through runtime control, then publishes
ordered steps, optional diagnostic start/finish times and bounded binary log
chunks under its current task capability. The controller reconciles these into
existing status and log pages. New runners retain final-output-only behavior
against older control endpoints.

Offsets count raw bytes; JSON pages use base64. Following validates each page,
retries transient failures from the last emitted cursor, waits at the live end,
and prints `next_offset`, completion and truncation on exit. Ctrl-C stops watching
without cancelling execution. Resume with the printed offset. JSON mode emits
one page per line. Authorization/session errors stop immediately. Existing
internal account/admin/MFA/HTTPS controls apply, including when reading retained
diagnostics with publication and compute disabled.

Progress retains the existing 64 KiB prefix with 16 KiB update pages, a 32 KiB
request limit, one 128 KiB snapshot and at most 4,096 updates per attempt. The
sender publishes every second with a two-second deadline and bounded transport
retries. Sequence, offset and digest checks protect response-loss replay; stale
leases and cancelled/sealed/stopped grants cannot write. Telemetry cannot start
or retry commands. Guest timestamps do not control deadlines or billing.

Completed step progress does not certify a finished job. Final output must
extend the streamed prefix and match known steps, and native stop must be
confirmed. Unsealed cleanup preserves partial diagnostics with
`output_incomplete=true`, then settles as a platform failure or native
cancellation. Inconsistent sealed output requires repair. Archive compaction
clears progress payloads while preserving sequence evidence; rollback refuses
to discard that evidence. Defaults remain off. See
[ADR-1587](../adr/1587-internal-ci-live-progress.md).

The fully migrated PostgreSQL test runs real intent transactions against fake
GitHub bytes, object storage and scheduler/guest receipts:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -race ./pkg/ci/... -run 'TestCIJobs'
go test -race ./pkg/state -run '^TestPgCIJobsCannotReplayThroughJobsAPI$'
```

The guest runner and task-scoped transfer implementation below extends this
boundary. HTTPS transfer and artifact routes are mounted behind a separate off
flag. There is no deployed CI image or automated compute admission. See
[ADR-1573](../adr/1573-dormant-ci-jobs-adapter.md) for the complete contracts and
enablement prerequisites.

## Guest runner and scoped transfers

`make ci-runner-build` builds the static Linux/amd64 guest binary at
`bin/gregale-ci-runner`. The digest-pinned `images/ci-runner.Dockerfile` packages
it with a shell and TLS CA trust. It is outside the image publication matrix.
Extend the image with required test/build tools and pin the resulting platform
manifest digest; this recipe is not a materialized or native-qualified image.

The binary requires the native Jobs marker and exact manifest command. It
downloads the exact archive through one HTTPS origin, validates SHA-256, extracts
inside its workspace root, runs ordered shell steps with an explicit environment,
captures bounded logs, and uploads selected artifacts after success or failure.
Cancellation terminates the current process group and attempts bounded final
publication. GitHub and account tokens never enter this protocol. Portable tests
inject a fake executor and do not execute repository commands on the host.

`ci.IssueJobsRuntime` requires the current native lease and bound Jobs instance.
The disabled scheduler integration calls this host seam after claiming an owned
task. There is no issuing HTTP route. apidserver mounts `ciruntime.Handler`
behind the disabled transport flag. Its source/control/upload/seal routes
require a capability bound to one attempt and
instance; unknown, replaced, expired or stopped native authority is refused.

Source reads require published native runtime identity and stop immediately on
cancellation or policy loss. Existing grants
may finalize output for up to 20 seconds, within the guest supervisor's existing
30-second grace, and cannot read more source or extend execution. A database
lease permits one data transfer per attempt, across API processes. Transfers have
15-second HTTP deadlines and bounded private spooling. Final output is sealed in
PostgreSQL after matching verified object receipts. Retried seals are idempotent;
an uploaded `result.json` cannot publish an execution result.

| Guest/transport bound | Value |
|---|---|
| Compressed source / expanded source | 32 MiB / 128 MiB |
| Source entries / path bytes | 8,192 / 512 |
| Output finalization after cancellation | Up to 20 seconds; immediate revocation on VM stop |
| Active data transfers per attempt | 1, with token-fenced 30-second recovery lease |
| Transfer HTTP deadline / retry attempts | 15 seconds / 3 |
| Control poll / step TERM grace | 1 second / 500 ms |

Use `ci.SealedJobsOutput` for the runtime protocol. It reads the database seal,
verifies the actual object bytes, follows the scheduler's native terminal result,
and still waits for confirmed VM stop. Once a native task has stopped, missing
publication becomes an immutable platform failure with an incomplete-output
diagnostic; partial blobs stay retained for operator reconciliation. Source
links, traversal, duplicate entries, `.git`,
special files, privilege bits, oversized expansion and extra gzip members are
refused. Artifact reads remain inside the workspace, reject links and
special files without blocking, and prune unrelated paths.

Run portable qualification against the isolated test database:

```sh
make test-ci-runtime
make test-ci-scheduler
make test-ci-storage
make images-lock-check
```

This exercises TLS HTTP, full migrations and real intent/grant transactions with
simulated compute and steps. Native two-job and failure/restart acceptance with
`test-metal` and `leakcheck` on a dedicated x86_64 KVM host remains pending, as
does real GCS acceptance. Storage retirement removes grants only after
stop/output reconciliation and payload retirement. Keep all execution flags off until those gates are met.
[ADR-1574](../adr/1574-ci-guest-runner-and-scoped-transfers.md) records the
authority, cancellation and publication contracts.

## Disabled scheduler integration

schedd detects CI ownership before capacity admission and refuses it by default.
Configured launches recheck the account, source binding, pinned template and
immutable Jobs snapshot. The native timeout includes time already spent queued.
Customer `GREGALE_CI_*` variables are removed; only an owned attempt receives
its scoped runtime values. Ordinary Jobs retain their execution path.

CI uses held start: native boot must acknowledge the gate, runtime identity is
published, and current policy is checked again before command release. A grant
staged during cold boot cannot fetch source. Lost release responses retain the
same claimed task. Boot failure and lease expiry cannot retry a CI command.
Failed destroy retains capacity until native orphan reconciliation confirms stop.

`SealedJobsOutput` records an explicit platform failure after confirmed stop when
the guest could not seal a result. Native success alone cannot make that CI run
successful. Partial outputs remain retained without claiming a complete artifact
manifest. Native cancellation remains cancelled. These receipts survive restart.

The scheduler hook is wired with every CI gate off and its origin empty. CI
admission and execution reconciliation workers remain absent. Transfer routes,
private storage and retirement have independent disabled configuration. Do not
enable any gate until the native, provider and operational prerequisites pass. See
[ADR-1651](../adr/1651-disabled-ci-scheduler-boundary.md).

Native qualification is pending in the managed development container: `/dev/kvm`
is absent, and `leakcheck` cannot inspect sysfs loop entries without matching
`/dev/loop*` devices. Run both native gates on the dedicated acceptance host.

## Disabled HTTPS storage and artifact access

With both transport and retention off, apid creates no storage client and
performs no provider discovery or bucket I/O. Turning on either component
requires the PostgreSQL CI store and `FAAS_CI_GCS_BUCKET`. Transport also requires
`FAAS_CI_RUNTIME_URL`; requests must match its host and arrive over TLS or from
the loopback TLS terminator with one `X-Forwarded-Proto: https` value. Remote
forwarding headers cannot authorize plaintext access.

The existing bucket must be dedicated to CI, with public access prevention
enforced, uniform bucket-level access, versioning and soft delete disabled, and
no retention policy or lifecycle rules. Preserve these properties and restrict
object writes to the managed service identity. The service checks them at
startup and before writes/retirement; it does not create a bucket or change IAM.
Use the deployment's existing application default credentials. No credentials
are placed in the guest or in a URL.

| Method | Path | Authority |
|---|---|---|
| GET | `/v1/internal/ci/runtime/{run}/control` | Current attempt/instance capability. |
| GET | `/v1/internal/ci/runtime/{run}/source` | Same, plus live source policy and native running identity. |
| GET | `/v1/internal/ci/runtime/{run}/inputs/{input}` | Current consumer capability and policy; named file from a sealed, stopped successful dependency. |
| PUT | `/v1/internal/ci/runtime/{run}/objects` | Same, bounded reserved log/artifact object. |
| PUT | `/v1/internal/ci/runtime/{run}/result` | Same, database result seal. |
| PUT | `/v1/internal/ci/runtime/{run}/progress` | Same, bounded live diagnostics before sealing. |
| GET | `/v1/internal/ci/executions/{execution}/jobs/{job}/artifacts/{path...}` | Authenticated allowlisted active account, admin scope and existing MFA session. |

Guest routes refuse account bearer tokens, cookies and query credentials.
Operator artifact downloads require a terminal execution and a frozen, verified
output reference.
They verify size and SHA-256 before writing an attachment, recheck ownership and
expiry, and send `no-store`. Guest input downloads require a sealed, stopped
successful producer within the active consumer execution and recheck consumer
authority before returning verified bytes. These routes expose no signed/public
storage URL.

`ci_managed_objects` reserves an immutable descriptor and random physical ID
before publication. Exact source retries reuse the staged bytes; a lost publish
acknowledgement is recovered by verifying the existing object. There is one
source slot per attempt. Physical writes are create-only. Storage accounting
includes pending and deleting objects until confirmed retirement:

| Bound | Account | Global |
|---|---:|---:|
| Charged bytes | 2 GiB | 8 GiB |
| Charged objects | 1,024 | 8,192 |
| Journal rows, including retired objects | 16,384 | 131,072 |
| Physical I/O leases | 2 | 8 |

Leases last 30 seconds; individual I/O calls have 15-second deadlines. New
reservations stop at these ceilings; existing recovery retains its reservations.
Ready payloads expire seven days after publication. Unpublished reservations
become cleanup candidates after two hours. Expired artifacts return 404.

With retention enabled, a 60-second worker considers at most 16 eligible objects
per sweep. Cleanup waits for terminal execution, expired controller/transfer
leases, frozen output and confirmed native stop. Active executions keep recovery
data past expiry. Deleted intent does not remove the storage journal; any live
instance of the owning Job blocks orphan cleanup. Failed/ambiguous retirement
keeps quota charged and retries later. The worker changes no native instance.

Retirement atomically replaces the payload with a permanent empty GCS tombstone,
including when an object is absent. This prevents a delayed upload from
resurrecting bytes after quota is released. Never delete these tombstones or
enable bucket lifecycle rules. Journal rows also remain; their separate ceiling
bounds retained metadata. Runtime grants retire only after output/stop and all
payload retirement. Detailed execution retention remains capped at 32/account;
payload cleanup does not free execution slots. There is no automatic metadata
pruning or public retention promise.

For recovery, inspect journal state, reserved byte counts and lease expiry with
the existing database operator workflow. Allow expired operation leases to be
reclaimed; do not clear active leases or delete reservations to free quota. Fix
provider access/policy failures and rerun the same retirement worker. Retention
can remain enabled while transport/admission are off. Before removing native
history, deleting the journal or rolling back its migration, reconcile every
payload and delayed-writer fence. The migration refuses retained rows, including
retired ones.

Portable storage tests cover quotas, orphan intent deletion, lease takeover,
lost acknowledgements, stop/read barriers and GCS generation fencing through a
simulated endpoint. API qualification also runs with:

```sh
make test-apid RUN='^TestInternalCI' RACE=1
```

Real GCS IAM/policy, resumable uploads and failure/restart qualification remain
pending because this environment has no configured cloud credentials. Native
acceptance and image materialization/lifetime remain before any compute pilot. No image, bucket or fleet configuration
is published here. See
[ADR-1652](../adr/1652-ci-private-storage-and-retention.md).

## Opt-in native acceptance harness

`make native-ci-acceptance` now supplies the isolated end-to-end gate. It needs
a dedicated KVM host, pinned candidate image, trusted guest-reachable HTTPS
origin and qualified private GCS bucket. It runs two dependent jobs, failure,
timeout, cancellation, daemon/controller restart with a lost upload ACK, billing
reconciliation and tenant wake under CI CPU contention. The source repository
is a fixed fixture; real GitHub access and full-host memory-pressure acceptance
remain separate gates. Production CI flags stay off.

The gate retains marker-owned schema/native recovery files after an uncertain
drain and requires provider retirement plus native leak checks before deleting
them. `make test-ci-acceptance` tests its ownership/evidence contract portably;
compilation or synthetic receipts do not count as native acceptance. Follow the
[native CI runbook](ci-native-acceptance.md) for setup, evidence and interruption
recovery. No native or real GCS run has been performed in this workspace.
