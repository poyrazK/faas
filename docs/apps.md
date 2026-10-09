# Apps

An app is a named HTTP API with a current deployment, runtime profile, and
optional domains, secrets, environment variables, and triggers.

```bash
gregale apps
gregale app my-api
gregale app my-api scale --min 1
gregale open my-api
gregale apps --q my-api
```

`gregale deploy` creates the app on first use and updates it on later runs.
Use `gregale app my-api --json` in automation; API errors include a stable
code, the observed value, the limit, and a next action.

Apps are isolated from one another. A parked app consumes no resident memory;
its next request wakes a snapshot or cold-boots from its immutable artifact.
See [scale-to-zero](cold-wake.md) and [plans](plans.md) for the limits that
control this lifecycle.

## App costs

Use the app detail page or inspect retained usage costs from the CLI:

```bash
gregale app my-api costs
gregale app my-api costs --month 2026-10 --json
```

The app dashboard lets you select a UTC month and shows compute and
interface-egress allocations attributed to that app ID. Closed months compare
as full months; the current month compares against the same elapsed part of the
previous month, capped at its end. Comparisons appear only when both periods
have complete, fresh, priced data. Per-meter source coverage and workload
drivers remain visible. Load the six month trend to compare complete monthly
compute and egress costs at a glance; selecting a month opens its breakdown.
Months with incomplete data show coverage warnings instead of zero bars.
Download the selected month as CSV to share meter totals, workload allocations,
and their source coverage with a spreadsheet or finance team.
The six month trend also has a CSV download containing each month's totals,
workload allocations, and coverage status; months whose retained data is
unavailable are identified in the export.
Use **App costs** in the dashboard to rank apps by a selected month and compare
compute and egress totals across the account.
Separately attributed jobs and account-level charges are excluded. The known
usage amount is not an invoice total, and app-level forecasts are unavailable.
The CLI accepts the same `--month YYYY-MM` selection. In `--json` output, `meters[].coverage` shows completeness, freshness, and reasons, while `missing_bill_components` lists unavailable bill inputs. Check these fields before interpreting `known_usage_millicents`; it is a retained usage subtotal, not an invoice total.

## Current health and recovery

```bash
gregale inspect my-api
gregale inspect my-api --json
```

The existing application summary now includes **Current operations**, also
shown at the top of the app dashboard. Both read the same
`GET /v1/apps/{slug}/operational-summary` contract with app-read authorization
and completed session MFA. The read leaves parked apps asleep and performs no
traffic changes, monitor-worker evaluations, or recovery actions.

Deployment smoke verification remains a launch-time result. Current production
route health comes from the [production route monitor](route-production-monitoring.md)
and includes its evaluation time and retained observation window. Monitoring
covers the default deployment scope and configured routes only. Low traffic,
missing observations, split traffic, or unavailable telemetry can leave health
unknown; disabled monitoring does not establish health.

Open incidents are shown separately from the current report. A read does not
close an incident or declare recovery. Customer identities and saved request
evidence remain on the existing authorized incident investigation surfaces.

Recovery progress lists pending checked rollbacks across the app's scopes and
pending or failed durable restart handoffs. Rollbacks show their target, scope,
status and stable blocker code. Restarts show attempts and sanitized failure
reasons; handoff status does not independently prove application health. Each
list shows at most ten records and explicitly reports truncation. Missing
status sources are unavailable, never an empty successful queue. Completed
rollbacks and delivered restart handoffs are omitted. Free-form rollback
reasons, blocker details and internal restart errors are not included.

`inspect --json` uses summary schema version 3 and adds `operational` while
preserving the existing fields. Its `runtime.health` still describes deployment
verification. Older servers without the endpoint remain usable; `operational`
is omitted and listed in `unavailable`. The API summary itself uses version 1
and is available through the Go, Node and Python clients.

## Preview app setting changes

Review an app configuration change before applying it:

```bash
gregale app my-api scale --plan --ram 512 --min 1
gregale app my-api scale --plan --ram 512 --out scale-change.json
gregale app my-api scale --apply scale-change.json --confirm
gregale app my-api scale --plan --environment staging --profile medium
```

The preview reads current app settings and the account plan, then shows the
changed values, relevant resource limits, plan-gated settings and known
compatibility issues. `--environment` previews the desired settings of that
environment. The command changes no settings. Remove `--plan` to apply the same
flags immediately, or use `--out` to save a reusable plan and `--apply` with
`--confirm` to apply the reviewed settings without retyping them. The saved plan
records the app's scale-settings fingerprint or the environment's workload
revision; applying it is refused if the settings changed since the preview.
Gregale's API checks permissions and validates the request when applied, so a
preview does not reserve capacity.

