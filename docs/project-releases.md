# Atomic project releases

An atomic project release ships every service of a project as one unit. Check readiness before uploading, guard schema changes, and rotate secrets with the release.

## Adding services to an atomic project

Atomic projects can opt into adding independent HTTP source services with
`gregale deploy --project --atomic --add-services --verify --yes`. Run
`gregale deploy check --project --atomic --add-services --verify` first to inspect
prerequisites without reserving apps. This is not enabled for every account yet; contact support to use it.
Every existing serving service must remain present with unchanged settings.
New services pass startup readiness before the graph switches; `--verify` checks
existing critical routes and restores the original membership on regression.
New services have no baseline probe history. Failed or withdrawn additions keep
their app IDs and consume quota but have no default route. An identical retry
can reuse them after the earlier attempt finishes. Removal, dependencies, declared
environment keys, schedules, workers, images, migration commands, configuration
snapshots and progressive rollout are unsupported in this first version.

## Check project deployment readiness

Run a read-only check before uploading source or starting builds:

```sh
gregale deploy check --project --project-slug shop --json
gregale deploy check --project --project-slug shop --atomic --verify --config-file release.json
gregale deploy check --project --project-slug shop --from staging --env production --sync-config --verify --workers
```

The report collects missing runtime keys, invalid dependencies, quota and rollback
retention limits, incompatible release options, worker consumer restrictions,
unavailable verification policy and missing rollout qualification. It uses the
same repository detector and source-selection policy as deployment; use
`--source=worktree` to include local changes. A `--secrets-file` supplies proposed
key names for the check; its values stay on this machine. The command never
submits a deployment, stages credentials, records qualification or executes probes.

CI exits **0** for ready, **1** for blocked and **2** for unknown/unavailable.
Each check includes a stable code, affected workload when applicable and a remedy.
An older server produces unknown and a nonzero exit. Protected targets report that
the final deployment/promotion plan needs approval; this check does not issue,
consume or validate a separate approval credential.

Save `state_hash` and pass `--expected-state HASH` on a subsequent check to detect
changed intent or configuration. Unreadable required state has no comparison hash.
A ready result covers the declared inputs and current control-plane prerequisites;
deployment still validates source bytes, capacity and runtime health.

### Release a push-queue worker with the project

Verified atomic project deploys can opt into one existing push-queue worker with
`gregale deploy --project --atomic --verify --workers`. The first milestone
requires HTTP workloads plus exactly one production worker, one enabled push
binding and one matching consumer. It also accepts `--config-file` and `--migrate`.
The worker starts with its candidate configuration while production queue work
continues to use the previous graph. Startup/configuration proof gates the shared
HTTP/worker cutover; the previous generation then receives bounded graceful
shutdown (default 30 seconds, maximum accepted 60 seconds).

Rollback restores the prior graph and paired variables. Completed jobs and
external or database effects remain. Post-cutover jobs retain normal retry/lease
semantics. This is not enabled for every account yet; contact support to use it.

### Verify real queue health

For real queue health verification, add `--worker-health`:

```sh
gregale deploy --project --atomic --verify --workers --worker-health
```

This requires at least five completed delivery attempts in each candidate verification
window and five baseline attempts in the five minutes before cutover. It compares
failures, retries, processing time, queue wait and due backlog. Low traffic reports
`insufficient_evidence`; it never finalizes a release as healthy. Repeated regressions
restore the retained release, worker admission and paired configuration. Job side
effects remain committed. The existing ten-minute verification deadline applies.
The baseline needs real delivery evidence before opting in. Worker health
verification is not enabled for every account yet.

### Jobs keep their release across cutovers

Named production project worker jobs now persist their release graph at the first
claimed delivery. Retries and operator queue replays retain that graph across
cutovers and gateway restarts. New jobs select the current graph independently.
Expired graphs fail delivery rather than silently move the job to another release;
these pins do not extend the release retention window. Explicit revision pins keep
their existing behavior.
While an unexpired pin owns pending or dispatching work, the scheduler keeps a
separate retained worker pool and can restart a missing worker from that deployment.
It protects the last worker while draining excess replicas, then drains the pool
after the last job finishes. Existing worker/account/concurrency limits still apply;
insufficient capacity delays recovery rather than changing the job's release.

### Progressive canaries

Progressive canaries are available through an explicit opt-in contract:

```sh
gregale deploy --project --atomic --verify --progressive --config-file release.json
# Include a production worker only with real delivery verification:
gregale deploy --project --atomic --verify --progressive --config-file release.json --workers --worker-health
```

