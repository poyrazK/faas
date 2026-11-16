# Environment promotion

Promote what you tested: move an artifact and its pinned configuration from one environment (such as staging) to the next, with health checks, migrations and queue workers handled together.

## Promote a tested HTTP configuration snapshot

Use the existing environment promotion command to transfer a tested artifact and
its pinned non-secret variables together:

```sh
gregale environment promote my-project --from staging --to production --sync-config --yes --wait
```

Both environments must have complete active HTTP release graphs. The preview
lists changed variable keys and binds the exact configuration versions. Production
keeps its own secrets, secret version pins, authentication credentials, service
bindings and infrastructure assignments. An explicitly empty source variable set
clears the candidate's variables. Promotion reuses the source artifact without a
new build, and protected targets still require source qualification and approval.

A change after preview requires a fresh preview. Once published, verification
checks the graph, artifact and captured configuration identities. Its automatic
rollback restores the previous graph and pinned variables with the previous
configuration document. Later production edits block rollback from overwriting
them. Interrupted requests resume from the stored capture using the same
idempotency key. Secret envelope rotation preserves the approved identity.

Workers, jobs, migrations, sidecars, reload hooks and owned work/queue policies
are not transferred with the snapshot. Generated service URL variables
require the opt-in verified service graph mode described below. Environment-owned
bindings remain at the destination.

### Verify destination health during environment promotion

Health-verified promotion of HTTP configuration snapshots is not enabled for
every account yet; contact support to use it.
Configure read-only GET/HEAD route-health probes for every workload first.
Both environments need active complete HTTP graphs, pinned configuration and
at least 21 minutes of revision retention; at most five project probes apply.

```bash
gregale environment preview shop --from staging --to production --sync-config --verify
gregale environment promote shop --from staging --to production --sync-config --verify --yes --wait --progress --timeout 25m
```

The server returns an accepted running operation. Two complete healthy rounds,
one minute apart, are required while candidates serve no normal traffic. Graph
and configuration then cut over atomically, followed by two new healthy rounds
before success. Probe requests preserve destination scope and pass only Gregale's own auth gates; your app's own auth still applies.
Use a dependency-checking application endpoint to test database access.

Inspect `gregale environment status shop PROMOTION_ID --to production`
for phase, verdict, deadline and bounded probe counts in JSON. Missing results,
authentication failures and unexpected HTTP responses remain unknown health.
Preflight has a ten-minute deadline and leaves the previous release active on
failure. Repeated post-cutover regressions with a healthy retained baseline
restore its graph, configuration and runtime variables. If baseline health cannot
be confirmed, or newer destination intent conflicts, status requires operator
action. A client timeout does not stop server processing; retries and daemon
restarts retain deadlines and checkpoints.

### Promote a complete HTTP service graph

Where complete-graph promotion is enabled for your account, use the same
`--sync-config --verify` command. Every declared destination dependency must belong to the complete project
HTTP graph, and required source binding keys must exist at the destination.
The server regenerates platform service URLs using the destination assignments
and transport; production keeps its own secrets and service caller grants.

Preview reports `destination_config_hash` and changed variable keys. The source
and destination input document diff remains visible separately. The resolved
document and runtime pins publish together. Configure a read-only frontend probe
that actually calls the API and validates its response. Private candidate callers
route to candidate peers before cutover; retained baseline callers keep their
previous peers afterward. A failing candidate API prevents cutover or triggers
the existing health rollback.

### Promote a release with a database expansion

Where migration promotion is enabled for your account, preview and submit
with:

```sh
gregale environment preview shop --from staging --to production --sync-config --verify --migrate
gregale environment promote shop --from staging --to production --sync-config --verify --migrate --yes --wait --timeout 35m
```

`--migrate` explicitly asserts that the source release's single declared
`release_command` is backward-compatible with the destination's current code.
Both environments must have complete pinned HTTP release graphs. Service graphs
also require the service graph admission switch. The command reuses the approved
source artifact with destination variables, secrets, migration credentials and
bindings. Source migration success does not authorize destination cutover.

The server first prepares dark candidates, then admits one durable release task.
The task has one minute from admission to start and at most ten minutes to run.
It must return an owned zero exit receipt before candidate and previous-version
HTTP checks begin. Existing health checks gate atomic graph/config cutover and
post-cutover success. A process restart resumes the same task; commands dispatched
without a confirmed exit become uncertain and are never replayed automatically.
Status includes the migration task ID, outcome and exit code. A destination input
change invalidates dispatch or cutover; preview again after resolving the change.

