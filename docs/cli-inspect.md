# Deployments, settings and health

Inspect what is deployed and why it is or is not serving, read what a settings change did, and check health from scripts.

## Guided resource settings

Run `gregale app scale my-api --interactive` to review current settings and
choose a resource profile, instance cap, and minimum warm instances within
your account plan's limits. Add `--environment staging` to edit that
environment's desired workload settings.

The flow previews the changes, compatibility warnings, and available resident
usage estimates. Choose to finish, save a reusable plan to a new file, or apply
after a separate confirmation (default: no). Applying reuses saved-plan checks
and rejects settings that changed since the preview was read.

This mode requires terminal input and output and cannot be combined with JSON,
automation mode, setting flags, or the saved-plan flags. For scripts, use
`scale --plan ... --out plan.json`, then `scale --apply plan.json --confirm`.

## Guided rollback

Run `gregale rollback my-api --interactive` to choose a historical release
from the app's deployment history. The list shows revision (or deployment ID),
creation date, status, and scope. The flow identifies a completed release
serving full traffic in that same scope, then shows the selected release's
summary. Summary changes compare that release with its own predecessor.

Enter an optional reason and confirm (default: no). The request pins both the
selected target and the observed current deployment. The checked rollback API
rechecks artifact availability, deployment state, bindings, and handoff
requirements; a changed current deployment requires a fresh review. The flow
waits for completion and uses the existing operation report to show blockers.
`--timeout` and `--poll-interval` control the wait. Interruption or a wait timeout
does not undo an accepted rollback; use its operation ID with
`gregale rollback status my-api --operation <id> --wait` to continue observing.

This mode requires terminal input and output. For scripts or JSON output,
use explicit `--to` and `--expected-current` flags. The guided mode accepts
only `--timeout` and `--poll-interval` alongside `--interactive`.

## Guided custom-domain setup

Run `gregale domain setup api.example.com --app my-api` to bind a domain,
show the DNS records to publish, and wait for ownership verification and an
unexpired TLS certificate. Add `--environment staging` for an environment
binding. An existing binding is reused only when its app and environment match.

After publishing the records, confirm when to start checking. Pending results
include available DNS and certificate diagnostics. The wait defaults to 10
minutes; `--timeout` accepts up to 1 hour, and `--poll-interval` accepts 5 seconds
to 1 minute. Timeout returns a failure status and prints a command to continue
checking the same binding. Leaving the flow preserves the binding and records.
For app bindings, the flow offers a default-domain change with a separate
confirmation, defaulting to no. Environment bindings keep their environment routing.

Setup requires terminal input and output. For scripts or JSON output, use the
existing `domains add`, `domains verify`, and `domains set-default` commands.

## Guided environment promotion

Run `gregale environment promote my-project --interactive` to choose
source and destination environments. Protected environments are marked in the
list. Choose whether to copy non-secret source configuration; secrets remain
scoped to the destination.

The flow displays the existing promotion preview, including release graphs,
workload changes, configuration changes, and blockers. Confirmation names the
project and both environments and defaults to no. Promotion uses the exact
reviewed preview token and the existing protected-environment approval flow.
Stale previews are rejected by the server and require a fresh review.

After submission, the flow waits and shows promotion progress. Use `--timeout`
to set the wait deadline (seconds or a duration, up to 24 hours). Interruption
or a wait timeout does not undo an accepted promotion. The existing promotion
status command can continue observing its promotion ID.

This mode requires terminal input and output and accepts only `--timeout`
alongside `--interactive`. Scripts retain the existing explicit `--from`,
`--to`, `--sync-config`, `--yes`, `--wait`, and `--progress` options.

## Guided project linking

```sh
gregale link --interactive
# Skip the project chooser when you know the project:
gregale link my-project --interactive
```

Choose a project, default app, and default environment. The flow shows current
checkout defaults and the proposed replacement, marks protected environments,
and asks before saving. You can choose no default app or environment. Selecting
an environment sets the scope used by commands that support linked environment
defaults; protection requirements still apply to future operations.

The guide saves the existing local project context and adds `.gregale/` to the
repository `.gitignore` if needed. Pass `--no-gitignore` to skip that update.
It requires an interactive terminal; for scripts, use
`gregale link PROJECT --app APP --env ENV`. Run `gregale config context get` to
inspect saved defaults or `gregale unlink` to remove them.