The first version requires paired non-secret configuration snapshots. New unpinned
requests and first-delivery queue jobs select a whole graph at 5%, then 25%, then
100%. Each stage needs two fresh healthy rounds; low worker traffic cannot promote
a release. The baseline graph and configuration head remain active until the 100%
transition, when graph, configuration and controller checkpoint commit together.
The candidate graph ID remains stable through promotion and controller restarts.

Regression before activation stops new candidate cohorts; regression afterward
restores the previous graph/configuration together. Existing job and explicit version
pins retain their graph until retention expires. Operator graph/configuration changes
stop the attempt rather than being overwritten. Unknown baseline health requires
operator action; an expired full-traffic checkpoint fails closed for new unassigned
work until recovery. Public receipts show `verification.percent` and `generation`.

Progressive project canaries are not enabled for every account yet; contact
support to use them. Workers additionally need atomic worker releases enabled.

### Inspect or control a progressive attempt

Inspect or control a progressive attempt using its durable submission ID:

```sh
gregale release attempt get ATTEMPT_ID
gregale release attempt cancel ATTEMPT_ID
gregale release recover ATTEMPT_ID
```

`status` shows the traffic stage, generation, blocking reason, latest control
request and available actions. Abort before 100% leaves the baseline graph/config
unchanged. At 100%, abort blocks new unassigned work while fresh baseline health
is checked; qualified restoration commits graph and configuration together.
`recover` retries that verification for a blocked attempt within the retained
graph's lifetime. It cannot force unhealthy or expired baselines, overwrite newer
operator changes, or retarget pinned jobs. Worker restoration still needs real
baseline delivery evidence; the command does not generate synthetic jobs.

Control commands return when their decision is durably accepted. Use `status`
to distinguish pending recovery from completed restoration. For an ambiguous
response, retry with the printed `--request-id ID --generation N`; the same
most recent decision is idempotent.

### Watch for regressions after success

For failures that appear after initial rollout success, opt into stabilization:

```sh
gregale deploy --project --atomic --verify --progressive --stabilize --config-file release.json
```

This keeps monitoring critical HTTP probes and real worker health for ten minutes
after success. Workers still require `--workers --worker-health`. Existing revision
retention must be at least 21 minutes. The deploy command returns on rollout
success; use `gregale release attempt get ATTEMPT_ID` to inspect the continuing watch.
Two confirmed regressions can restore graph/config together while the baseline
remains qualified and this attempt still owns production. Newer production intent
stops the old monitor. Unknown health never authorizes rollback or becomes a
healthy verdict merely because the window ended. Monitoring status and its health
verdict are separate fields.

## Migration compatibility before cutover

New `--migrate` atomic project plans check the current production version again
**after** the expansion command finishes and **before** publishing the candidate.
Configure read-only critical probes that exercise the application's database
paths. Command success alone does not permit cutover.

Missing, unauthenticated or unavailable probe results keep the deployment waiting
within its existing deadline. A complete failed baseline sample stops the attempt
and preserves the current production graph. Inspect the existing task logs and
database state before submitting another migration; schema changes are not
reversed and the completed command is not replayed. The receipt exposes
`release_command.baseline_verification`; `gregale release attempt get
<attempt-id>` also shows this checkpoint.

Passing probes provides evidence for the configured paths, not a proof that every
SQL change is compatible. Keep expansion backward compatible with retained code
and workers. Destructive cleanup is not part of `--migrate`; stabilization does
not by itself prove that old versions and pinned work have drained.

## Guarded schema cleanup

Where schema contracts are enabled for your account, declare both phases
for the expansion owner:

```yaml
release:
  command: node migrate.js expand
  contract_command: node migrate.js contract
```

Deploy using `--project --atomic --verify --migrate --progressive --stabilize
--config-file release.json`. For a multi-workload project, keep one Procfile
`release:` owner and declare only `release.contract_command` in its manifest.
Workers additionally require the existing worker/health flags.

After rollout and stabilization succeed:

```sh
gregale release attempt get ATTEMPT_ID
gregale release contract run ATTEMPT_ID
```

Cleanup waits for expired retained versions and code pins, drained work, and old
instances that stopped or parked. Legacy fallback traffic weights must also select
the current revision at 100%; graph expiry alone does not disable an explicit old
revision that still has a positive traffic weight. Unknown health, uncertain old tasks, newer
production intent or changed runtime inputs block it. The command runs against
the expansion's artifact and runtime fingerprint; credential changes, including
rotation, conservatively require a fresh deployment rather than silently choosing
a different database target.

