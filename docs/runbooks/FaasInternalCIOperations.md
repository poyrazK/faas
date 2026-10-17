# Internal CI operational warnings

Gregale CI remains internal and disabled by default. This runbook covers
`FaasCIHealthUnavailable`, `FaasCIWorkerUnavailable`,
`FaasCILeaseRecoveryStalled`, `FaasCICheckDeliveryDead` and
`FaasCIQuotaPressure`. All are internal warnings. Native qualification and an
authorized internal pilot are separate from these observations.

Open the private Grafana dashboard `/d/faas-internal-ci` and inspect the read
status and configured workers first. When the account has internal opt-in,
admin authentication, the existing MFA policy and a trusted HTTPS origin, use:

```sh
gregalectl ci health
gregalectl ci checks list
gregalectl ci execution list
```

The CLI reports account-owned work and quotas. Grafana shows anonymous fleet
aggregates; per-account history limits are not global limits. Health and status
reads do not need compute, Check publication or storage transport enabled.
Detailed commands and release gates are in [the internal CI guide](../ops/internal-ci.md).

## Read the evidence

For a running execution, inspect its current step states and follow logs:

```sh
gregalectl ci execution status --id EXECUTION_UUID
gregalectl ci execution logs --id EXECUTION_UUID --attempt ATTEMPT_UUID --follow
```

Ctrl-C stops watching without cancelling execution. Resume with the printed
`next_offset` using `--offset NEXT_OFFSET`. Cursors count bytes, including binary
output. Truncation means the 64 KiB retained prefix is full; it does not certify
native completion. Progress depends on a current negotiated runner/runtime
grant, while retained log reads work with compute disabled. Step times are
diagnostic, not billing or lease authority. A stopped task without a seal reports
`output_incomplete=true` and preserves partial bytes without claiming success.
Keep sequence evidence during rollback; archive clears raw progress payloads.
See [ADR-1587](../adr/1587-internal-ci-live-progress.md). Qualify the updated pinned
runner image separately on the native host; defaults and release remain paused.

- Disabled means a fresh owner configuration says the path is off. Paused
  backlogs remain visible and their work-specific warnings stay quiet.
- Unknown means the scrape, declaration, recording group or worker evidence is
  missing. It must not be interpreted as a healthy empty queue.
- Unavailable means monitoring is configured on but no replica of that daemon
  can read durable health. If all fleet reads fail, dependent counts disappear.
- Starting and Healthy mean at least one matching group is available. Check
  per-condition counts for other blocked/degraded/stalled/stale groups.
- Heartbeats become stale after 45 seconds; fresh heartbeats without cycle
  progress for 120 seconds become stalled. The underlying diagnostic slots are
  bounded and fenced; they are not a complete census of daemon processes.

Raw fleet gauges repeat on every apid/githubd replica. Use the dashboard's
`faas_ci:*` recording rules, which discard unreadable targets, use max for
counts/usage and min for limits, and compute conservative headroom. Do not sum
replicas or per-condition maxima into a process total. Check ordinary daemon
scrape alerts for a lost target: its CI configuration is unknown until observed
again. A healthy second replica can retain fleet visibility.

## Dependency artifact inputs

For a job with `inputs`, inspect its explicit `needs`, producer status and selected
artifact path. A usable producer is successful with sealed, verified output and
confirmed native stop. The consuming manifest pins its producer attempt, digest
and length within the same account/execution. Missing inputs fail before native
admission; guest transfer or checksum failures skip all commands and publish a
failed preparation result. Existing logs/status show the failure.

Input files are private copies under `.gregale-ci-inputs/NAME`. A source collision
with this reserved directory is a preparation failure. Names/paths, file counts
and total bytes are bounded; inputs are not extracted archives or executable by
default. Never repair an input by rewriting an existing producer object or
changing its frozen descriptor. Submit a fresh execution after fixing the
producer declaration/output. A rerun resolves its own fresh producer attempts.