Migration commands can commit partial database changes even if they fail.
Rollback restores code and configuration; it leaves the schema expansion in
place. Destructive schema cleanup and multiple migration owners are unsupported.
For an uncertain outcome, inspect task logs and the destination database before
recovering it. New migration promotions are blocked while that outcome is
unresolved.

### Recover an interrupted promotion migration

Inspect the original task and destination database first. Keep private inspection
notes and supply their SHA256; Gregale does not upload those notes or run repair
SQL. Read the current health generation with `projects environments status`.

If the expected expansion is present and compatible with both releases:

```sh
gregale environment migration recover shop PROMOTION_ID --to production \
  --action resume --task-id TASK_ID --generation GENERATION \
  --database-state-checked --schema-compatible --evidence-sha256 EVIDENCE_SHA256 --wait
```

Recovery first requests scheduler teardown of the original VM. After its confirmed
acknowledgement, the server rechecks the original configuration/runtime pins and
starts a fresh health preflight. `--wait` waits for recovery acknowledgement;
`resumed` means promotion health continues, rather than that cutover has finished.
The original migration task remains failed or uncertain and is never replayed.

If repair code/configuration is required, publish a different complete snapshot
release without another migration, then complete a normal verified promotion into
the same destination. Close the old incident against that repair promotion:

```sh
gregale environment migration recover shop PROMOTION_ID --to production \
  --action resolve --replacement-promotion REPAIR_PROMOTION_ID \
  --task-id TASK_ID --generation GENERATION --database-state-checked \
  --evidence-sha256 EVIDENCE_SHA256 --wait
```

The repair promotion must already be succeeded, verified and currently active.
Fresh server-owned probes and runtime/configuration guards run again before
resolution. A repair can include the supported worker using `--workers` without
`--migrate`. The server rechecks current worker startup and consumer configuration,
and compares deliveries by that replacement before and after the recovery request.
Both windows need sufficient complete, healthy attempts; low traffic leaves recovery
verifying until its deadline. The old promotion remains failed, with a separate resolved receipt.
A matching `--request-id` retries the same decision; when omitted, the CLI derives
a stable ID from the request. Changed attestations with the same ID conflict.
Blocked attempts can use a new request and the current health generation.

An unreachable executor or missing runtime registration cannot be acknowledged as
stopped. Recovery waits until teardown is confirmed and expires safely if it
cannot be. There is no force option or automatic SQL retry.

### Promote APIs and queue workers together

Where worker promotion is enabled for your account, promote a complete pinned release containing HTTP services
and existing production push-queue workers:

```sh
gregale environment preview shop --from staging --to production --sync-config --verify --workers
gregale environment promote shop --from staging --to production --sync-config --verify --workers --yes --wait
```

Each production worker keeps its queue binding, consumer policy and credentials.
Its source artifact and non-secret variables follow the tested release. A source
worker without a variable snapshot needs a live runtime proof matching its current
variables and credentials. Each source and previous worker pair needs matching
worker execution settings and stop grace.
Verified environment promotion supports up to eight push workers, each with one
production queue consumer. Account worker-replica quotas still apply to the
retained, candidate and any running staging pools. All workers must pass startup
and baseline checks;
post-cutover delivery health is tracked independently. A healthy subset cannot
complete the release. A qualified regression restores the entire application after
all retained workers are ready. Existing jobs keep their original version pins.
Multiple consumers per worker, pull/job workers and sidecars remain unsupported.
Promoting several workers at once, and combined `--migrate --workers`
promotions, are not available yet.
Migration-incident resolution requires a verified repair promotion without a
migration, with fresh startup and delivery evidence for each replacement worker.

Candidates start without receiving production queue jobs. Cutover requires
every worker's configuration/startup proof, healthy baseline job evidence and the configured
HTTP probes. Graph/configuration and new-job admission switch together. Existing
jobs retain their original release through retries; retained workers remain
available for eligible outstanding pins within the normal retention limits.

After cutover, real worker delivery health and HTTP probes must agree before
success. Low queue traffic remains unknown and does not count as healthy. Status
includes worker readiness, job sample counts, backlog and verdict. A regression
can restore the captured graph/configuration only after every previous worker has
current startup proof. Changed bindings or newer production intent require
operator action. If an explicit rollback is waiting for worker startup, retry
the same rollback request once all retained workers are ready. Completed jobs and
other external effects are never undone.