When the current or proposed settings keep instances warm, the preview estimates
their 30-day GB-hour usage before and after the change. This covers only the
always-resident portion;
request-driven compute, egress, included plan usage, and the total bill depend on
usage and are not predicted here. `--json` returns a version 1 preview object
with raw current/proposed values, plan checks, warnings and any resident usage
estimate. If the account plan cannot be read, the preview still shows settings
and clearly omits plan and usage estimates.

## Watch application changes

Keep one read-only inspection open after deploying or requesting recovery:

```bash
gregale inspect my-api --watch
gregale inspect my-api --watch --interval 5s --timeout 10m
gregale inspect my-api --watch --timeout 10m --json
```

The watch prints the full summary first, then only changed sections with an
observation timestamp. Deployment, restart and rollback progress, incidents,
current route health, recommendations and unavailable sources are followed
together. Unchanged polls are quiet; updated evaluation/window timestamps alone
do not produce an update. The next emitted summary retains the actual source
timestamps. Current health remains limited to observed traffic, and deployment
verification or the disappearance of a pending operation does not prove recovery.

Reads run one at a time, with a thirty-second deadline per poll. The interval
defaults to five seconds after each read and accepts one second through one hour.
A failed app read emits an unavailable observation without reusing a successful
summary as current health. Temporary failures are retried; a successful read
prints a fresh full summary. Missing optional sources remain explicitly
unavailable. Authorization failures and a missing app stop the watch.

`--json` emits one compact JSON object per line. Watch event schema version 1
includes `type` (`snapshot`, `change` or `unavailable`), `observed_at` and
`app_slug`. Snapshot/change events contain the existing version 3 `summary`;
changes also list their changed section names. Unavailable events contain a
message and omit the summary. Errors and timeout notices go to stderr.

The default duration is unlimited (`--timeout 0`). Ctrl+C exits with code 130;
an elapsed positive timeout exits with code 124 and leaves printed observations
intact. These exit codes describe the watch, not application health. `--interval`
and `--timeout` require `--watch`, which cannot be combined with `--upstreams`,
`--errors` or `--scope`. A linked project may supply the app when the slug is
omitted, just as with a single inspection.

## Follow a restart

To apply current runtime configuration and follow processing:

```bash
gregale app my-api restart --fresh --wait --timeout 5m
```

The command submits one fresh restart and follows its accepted `wake_id` with
status reads. It explains queued, running and retrying states, including waiting
for active requests or a quiet period. Processing completion does not establish
application health; check `gregale inspect my-api` afterwards.

To follow an already accepted fresh restart, use the ID printed by the request:

```bash
gregale app my-api restart status --wake-id "$WAKE_ID" --wait --timeout 5m
gregale app my-api restart status --wake-id "$WAKE_ID" --json
```

Status commands never submit another restart. A client timeout or interrupt
does not cancel accepted work and prints the command to resume following it.
`--json` writes the last observed status receipt to stdout; progress and errors
go to stderr. If a newly submitted restart's status cannot be read before the
first observation, stdout retains its accepted `wake_id` receipt. Failure,
timeout and unavailable status return a nonzero exit code; interruption returns
130. Omit `--wait` to read status once. The default timeout is ten minutes and
the default poll interval is two seconds, configurable with `--poll-interval`.

Durable tracking covers fresh runtime-configuration restarts. The existing
`gregale app my-api restart` snapshot restart retains its behavior; `--wait`
requires `--fresh`. A snapshot restart ID is not a fresh restart status ID.

In the dashboard, select **view progress** on a restart in Current operations.
The page shows the same status and explanations as the CLI, links to logs,
instances and current health, and refreshes every five seconds while processing
is pending. Refresh stops on completion, failure or an unavailable status read.
Refreshing the page only reads the accepted request.

## Settings in a project environment

Read and edit the desired workload configuration of a registered environment:

```bash
gregale app my-api --environment staging
gregale app my-api --environment staging --ram 512
gregale app my-api scale --environment staging --request-timeout 17
```

Deploy to that environment to test the saved configuration. A deployment pins
its settings revision: subsequent desired settings edits take effect on the next
deployment. Changes in staging leave the production app settings unchanged.
The CLI uses the observed revision to reject concurrent edits instead of
silently replacing a newer nested scaling policy. Reload and reapply an edit
when the API returns a revision conflict.

Environment cloning materializes workload settings into an independent revision.
Protected environments require an approved project plan or promotion for edits.
With active source and target release graphs, environment promotion with
`--sync-config` now carries the source deployment's tested workload settings
alongside project configuration. Configuration-only changes are included even
when the artifact is unchanged. Preparation leaves production settings in place;
cutover activates the graph and desired settings atomically, and rollback restores
both. Target physical placement and managed data bindings remain in the target.
Concurrent target settings edits stop publication or rollback.

The full clone workflow is still being implemented: these settings operations
do not yet copy and publish active deployments with a coordinated database and
object-storage capture, or isolate every related resource and runtime control.