Existing retention protects producer bytes through active consumers, including
normal TTL expiry. After confirmed consumer stop and terminal execution, normal
cleanup resumes. Input jobs need qualified manifest-v2 runner images (v3 with
environment/directory configuration) and current
control-plane binaries. Drain them before a binary downgrade. Keep flags off;
native/provider validation and release remain separate. See
[ADR-1588](../adr/1588-internal-ci-dependency-artifact-inputs.md).

## Matrix planning or variant execution failure

Inspect `gregalectl ci plan --file PIPELINE_FILE` before admission. Matrices allow
one to eight named variants/job and at most sixteen concrete jobs total. Resolve
invalid/overlong/colliding `JOB-VARIANT` names, cycles and overlapping dependencies
in the definition. No compute is admitted by planning. Profile/execution template
maps must use concrete names and match each resolved variant image.

Use a concrete job's attempt ID for logs/progress and concrete job name for
artifacts/reports. `needs: [test]` waits for every variant to finish native cleanup;
`needs: [test-beta]` waits for one. A failed variant skips its dependents while
siblings continue. Artifact inputs explicitly name the concrete producer and
remain bound to that producer's sealed attempt. Never repair an input or a variant
by editing frozen metadata, moving another variant's blob, or replaying a native
CI run. Submit a fresh corrected execution or use the existing whole-pipeline rerun.

Native qualification must verify all pinned variant images, resource admission,
restart, cancellation and cleanup. Definition readers must understand matrices
before fresh admission; drain with current binaries and keep admission off during
binary rollback. Flags remain off and release stays paused. See
[ADR-1590](../adr/1590-internal-ci-named-matrix-jobs.md).

## Step environment or working-directory failure

Inspect the immutable revision/execution plan's job defaults and step overrides.
Environment values are non-secret literal metadata; empty strings override job
values, and each step gets a fresh explicit environment. Runner/runtime settings
cannot be overridden. Quote YAML numbers/booleans and observe the declared and
merged bounds documented in
[ADR-1589](../adr/1589-internal-ci-step-configuration.md).

Directories are checkout-relative and must exist when their step starts. An
earlier step with `working_directory: .` can create a later directory. Missing
directories, links and special files fail the affected step with exit 125 and
skip subsequent steps; the log contains a fixed configuration-preparation
diagnostic. Submit corrected source/configuration as a fresh execution rather
than changing a frozen command. Artifacts, JUnit paths and inputs remain relative
to the checkout root, available to commands as `$GREGALE_CI_WORKSPACE`.

Configured jobs require a qualified manifest-v3 runner image and current readers.
Drain these executions before binary downgrade and keep admission off during
rollback. Native qualification must check descriptor-based chdir, cancellation,
environment isolation and confirmed native cleanup. All feature flags remain off
and release remains paused.

## Health query unavailable

`FaasCIHealthUnavailable` waits five minutes. Inspect PostgreSQL reachability,
pool health and the thirteen CI migrations through the normal release process.
Read the responding daemon's configuration and private metrics. A health read
error omits dependent evidence; it does not turn queue depth into zero.

If another readable replica exists for the same daemon, its fleet read can
suppress this warning. Investigate individual unhealthy targets through their
ordinary daemon and database diagnostics. Keep connection strings, provider
URLs, credentials and raw payloads out of incident notes.

## Enabled worker unavailable

`FaasCIWorkerUnavailable` waits five minutes while durable health is readable.
Inspect the worker's current configuration and its observed conditions:

- Blocked: check its wired dependencies. Dispatcher/retention need managed
  storage; source admission needs GitHub wiring; publication needs the App
  service and Check writer. This does not justify enabling compute gates.
- Degraded: inspect safe queue failure codes and normal daemon diagnostics.
  Worker observations sample the latest completed decision; they are not a
  per-attempt failure history.
- Stalled: the process can pulse but has not reported progress within the
  existing bound. Inspect bounded RPCs, database contention and loop lifetime.