The CLI prints the durable request ID and generation. After an ambiguous response,
reuse them with `--request-id ID --generation N`; an admitted attempt keeps the
same cleanup task. Use `deploy status` and the existing app-task logs to check its
result. Acceptance is asynchronous. A failed or uncertain cleanup is not replayed,
and retired deployment IDs remain fenced, so they cannot be selected for rollback.
Schema changes are not reversed. Investigate the database before a new attempt.

Schema contracts are not enabled for every account yet. Passing critical probes
checks the configured paths, not every SQL operation or externally managed work.

## Roll forward after failed or uncertain cleanup

Inspect `gregale release attempt get FAILED_ATTEMPT` and its cleanup task logs. Establish
that the original execution has stopped, inspect actual database state, and repair
SQL explicitly if necessary. Keep that private operational evidence outside the
deploy journal. An unknown task exit remains unknown after recovery.

Deploy compatible corrected code as a different atomic verified attempt with
paired configuration (`--project --atomic --verify --config-file repair.json`).
Remove obsolete release declarations, or declare a separately reviewed expansion
when needed. The original cleanup command is not part of this repair deployment.
Wait for verification/stabilization, retire legacy fallback traffic through the
usual revision traffic controls, and allow retained graphs, pins, work and old
instances to drain.

Then record incident closure:

```bash
gregale release contract resolve FAILED_ATTEMPT \
  --replacement-attempt REPAIR_ATTEMPT \
  --database-state-checked --execution-quiescent \
  --evidence-hash SHA256_OF_PRIVATE_EVIDENCE
```

The CLI binds the inspected task and cleanup generation and prints a durable
request ID. Reuse `--request-id` and the same identities/attestations after an
ambiguous response. State and configured critical-route checks must still match
before first acceptance. Existing accepted resolutions replay after newer deploys.
Retirement fences extend to drained non-current code, while the original cleanup
keeps its failed or uncertain outcome. `deploy status` shows the resolution and
replacement attempt. A missing cleanup receipt cannot be overridden with force.

This action remains available for existing incidents when new cleanup admission
is disabled. Database and execution checks are explicit operator attestations;
configured HTTP probes do not establish arbitrary SQL compatibility or prove
that an unobserved external execution has stopped.

At full traffic, a new `abort` or `recover` decision requires an unexpired retained
baseline. If retention has ended, the server returns HTTP 410 with
`project_release_baseline_expired`; deploy a compatible replacement. Rejection
preserves the current graph, configuration and existing control decision. Replaying
an already accepted request still returns its historical decision.

## Atomic application secret rotation

Encrypted production secret snapshots are opt-in, and not enabled for every
account yet:

```sh
gregale deploy --project --atomic --verify --yes \
  --config-file release.json --release-secrets-file credentials.json
```

`credentials.json` maps existing workload names to complete secret value sets:

```json
{"frontend":{"API_KEY":"replacement-value"}}
```

Use existing application-owned production keys. Omitted workloads inherit their
prior secret snapshot; an empty workload map removes its serving secrets. The
first version supports HTTP workloads with persistent secrets and excludes managed
credentials, workers, migrations, sidecars, explicit secret grants and reload hooks.
The CLI excludes this file from source packing and sends values separately over
TLS. APID encrypts them before storage and returns version IDs, which are then
bound into the scan/apply plan. The candidate uses its proposed secrets while the
old release keeps its earlier versions. Cutover and rollback select code, variables
and immutable secret references through the same project graph.

If staging loses its response, reuse the printed ID with
`--release-secrets-request-id ID` and the same file contents within 24 hours.
Staging is limited to 32 live bundles per project and the shared 64 KiB document
bound. Public receipts and local sessions contain version IDs and fingerprints,
never secret values or encrypted envelopes. JSON receipts show candidate references
under `configuration.secrets.versions` and rollback references under
`configuration.secrets.previous_versions`.

Once pinned, production secret edits through `secrets set`, deletion or resealing
are rejected; use another atomic secret release. Keep both external credentials
valid while rollback remains possible. Host-key rotation of pinned applications
requires a future snapshot-aware rekey workflow; retain the old host identities.
The secret registry remains the legacy source, while release receipts identify
which versions a deployment actually uses.

Contact support to enable release secret snapshots.