## Preview an environment upload

```sh
gregale env push --app my-api --dry-run
gregale env push --app my-api --env staging -f .env.staging --dry-run --restart
```

The preview uses the same input parsing and secret scan mode as `env push`.
It shows the resolved app and scope, keys to add or update, scan findings,
projected app secret usage across all scopes, and how changes would apply.
Values and scan snippets are hidden in both human and `--json` output.
Existing keys are marked as updates; sealed values cannot be compared to detect
unchanged values. Repeated keys are shown in upload order.

Every pushed value is sealed, so the default scan only notes pairs that look
like known credentials; strict mode blocks the upload. Invalid keys, values
above the plan limit, or exceeding the secret quota produce a nonzero exit
status. The JSON preview
includes `can_push`, `changes`, `scan_findings`, and `blockers`.

`--dry-run` reads account and secret metadata but uploads nothing and requests
no restart, even with `--restart`. Remove `--dry-run` to upload. The preview
reflects current metadata; the server validates rules again during the upload.
The `--from-stdin` input option is also supported.

## Browse deployment summaries

```sh
gregale release summary --app my-api --interactive
# Use the linked app, or choose an app when no default is linked:
gregale release summary --interactive
```

Choose a release from recent app history, with revision, status, creation time,
and scope shown for each entry. Choose **Show older releases** to browse the
next page or **Cancel** to exit. The summary shows changes relative to the
preceding release and the reported rollback target. The flow prints an
equivalent command pinned to the selected release ID for reuse.

The guide requires an interactive terminal. For scripts or JSON output, use
`gregale release summary ID --app APP --json`. The history picker reads
release data and does not submit a rollback.

## Inspect automatic rollback actions interactively

```sh
gregale alert actions --app my-api --interactive
# Use the linked app, or pick one:
gregale alert actions --interactive
```

Pick an action from the returned history, ordered newest first and labeled with
its status, rule ID, and fire time. The CLI fetches fresh details for that action
and shows its deployment pair, evidence, blockers, and rollback or service
handoff progress. It prints a reusable inspection command with the active profile.

For a pending or blocked action, choose whether to follow it until completion.
The wait defaults to 10 minutes with a 2-second polling interval; configure these
with `--timeout` and `--poll-interval`. A resume command is printed before waiting.
Interrupting or timing out stops observation; the rollback continues independently.
The flow only reads status and never submits or retries rollback actions.

For scripts, use `alerts actions --app APP --fire UUID [--wait] --json`.

## Check binding readiness interactively

```sh
gregale app binding check --app my-api --interactive
# Use the linked app, or choose one:
gregale app binding check --interactive
```

Choose a live deployment from paginated history. The check stays pinned to its
ID and scope, including live candidates with no traffic. Choose the maximum age
of verification evidence (default 10 minutes), whether to require current
PostgreSQL/object-storage application acknowledgements, and whether to waive
unsupported connectivity coverage for active queue/outbound bindings (default no).

The flow prints a reusable command and the readiness report, including blockers
and runtime freshness. If blocked, optionally wait with the existing read-only
poller. It stops when checks pass or blockers cannot progress through existing
work. Configure the wait with `--timeout` and `--poll-interval`; a resume command
is printed before waiting. The flow does not start probes, rotate credentials,
or restart applications. A deployment that stops being live cannot pass.

For scripts, use `bindings check APP --deployment ID` with explicit flags and
`--json`. Blocked checks return a nonzero exit status.

## Reading the inspection overview

`gregale inspect APP` starts with a short human-readable overview, the main
known reason, and one copyable next command that retains the selected profile.
Known errors take priority, followed by blocked recovery, processing operations,
unavailable information, and warnings. The detailed observations and all
recommendations remain below. “No urgent issues reported” means this snapshot
has no error or warning recommendations; it is not a claim that every route or
workload is healthy. JSON snapshots retain their existing schema and contents.
The same overview appears in human-readable watch snapshots.

## Understanding a deployment failure from inspect