- Stale or Unknown: inspect process lifetime, startup wiring and scrape data.
  There are eight diagnostic slots per kind. Additional groups can reconcile
  without a slot, so check slot exhaustion before inferring that all work stopped.

A configured-off worker should stay off until the authorized rollout or drain
procedure calls for it. Restarting a worker preserves execution/attempt identity;
never replay work to repair its diagnostic observation.

## Expired lease recovery

`FaasCILeaseRecoveryStalled` waits five minutes with the owning worker enabled.
Queue ownership is: executions → dispatcher, metadata admission → metadata
admission, automatic admission → automatic admission, metadata/execution Checks
→ their corresponding publishers, retirement → retention. Unapplied Check retry
intent is displayed under its execution publisher but has no separate work lease.

Inspect worker health, next-attempt/backoff state, retry budgets and native task
state. Expired means time eligible; authority, active-work limits, stopped-VM
receipts, output settlement and retirement checks still apply. Preserve execution
fences and immutable attempts. Do not clear lease columns, delete Jobs rows or
force object retirement to silence a warning.

## Dead Check delivery

`FaasCICheckDeliveryDead` waits five minutes while publication is configured on.
Inspect safe delivery status and current GitHub repository/installation authority:

```sh
gregalectl ci checks status --id EXECUTION_UUID
```

Fix access or provider availability first. For an eligible exhausted execution
Check, use its current retry generation and a stable new request key:

```sh
gregalectl ci checks retry --id EXECUTION_UUID --generation CURRENT_GENERATION --key STABLE_RETRY_KEY
```

The existing API rechecks authority and records bounded audited retry intent;
githubd consumes that intent. Applied does not mean published. An ignored or
revoked delivery cannot be forced through retry. Metadata Check failures use
their existing binding/source diagnostics; the execution retry API does not
rearm metadata delivery. Never submit another execution to repair publication.

## Dead automatic admission

Inspect the original authenticated source event and resolve capacity or provider
availability before requesting recovery:

```sh
gregalectl ci automation status --binding BINDING_UUID --event EVENT_UUID
gregalectl ci automation retry --binding BINDING_UUID --event EVENT_UUID --generation CURRENT_RETRY_GENERATION --key STABLE_RECOVERY_KEY
```

Use the current generation and repeat the same key after a lost response.
Recovery is limited to three lifetime budget resets for a never-admitted dead
event. It revalidates account, repository, policy, binding, profile and image
authority. Ignored/revoked events and old metadata receipts cannot be revived.
A 202 receipt means admission is pending; it does not mean an execution started.
The worker retains all normal execution gates and quotas. If the source has an
execution ID, inspect that existing execution rather than recovering admission.
Never clear attempt counters, edit generations or remove recovery receipts.

## Explicit execution reruns

Use `gregalectl ci execution rerun --id EXECUTION_UUID --key STABLE_RERUN_KEY`
after the original Jobs execution is terminal and its native/output/transfer
receipts confirm cleanup. Inspect the returned child's status, logs and reports
using its new ID. `rerun.parent_execution_id` and `rerun.root_execution_id` link
its history to the original result. This starts the full frozen pipeline at the
original commit once normal compute admission has been deliberately enabled.

Retain the same key when a response is lost; a replay returns the original
child without provider calls or new work. New keys request distinct executions
and consume normal quotas. `ci_rerun_not_ready` requires completion of existing
work or cleanup. Repository/profile/binding/image conflicts require a fresh
validated profile submission. Do not clear request identities or weaken native
receipt checks. An archived child keeps its key reserved; an archived parent
cannot supply fresh rerun configuration. Existing receipt replay can still
resolve a retained child after parent archival. Repeated native commands never
reuse a task attempt, runtime grant or artifact prefix.