Worker promotion is not enabled for every account yet; contact support to use
it.

### Abort or recover an environment promotion

Inspect `gregale environment status PROJECT PROMOTION --to ENV` for the
health generation and available operator action. For a health-gated HTTP promotion,
including supported production push workers:

```bash
gregale environment cancel shop PROMOTION_ID --to production --generation 7 --wait
# After a blocked restoration and dependency repair, inspect status again:
gregale environment recover shop PROMOTION_ID --to production --generation 12 --wait
```

Before cutover, abort keeps the destination graph/configuration selected, retires
owned unused candidates, and lets the scheduler drain their instances. After
cutover, abort requests fresh baseline-only checks before restoring the captured
graph/configuration and worker selection together. Two minute-spaced healthy HTTP
rounds and, for workers, current retained-worker startup proof and healthy real
job evidence are required. Unknown health and quiet workers cannot authorize a
switch. Existing jobs retain their first-admission deployment pins.

Each decision has a ten-minute server deadline. A blocked decision may be retried
with `recover` after inspecting status and fixing its cause. Recovery uses the
original baseline and artifacts; it does not extend retention or overwrite newer
destination intent. Migration incidents use `recover-migration` instead.

The CLI derives a stable request ID from the inspected generation, or accepts
`--request-id` with 32 lowercase hexadecimal characters. Replaying the same request
returns its durable receipt. A changed generation/action with that ID conflicts.
A local wait timeout leaves the server decision running. The API exposes POST
`.../promotions/{promotion}/abort` and `/recover`; Go, Node and Python SDKs include
the same request and receipt contract.

Promotion health checks now use durable due-time scheduling. Four reconciliation
slots are shared by updated APID instances, with at most two per account. A slot
prefers rollback and recovery while the others serve the oldest due work. Slow
checks can no longer keep every later promotion behind the same 20-record batch.
APID restarts retain pending work; abandoned claims become eligible after their
one-minute lease expires. Required sample intervals, health deadlines and release
retention remain unchanged. Existing scheduler quotas still govern every VM wake.

## Promotion admission

Verified environment promotions start immediately when capacity is available.
An account can run up to eight active promotions, within a platform-wide limit.
Excess requests wait durably with `status=running` and `health.phase=queued`;
the CLI displays the reason and wait deadline. Waiting lasts at most 15 minutes,
with up to 32 queued requests per account. These limits do not increase your application's resource quotas.

The status endpoint includes `health.admission.queued_at`, `wait_deadline`,
`reason`, and, once dispatched, `started_at`. Before dispatch, the health
checkpoint's timestamps describe waiting. Dispatch starts a fresh health window
only after checking that the approved source, destination and configuration are
still current and retained. Changed inputs finish as conflicted; preview and
approve again. Expired waiting requests fail without starting workloads.

A full queue returns HTTP 429 with code `project_promotion_queue_full` and
`Retry-After: 5`. No promotion was accepted and the approval remains available.
Retry with the same idempotency key and approval while the preview is still valid.
Previously accepted keys continue to return their original operation.

Use the existing abort command with the current generation to cancel waiting,
including a queued migration. A local CLI wait timeout leaves the durable operation
running; use the printed status command to resume observing it. Rollback and
recovery continue while new starts wait.

### Promote artifacts with production secret changes

For a complete HTTP project, stage a destination-only secret rotation while
promoting the tested artifacts:

```sh
gregale environment promote shop \
  --from staging --to production --sync-config --verify --yes \
  --release-secrets-file production.json --wait
```

`production.json` maps workload names to complete proposed secret maps, for
example `{"api":{"TOKEN":"new-production-credential"}}`. Omitted workloads
inherit production secrets; an empty workload map removes its serving secrets.
Only existing application-owned persistent production secret keys are supported.
Migrations, workers, managed credentials, sidecars, explicit grants and reload
hooks are outside this mode.

Values go only to encrypted secret staging. Preview and approval bind immutable
version references and show secret key changes. Use the printed request ID with
`--release-secrets-request-id ID` to replay staging within 24 hours. Candidates
receive the proposed production versions, and rollback restores the previous
code, configuration and secret versions. Keep old external credentials valid for
the rollback window. Staging credentials are never copied to production.

Promoting with production secret changes is not available yet.