The human-readable `gregale inspect APP` overview includes the selected failed
deployment's persisted cause and recovery instruction when available. It reuses
the existing deployment history response without another request. Each field is
limited to 400 characters; an exact deployment command provides the full details.
A newer failed zero-traffic candidate is labeled separately from the release
still serving traffic. No explanation is inferred when persisted text is absent.
Log excerpts stay in the detailed failure view. JSON summaries retain their
existing fields and schema. Human-readable watch output refreshes the overview
when the persisted explanation changes.

## Reading deployment readiness

Human-readable `deploy --dry-run`, `--diff`, and `--server-diff` previews print a
short readiness overview on stderr before the stable diff on stdout. It shows
the app, connection profile, default target scope, inferred runtime/start command
when available, and the first blocking finding or warning. Named environment
previews remain unsupported; the overview does not imply that another environment
was checked. `--lenient` still changes only the exit-code gate. No findings does
not guarantee a successful build or runtime.

Doctor's human-readable report now leads with its first error (or first warning
when no errors exist), existing recovery instruction, and source location before
the full check list. Existing strictness, checks, and JSON formats are unchanged.

## Going from a failure to evidence

Inspection overviews with persisted deployment failures and `inspect --errors`
include a log command pinned to the failed deployment, including a failed
candidate rather than the serving release. `jobs tasks JOB RUN_ID` prints scoped
log commands for up to three failed/dead-letter tasks, sorted by task index;
other failed task indexes remain in the table. A failed `jobs wait` receipt points
to that task view. Workflow status failures link to diagnostics and the current
step's attempts, or to the step list when no current step is available. These
commands retain the selected profile. Logs may expire; retained Job attempt
outcomes remain a separate evidence source. JSON output is unchanged.

## Checking when saved configuration takes effect

Human-readable `env push` receipts now show the app and scope, a scoped secret
delivery command (env push stores values as sealed secrets), and a command to
follow the requested fresh restart or request one after reviewing the serving
scope. Without a restart, running instances retain their environment until a
cold wake. A saved value does not by itself confirm application acknowledgement.

App-setting and scaling receipts list the saved field names without their values,
identify the app and environment, and include a command to reread that same
configuration. Default app updates also point to current operations through
`inspect`. Routing, scheduling, and boot settings have different application
paths; the update response confirms saved intent, not runtime convergence. Named
environment updates do not confirm a release there. These receipts do not
request extra mutations or change the JSON response format.

## Understanding empty lists

Empty app, deployment, Job, and workflow-run lists now identify the selected
connection profile and offer a copyable next command. Workflow lists echo their
status, name, time, and offset filters. Empty deployment cursor pages are distinct
from an empty deployment history; their next command returns to the newest page.
Job lists distinguish the first page from an offset page. Empty run lists do not
prove that an uncertain submission was rejected. Existing JSON envelopes and
NDJSON output remain unchanged. Completed non-follow runtime log queries with
no returned lines show the app, deployment and filters, with an inspection
command. Filters and retention can limit results; active follow streams and
JSON log output keep their existing behavior.

## Reviewing recent observed changes

Human-readable `inspect` summaries show up to three newest observations from the
already-loaded deployment submissions and pending/failed recovery operations.
Each row identifies its reported status, scope when available, and whether its
UTC timestamp is submission, request, or update time. Missing timestamps are
labeled unavailable. Each row includes a profile-preserving command for that
exact deployment, rollback, or restart. Deployment history remains one command
away. This is partial history, not an audit of all configuration changes, and
nearby timestamps do not establish causation. JSON summaries and JSON watch
contents retain their existing schema; human watch output includes these rows.

## Distinguishing processing from health verification

Human-readable `inspect` output separates deployment-time verification receipts
from production route evidence, including the available evaluation time and
coverage, and provides a profile-preserving default-scope `app APP health`
command. This adds no API request to inspection. Health assessment output names
its serving deployments and request-evidence timestamp when supplied. A reported
healthy assessment is bounded by its scope, timestamp, and validity; this read
uses recorded evidence and does not wake the app or probe every route.

Restart receipts include request/completion timestamps when available and a
copyable next command. Pending operations link to restart status, failed
processing links to inspection, and completed processing links to HTTP health.
Completing a restart does not establish application health. JSON responses and
existing exit codes are unchanged.

## Scanning the consolidated inspection output