For native qualification, retain a completed fixture's execution, task, output
and Check identities. Rerun it twice with the same key and confirm one child,
the same commit/plan, new first-attempt task identities and independently
published output and Checks. A deterministic failing fixture must fail again;
rerun cannot promote its native result. Confirm ordinary cancellation/cleanup
and retained parent/root lineage through archival. Inspect capacity and cleanup
evidence through the existing native qualification workflow.

All production flags stay off and release stays paused. Native/provider
qualification remains separate from portable rerun tests.

## Automation path filter decisions

Inspect `gregalectl ci automation events --binding BINDING_UUID` or
`ci automation status --binding BINDING_UUID --event EVENT_UUID`. A settled
`path_decision` carries exact base/head/merge-base SHAs and a file count.
`paths_filtered` is an intentional skip with no execution; `paths_matched`
runs normally; `changes_unavailable` runs conservatively. Missing before/base
pins, new branches, divergent pushes, provider outages and comparison caps
must never appear as successful skips. Recovery cannot promote filtered sources.
Manual submissions and execution reruns are independent of automation filters.

Check the saved `include_paths` / `exclude_paths` arrays before changing policy.
An empty include list includes all paths; exclusions take precedence per path.
Both rename/copy names and deletions count. Policy replacements revoke pending
sources and do not re-evaluate settled sources. A filtered push preserves older
code executions; a cumulative filtered PR follows normal supersession/drain.

With Check capture enabled, expect a terminal `skipped` Check with external ID
`gregale-ci-source:EVENT_UUID`. It is source feedback, without an execution.
The existing `execution_checks` health queue includes pending/dead source
feedback. The same worker retries delivery up to eight times; provider failures
do not alter the source decision. Inspect and recover exhausted feedback with:

```sh
gregalectl ci checks status --binding BINDING_UUID --event EVENT_UUID
gregalectl ci checks retry --binding BINDING_UUID --event EVENT_UUID --generation CURRENT_FEEDBACK_GENERATION --key STABLE_SOURCE_RECOVERY_KEY
```

Use feedback `retry_generation`, and keep the same key/generation after response
loss. Only transient `dead` feedback exhausted after eight attempts is eligible;
three lifetime operator generations are available per source. `not_requested`,
active, successful and access-denied feedback cannot be retried. The request
requires existing Check publication gates and fresh account/repository access,
with compute disabled. githubd applies the intent, rechecks access and publishes
the original skipped result with the same remote identity. Pending source retry
intents appear in `check_retries`; event history includes `check_delivery`.
Preserve the retry journal during rollback. Admission recovery cannot promote
the source. See [ADR-1586](../adr/1586-internal-ci-source-check-recovery.md).

Native verification handoff: after deliberate internal opt-in, verify a
docs-only push skips without creating a Jobs run, a code-path rename/deletion
runs, an unavailable/truncated comparison runs, and an excluded PR head drains
the prior automatic execution before its skip. Confirm a docs-only push leaves
older code work running, manual reruns ignore filters, and GitHub reports
`skipped` with the source identity. Check response-loss/restart recovery and
account isolation. Keep all production gates off and the release paused until
that separate qualification is complete. See
[ADR-1585](../adr/1585-internal-ci-automation-path-filters.md).

## Pull-request automation

Inspect `gregalectl ci automation events --binding BINDING_UUID`, then source
status by event ID. `kind=pull_request` and `pr_number` identify the PR; `branch`
is its captured base branch. PR filters must be explicitly set in both binding
and policy. Existing push-only configuration admits no PR execution.

`pr_wait` means an observation changed or previous work still owns the PR slot;
it does not consume the admission failure budget. Check the older execution's
cancellation, native-stop, collected-output and transfer evidence. Manual work,
other PRs and prior policy generations are not automatically cancelled. A prior
generation still holding this PR's slot needs completion or explicit cancellation.