The human-readable overview has one primary next command. Persisted failure
recovery and deployment log commands appear below it in a dedicated detail
section. Deployment-check timestamps remain beside runtime evidence; current
route-health timestamps and coverage stay in Current operations, replacing the
separate duplicate Verification section. A primary recommendation's explanation
is not repeated at the end, but its recovery advice remains available. Identical
additional recommendations are deduplicated in human output only. A snapshot
with no urgent recommendations points to the existing HTTP health assessment.
JSON snapshots, observations, and recommendations remain unchanged.

## Recovering partial configuration operations

Human-readable errors for partially completed env pushes, multi-secret updates,
secret removals, and rotations identify confirmed writes separately from an
unconfirmed restart or incomplete acknowledgement. They show confirmed key names
without values and include a scoped metadata command. When a restart identifier
is known, the recovery command targets that exact operation; otherwise it points
to current operations before another restart is requested. Failed writes may have
been accepted despite a transport error, so the guidance does not recommend
repeating the whole operation. Existing JSON receipts and error exit codes remain
unchanged. Deployment submissions retain their existing recovery receipts.

## Plain-language settings receipts

Human app-setting target previews and saved receipts use readable labels such as
“Idle timeout (seconds)” and “Minimum warm instances,” reusing scale-plan labels.
They show names without values and retain explicit zero, false, and reset fields.
An app-level update now says “App-level settings saved” rather than implying a
named `default` environment. Named-environment receipts retain their target.
Request payloads, JSON responses, and saved scale-plan formats are unchanged.

## Waiting for recorded health

```sh
gregale app health my-api --wait --timeout 2m
```

Gregale polls recorded default-scope health every two seconds. Success requires
a `healthy` assessment whose evaluation time is not in the future and remains
within its positive validity window. This does not wake or restart the app or
prove every route and workload is healthy. Transient read failures are retried;
authorization and other permanent request errors stop the wait.

Human progress appears on stderr only when the status, phase, scope, freshness,
or summary changes. `--json` emits one final assessment on stdout. On timeout,
exit code 124 accompanies the last fetched assessment, if any, plus an error on
stderr; that observation may be stale. Cancellation exits 130. Ordinary reads
and history output remain unchanged. `--poll-interval` accepts 1s to 1h; timeout
must be positive. Both options require `--wait`, which excludes history options.

## Checking health in scripts

```sh
gregale app health my-api --check
```

`--check` makes one read of recorded health and exits 0 only when the assessment
is healthy, default-scope, and within its positive timestamp validity window.
Degraded, unhealthy, unknown, stale, future-dated, invalid-timestamp, and other-scope
assessments exit 1. API failures retain their existing error exit codes. The read
has a 30-second deadline and does not wake or restart the app.

The full assessment remains on stdout, including with `--json`; a failed check
also writes a diagnostic to stderr. Use the exit code rather than parsing the
assessment to gate a script. `--check` excludes wait, history, timeout, poll
interval, and history pagination options. Ordinary health reads are unchanged.

## Inspecting the latest deployment

To find failed deployments in history:

```sh
gregale release list --app my-api --failed-only --all
```

`--failed-only` filters fetched history to deployments whose recorded status
is `failed`. It filters one page by default or all pages from the supplied
cursor with `--all`. Page size and cursors describe unfiltered history. Human
output includes exact deployment-detail commands and, for app-scoped reads,
deployment-log commands. Account-wide reads provide detail commands and suggest
`--app` for scoped logs. Next-page commands retain the app, filter, limit, and
wide-output option even when the current page has no failures. JSON preserves
the one-page envelope/cursors or the existing `--all` NDJSON format. Empty
results only describe fetched history and do not establish application health.
The command changes no deployments; ordinary output remains unchanged.

```sh
gregale deployment latest
gregale release wait latest --timeout 2m
```

Use a linked app or supply `--app SLUG`. `latest` selects the newest entry in
that app's deployment history across scopes, including failed or pending
deployments; it does not mean the currently serving deployment. Human output
identifies the selected ID, revision, scope, and recorded status on stderr.

The selection uses one history read and pins the resulting ID for the command,
so a later upload cannot change what a running wait observes. JSON output keeps
the existing deployment response format. Inspection bounds the latest lookup
and initial detail read to 30 seconds; waiting includes lookup in its existing
timeout. Empty history reports how to proceed. This selector is supported by
`deployment latest` and `deployment wait latest`; other deployment operations
continue to require their existing explicit references.