`pr_closed`, `pr_draft` and `pr_changed` permanently ignore only that candidate.
`head_changed` records a freshly observed replacement head. Provider failures,
HTTP 404 and incomplete identity remain availability failures and never cancel
other work. Check the App's `Pull requests:read` permission and `pull_request`
webhook delivery when diagnosing provider access or missing sources.
Resolve the provider/authority issue before explicit bounded source
recovery; its worker verifies current PR state again. Do not replay settled
webhooks or clear source/fence journals. Use a fresh explicit manual submission
when an already settled exact logical source must run again.

GitHub state can change after an RPC. Already-admitted executions keep their
pinned SHA when a PR later closes; use the existing cancellation command if
needed. All production flags remain off, and native/provider qualification stays
with the verification workflow. See [ADR-1583](../adr/1583-internal-ci-pull-request-automation.md).

## Test report diagnostics

Read `gregalectl ci execution reports --id EXECUTION_UUID` for configured JUnit
reports. This internal admin/MFA/HTTPS read needs no compute or publication
gate and makes no provider call. Check execution, commit, attempt and artifact
digest before comparing runs. Test names/messages/details are private output;
GitHub receives aggregate counts only.

`missing` means the selected artifact was absent from the verified terminal
manifest. `invalid` means unsupported or malformed XML; `limit_exceeded` means
a parser bound was exceeded. `uncollected` means a retained artifact has no
report projection, including legacy output. Totals cover parsed reports only;
pending or never-run jobs have no invented test counts. `truncated` limits detail,
not exact counts. Fix report generation/selection in a fresh pipeline revision
and make a deliberate fresh submission when needed. Reports never rerun commands
or change native status; test commands must return nonzero to fail the job.

If execution output collection is failing, investigate the existing storage,
checksum, ownership and native-stop evidence. Those errors are distinct from
accepted report diagnostics. Do not bypass sealed output or checksum checks.
Archival removes detailed failures and retains aggregate report history; a gone
report response is expected afterward. All feature flags remain off until the
existing native/provider qualification and internal pilot authorization.

## Sustained quota pressure

`FaasCIQuotaPressure` waits ten minutes at or above 80 percent of an existing
global allowance. Execution/dispatch capacity requires the dispatcher on.
Storage pressure requires the dispatcher or retention on; intentional pauses
keep the work-specific warning quiet while retained usage remains visible.

Inspect active execution, lease, storage byte/object/I/O and permanent journal
usage/headroom. Allow normal cancellation, settlement and safe retention to
drain live work. Archive eligible terminal executions through the existing
operation when account detail history fills. Archival cannot retire unsafe data
or remove unfinished delivery intent.

Deleted payload tombstones and authenticated source deduplication evidence keep
their permanent journal charge. Never delete or reset that evidence to create
capacity. Plan a separately reviewed retention/capacity change when permanent
allowances fill. High oldest-item age alone is not a delivery/queue latency SLO:
an old retained item can become pending again.

## Recovery and release

Warnings clear when their condition resolves or its owning path is deliberately
paused. Status panels use current samples and show Unknown when evidence is
missing; historical charts preserve gaps. Confirm the observed state, then
resume only through the existing authorized configuration process.

Validate the implementation in a development checkout with:

```sh
make test-ci-observability
```

This evaluates synthetic Prometheus input and dashboard contracts. It performs
no deployment, notification, image publication or native/provider qualification.
Do not change production flags as part of this check.

## Automatic branch waiting and supersession

A `branch_wait` source retains its failure budget while an older automatic run
holds the same account/binding/branch slot. Inspect that run's cancellation,
native stop and transfer receipts; a terminal CI label alone does not release
unsafe native work. Retained artifacts do not hold the branch slot. Do not clear
leases or delete source journals to force a replacement.

`latest_only` is an explicit automation policy choice, disabled by default.
Inspect `superseded_by`, `superseded_sha` and `superseded_at` in event status.
Head lookup failures never cancel existing executions. `head_changed` sources
are permanent deduplication evidence and cannot be revived through admission
recovery. Prior-generation executions require normal completion or explicit
cancellation; a new policy does not silently cancel them. Keep all production
CI gates off until the separate native/provider qualification is complete.
