# Test stored workflows

Check a stored workflow before and after you publish it: save test cases,
run verification suites against real executions, keep baselines, simulate
runs, and require coverage before a release. Building workflows is covered in
[Build stored workflows](stored-workflow-authoring.md); running them in
[Run and monitor stored workflows](stored-workflow-operations.md); recovering
failed runs in [Recover stored workflow runs](stored-workflow-recovery.md).

## Test cases, verification suites and baselines

### Save and rerun reusable test cases

Use **Guided test cases** to add, duplicate, rename, and remove cases. Open a
case to edit its input/mocks and choose a workflow step assertion. Check the
expected state, output, or attempt count, then **Apply case changes**. Loop action
assertions select an item index starting at zero. An output of `null` asserts
null; unchecking output omits that check. A blank attempt count omits it, while
zero checks that no attempts occurred. New assertions require an explicit check.

Case navigation applies pending edits locally. **Save test data** persists the
sample and cases; adding or removing them in the editor alone makes no write.
Apply changes before switching to **Advanced test JSON**, select that mode, and
choose **Apply editor mode** to edit complete scenarios. Advanced condition,
reason, detailed retry assertions and per-item mocks survive guided edits.
Input, mocks, and expected output remain JSON so structured data stays exact.

Edit **Sample and mocks** and the named cases, then choose **Save test data**. Use synthetic data: the sample, mock outcomes, and expected values
are explicitly stored with the draft and are readable by authorized app readers.
Credentials belong in managed integrations. Real run or webhook payloads are
never automatically copied into saved tests.

**Run saved test cases** simulates every saved case against the current saved
draft. It reports each case and assertion, including the observed step state,
and continues after failed cases. Unknown or removed step assertions are reported
as `invalid_expectations`. Saved result summaries exclude inputs, outputs and raw
errors. Failed step results identify mismatched fields: missing/ambiguous step,
state, reason, condition result, output, attempt count, or retry outcomes. Expected
and observed values are omitted. Older results can be rerun for field details.
They survive reopening and are marked outdated when the displayed
definition, sample, mocks, or expectations change. Publishing and pausing starts
alone do not make unchanged results outdated. Results describe mock simulations;
they do not prove deployed handlers, integrations or provider responses work.

Save changed definitions first, then save changed test data before running saved
tests or publishing. Suite saves advance the draft revision, reject stale writes,
and invalidate older publishing receipts. Runs store observations without
advancing the revision, changing schedule cursors or starting real actions.
**Check and publish** still executes fresh server checks and obtains a new receipt;
a retained report never grants publication authority.

**Clear saved test data** requires confirmation and removes the saved sample,
cases and report. The current editor retains its unsaved text until navigation.

The API exposes `PUT /v1/apps/{slug}/workflows/stored/{name}/tests` with
`{expected_version, suite: {sample, cases}}`; `suite: null` clears saved data.
`POST /v1/apps/{slug}/workflows/stored/{name}/tests/run` takes `{expected_version}`.
Stored workflow reads return optional `test_suite` and `test_report` fields. Test
inputs omit definitions; runs bind the authoritative saved draft. The suite is
bounded to 32 uniquely named cases, 3 MiB per sample/simulation, 24 MiB for the
combined cases, and 27 MiB overall. Each case accepts up to 4,096 assertions;
reports retain up to 1,024 step summaries per case and indicate truncation.

### Verify one saved case against real execution

Live verification executes the **published** stored workflow against the app's current
live default deployment. Configure synthetic case input and appropriate test
integration credentials before opting into real actions. This first version is
app-scoped; apps requiring platform-tenant identity are blocked.

In **Guided test cases**, use **Live expectations for this case** and choose
**Add live assertion**. Select a workflow step and choose its expected real state,
output, or attempt count. Loop actions also take an item index. Use **Open live
assertion** to edit another assertion, or **Remove live assertion** to remove it.
The **Expected live run state** defaults to succeeded and can explicitly expect
failed or dead. New assertions have no checks until you choose one.

These controls remain separate from the mock assertions. An output of `null`
asserts null; unchecking output omits that check. Blank attempt counts omit the
check; zero asserts no attempts. Counts include resumed attempts, up to 425.
Invalid fields retain the selected assertion and submitted values for correction.
Editing, duplicating or navigating cases does not start execution.

Apply case changes and **Save test data** to persist the live checks. **Disable
live verification for this case** removes its live expectations locally; save test
data to persist that choice. It does not cancel a previously admitted run.

For the complete object, apply changes before switching to **Advanced test JSON**
and edit the case's `live` property. For example:

```json
{
  "expected_run_status": "succeeded",
  "expectations": [
    {"step": "collect", "state": "succeeded", "attempt_count": 1},
    {"step": "deliver", "state": "succeeded", "output": null}
  ]
}
```

Use `live: null` or omit it to disable live checks in advanced JSON. Switching back
to guided cases preserves this object and loads fresh controls. Live step states
are `succeeded`, `failed`, `dead`, or `skipped`;
`mocked` is invalid. Expected run state defaults to `succeeded` and may explicitly
be `failed` or `dead`. Cancellation always fails verification. The durable step
attempt count includes attempts from resumed execution. Loop assertions use
`step: "batch/action"`, `loop: "batch"` and a zero-based `item_index`. Output and
attempt checks preserve explicit `null` and zero; omission skips that check.

Under **Verify a saved case with real handlers**, review the published revision
and deployment, choose a saved case, confirm real actions and select **Start live
verification**. Unsaved test edits must be saved first. Admission freezes the case,
assertions, published revision and code deployment together; stale target fences
reject the request before any run is created. Only the case's `input` is used;
mock outcomes never control real execution.

While the selected verification is open, automatic updates show queued, running
and waiting states and check its frozen assertions after execution finishes.
Only the result panel changes; unsaved editor text stays intact, even when it is
invalid JSON. Local edits keep a warning visible beside the historical result.

Updates pause while the page is hidden and stop after completion or navigation.
They also stop on authentication/permission failure, network failure, timeout,
invalid results or the request limit. **Resume automatic updates** observes the
same run with a fresh budget. Requests run at 3-second intervals, time out after
10 seconds, and are limited to 100 reads/checks per observation session. Automatic
checks send only a protected CSRF token, with a 4 KiB request-body limit.

**Check live results** also refreshes the selected run without replacing editor
fields when JavaScript is enabled. Without JavaScript it remains a manual form
submission. Checks read recorded terminal outcomes against the frozen assertions
and save safe results. They do not start a new run, retry a handler,
or apply later expectation edits. Running and waiting workflows have no verdict.
After a continuation/resume, check again once execution reaches a terminal state.
Reports identify failed fields without returning expected/actual output or errors.
The existing run inspector remains available for explicit diagnostics.

Verification history survives reopening. Case, published revision or current
code deployment changes mark results outdated. A current-target verdict requires
both `outdated: false` and `report_current: true`; the latter means the report
still describes the run's current status, update time and resume count. Draft or
automatic-start changes alone do not invalidate unchanged published-case evidence.

The API offers:

- `GET /v1/apps/{slug}/workflows/stored/{name}/live-verifications` for current target IDs
  and up to 20 recent verifications.
- `POST` to that collection with a required stable `Idempotency-Key` and
  `{expected_version, expected_published_version, expected_deployment_id, case_name,
  confirm_real_actions: true}` to admit one case.
- `GET /v1/apps/{slug}/workflows/stored/{name}/live-verifications/{run_id}` to read a
  retained record, and `POST .../{run_id}/check` to evaluate its results.

Identical retained requests return the original run, including after a publish,
case edit or runtime outage. Changed requests with the same key conflict. History
and idempotency follow workflow-run retention. Go, Node and Python SDKs expose all
four operations. Live assertions are bounded to 128 entries and 3 MiB per spec,
real input and each expected output to 1 MiB, start requests to 4 KiB, and reports
to 128 KiB. Existing saved-suite totals and real execution quotas still apply.

Clearing saved test data also removes frozen live expectations and verification
summaries. It leaves admitted runs under the normal workflow controls and
retention. Live reports are separate from publishing receipts and native runtime
qualification.

### Verify a deployed stored workflow from CI

`gregale workflow stored live` exposes the same saved-case verification through the
CLI. Authenticate the CI job with `GREGALE_TOKEN`; use `GREGALE_API_URL` when selecting a
different control-plane endpoint.
Use `live list --app billing --name paid-invoice --json` to read the current
published revision, default deployment and recent verification IDs. Read the
saved stored workflow version with `workflow stored get`. Review the synthetic case and
its integrations before confirming real actions.

Start one case against explicitly selected target versions:

```sh
gregale workflow stored live start --app billing --name paid-invoice \
  --case synthetic --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --idempotency-key deploy-42-synthetic --confirm-real-actions \
  --json > verification-start.json
```

The start command exits successfully when admission is confirmed. Save its JSON
artifact before waiting; `.verification.run_id` identifies the admitted run.
`live show --app billing --name paid-invoice --run RUN_UUID` inspects it. To require
a current passing verdict in a later CI step, reconnect to that same run:

```sh
gregale workflow stored live wait --app billing --name paid-invoice \
  --run RUN_UUID --timeout 10m --poll-interval 3s \
  --json > verification-result.json
```

For one-step jobs, add `--wait` and optional wait settings to `live start`.
`live check --app billing --name paid-invoice --run RUN_UUID` performs a single
result check; a queued/running/waiting run returns a pending result rather than
waiting. Current terminal reports are reused. Wait follows a continuation seen
during checking; a terminal report that remains stale stops as unavailable.

`check` and waiting commands exit 0 only for a passing report with
`report_current=true` and `outdated=false`. Failed assertions, pending checks,
unavailable verdicts and outdated targets exit 1. A case deliberately expecting a
failed/dead run can pass its assertions. `start` without waiting, `list` and `show`
return success for their own admission/read operation and are not assertion gates.
HTTP failures retain normal CLI exits: 2 authentication, 3 infrastructure,
4 missing/expired record, 5 conflict, and 6 permission denied. Wait deadline expiry exits 124;
SIGINT/SIGTERM interruption exits 130. A timed-out or interrupted run can continue;
the result retains its last observation and a shell-quoted reconnect command.

Wait defaults to 5 minutes, accepts a positive deadline up to 1 hour, polls every
3 seconds (configurable from 1 second to 1 minute), and caps each HTTP request at
10 seconds; an individual request timeout exits 3 as an infrastructure error.
Timeout and poll flags on `start` require `--wait`. A failed request
stops observation. Explicitly reconnect after resolving it. If start admission
was not confirmed, retry with the **same original target flags and idempotency
key**; the CLI does not refresh the target or generate a replacement key.

JSON operation results use `schema_version: 1`, `app`, `name`, `outcome`,
`exit_code`, `message`, and optional `run_id`, `verification` and `reconnect` fields.
Known run IDs and reconnect commands survive even a failed initial read. Outcomes
are `accepted`, `observed`, `passed`, `failed`, `pending`, `outdated`, `unavailable`,
`error`, `timed_out`, or `interrupted`. List results include `app`, `name` and the
existing collection fields. Standard error carries sanitized RFC 7807 diagnostics
for request/protocol failures. Reports contain identifiers, states and mismatch
field names; input/output bodies, expected values and raw errors are excluded.
These observations do not grant publication receipts or authorize future targets.

### Run a resumable live verification suite from CI

Use `live suite` to run an explicitly ordered selection of up to 32 saved cases
against one frozen target. Each case can execute real actions. Review the cases
and integrations before confirming them:

```sh
gregale workflow stored live run --app billing --name paid-invoice \
  --case happy --case expected-failure \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --idempotency-key deploy-42-suite --journal suite-progress.json \
  --confirm-real-actions --timeout 15m --json > suite-result.json
```

The CLI validates all selected cases before execution, then admits them
sequentially. A case must have a current passing assertion report before the next
case starts. Failed assertions or an unavailable/outdated verdict stop the suite.
A pending run is observed until the total invocation deadline. The final gate
refreshes every run and the target; an earlier passing observation changed by an
external continuation cannot establish overall success.

Keep `suite-progress.json` as a private CI artifact, including after timeout or a
failed request. It records the original selection and per-case admission keys,
accepted run IDs and safe observations. Progress is persisted before admission
and between cases. It contains no sample inputs, output values or credentials.
Restore Unix permissions to `0600` if the artifact service changes them, and keep
the journal and its adjacent `.lock` path stable while a command is running.
Only one CLI process may own that journal at a time.

Continue the original suite explicitly:

```sh
gregale workflow stored live run --resume suite-progress.json \
  --confirm-real-actions --timeout 15m --json > suite-result.json
```

Resume rechecks recorded runs and can admit remaining selected cases. It requires
the original API endpoint, app identity and target. It does not accept new case
or target flags. Missing accepted runs stop rather than start replacements. When
admission was unconfirmed, resume reuses the original request and key; it refuses
to replay an uncertain admission after 24 hours. Preserve server verification
records while recovering a suite: idempotent replay depends on their retention.
Inspect older uncertain admissions before explicitly planning new work.

The total invocation deadline defaults to 5 minutes and is configurable up to
1 hour. Polling defaults to 3 seconds (allowed 1 second–1 minute); each request
has a 10-second deadline. Timeout or interruption stops observation and does not
cancel admitted work. The command never automatically retries workflow execution,
continues a workflow or cancels it.

After journal preparation, `--json` emits one `schema_version: 1` report with
`outcome`, `exit_code`, target, `summary`, ordered `cases` and a shell-quoted
`resume` command. Summary counts include `selected`, `passed`, `failed`, `pending`,
`not_started` and `unresolved`; each case retains its last safe observation and
timestamp when available. Historical passing rows in a stopped report are not an
overall success: gate on the command exit and top-level outcome. Exit 0 means all
selected cases passed; assertion outcomes use 1, HTTP failures retain exits 2–6,
overall timeout uses 124 and interruption uses 130. Setup failures use ordinary
CLI diagnostics. Reports omit keys, payloads and raw errors; observations do not
provide publication receipts or an atomic server snapshot of every run.

### Run a live verification suite from the dashboard

After saving cases with live expectations, open **Shared live verification
suites** and choose **Run saved cases as a live suite**. Select cases in the
order shown and review the published revision and deployment displayed above.
Save any editor changes first, then confirm real actions and select **Start live
suite**. The suite calls real deployed handlers and connected services.

The dashboard checks each completed case before starting the next. It stops on
failed, outdated or unavailable results and waits for pending workflows. Progress
and original run IDs are stored on the server; refreshing or reopening the suite
only reads them. Confirm **Continue suite** to check those same runs and advance
remaining cases. Failed or expired runs are never silently replaced. Any decision
to resume an individual failed workflow uses that run's separate recovery controls.

Keep the page visible for automatic advancement. Hiding or leaving it, selecting
**Stop advancing this suite**, a request error or the 100-request polling limit
stops further advancement. Already admitted workflows continue independently.
Reopen the saved suite and confirm continuation when ready. Without JavaScript,
each confirmed **Continue suite** submission checks results and admits at most
one next case. Browser continuation never takes over a shared CLI journal.

Clients can POST `live-suites/{suite_id}/start` with `expected_version`,
`expected_published_version`, `expected_deployment_id`, ordered case names in
`cases` and `confirm_real_actions: true`. Use a UUIDv7 generated within the last
ten minutes and reuse its exact ID and body after a lost response. IDs up to 30
seconds ahead of server time are accepted to allow for clock differences. An
identical retained start request reads progress without advancing it. POST
`live-suites/{suite_id}/advance` with `confirm_real_actions: true` to continue.
Each admission and its stored run link commit together; missing suites are never
recreated by continuation. Go, Node and Python expose both operations.

Suite history keeps at most 100 records per stored workflow. Recent dashboard start
receipts are protected for the creation window plus the clock-skew allowance so
history churn cannot erase recovery identity while the original creation request
can still be admitted.
When all slots are protected, new creation waits rather than displacing them.

### Compare suites between deployments

Open a retained suite under **Shared live verification suites**, then use
**Compare live suites** to choose a baseline. The selected suite is the candidate;
**Swap baseline and candidate** reverses the direction. Each side shows its saved
revision, deployment and original verification run links. Comparison works for
dashboard suites and shared CLI suites.

Matching case names need equal frozen definition and saved-case hashes to compare
results. Pass-to-fail is a **regression**, fail-to-pass a **recovery**, and equal
passing or failing results are **unchanged**. Changes to definitions, inputs,
mocks or assertions are **incompatible**, even if the name stayed the same.
**Added** and **removed** describe which cases each suite selected.

Older deployments are labelled historical without discarding valid recorded
results. Pending, unstarted, missing, unchecked or stale reports are **unknown**;
resuming a run invalidates its old report until explicitly checked again.
Expired evidence keeps its original run link but cannot establish pass/fail.
Zero regressions alone does not mean the candidate passed.

Comparison and refresh only read retained records. They never execute handlers,
check assertions, resume workflows or continue a suite. The dashboard's separate
GET form sends only the stored workflow and suite selectors. Clients can call:

```text
GET /v1/apps/{slug}/workflows/stored/{name}/live-suites/{candidate_id}/compare/{baseline_id}
```

Go `CompareStoredWorkflowLiveSuites`, Node `compareStoredWorkflowLiveSuites`, and Python
`compare_stored_workflow_live_suites` expose the same read. Both suites and their
linked runs are observed in one database snapshot. The response contains safe
per-case observations and disjoint counts, with at most 64 case rows; no inputs,
outputs, assertion values or admission keys are returned. Suite and verification
retention still apply, so a removed suite returns not found. This feature does
not enforce a CI or release gate.

### Gate CI on retained suite evidence

Use the read-only gate after explicitly running and checking a dashboard suite
or a shared CLI suite. Pin the intended candidate's complete target:

```sh
gregale --non-interactive workflow stored live gate --app billing --name paid-invoice \
  --baseline 00000000-0000-4000-8000-000000000010 \
  --candidate 00000000-0000-4000-8000-000000000011 \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --json > stored-workflow-gate.json
```

`strict_v1` passes only when every candidate case passed, the candidate is still
current and matches the expected saved version, published revision and deployment,
and the comparison has no regressions, unknown/incompatible matches or removed
baseline cases. Recoveries and passed added cases are allowed. The historical
baseline's original reports remain usable when they still describe their runs.

| Exit | Meaning |
| --- | --- |
| 0 | The selected evidence satisfies the strict policy. |
| 1 | The intended current candidate contains failed assertions, including unchanged failures; also used for invalid local options. |
| 7 | Inconclusive: wrong/outdated candidate target, incomplete or incompatible evidence, or removed baseline coverage. |
| 2 / 3 / 4 / 5 / 6 | Authentication / infrastructure or protocol / unavailable records / conflict / permission denied. |
| 130 | Observation interrupted. |

Wrong or outdated candidate targets take precedence over their old verdicts.
For a matching current target, a known failure takes precedence over other gaps;
all reasons stay visible. Pending added cases also block a pass, even though their
comparison category is `added`. Malformed or inconsistent response counts cannot
produce a pass. The gate reads the comparison once with a ten-second deadline.
It never starts cases, checks assertions, resumes workflows or advances suites.

JSON reports use `schema_version: 1` and include `policy`, `app`, `name`,
`baseline_id`, `candidate_id`, `expected_target`, `outcome`, `exit_code`, `message`,
`reasons`, `candidate_passed`, `candidate_failed` and a safe `comparison` when valid.
Outcomes are `passed`, `failed`, `inconclusive`, `error` or `interrupted`.
After valid options are parsed, request errors also emit this report, with a
sanitized RFC 7807 diagnostic on stderr. Invalid local flags only emit the normal
CLI diagnostic. Reports contain no private inputs, outputs or assertion values.

The [GitHub Actions example](../examples/stored-workflow-ci/README.md) installs a
pinned CLI, reads existing suite evidence with an app-read token, preserves
nonzero exit codes and uploads the JSON artifact even when the gate fails. It is
opt-in and does not execute real actions. Use a CLI release containing this
command or build the current source. This observation does not enforce publishing
or deployment, impose a maximum age by default, or certify cases that were never
selected. Retention still applies to suites and their linked run records.

To require coverage even when both suites omit the same saved case, commit a
versioned JSON policy and add `--policy stored-workflow-gate-policy.json` to the gate:

```json
{"version":1,"required_cases":["paid-invoice","declined-payment"]}
```

Required names must match candidate selections exactly. A missing required case
adds `required_cases_missing` and returns inconclusive (7); failed assertions in
the intended current candidate still take precedence (1). Existing pending,
incompatible and outdated evidence checks continue to apply. Policies allow 1–32
unique case names and at most 16 KiB. Invalid policies fail locally before any
API request. Unknown or duplicate fields and trailing JSON are rejected.

The JSON report includes `coverage_policy`, `policy_hash` (SHA-256 of canonical
JSON with sorted required names), and `missing_required_cases` when nonempty.
Whitespace and case-list order do not change the hash. Request-error reports
retain policy provenance. Omitting `--policy` preserves the original strict gate
behavior; it does not require coverage beyond the selected suites. The gate
still performs only one comparison read and never starts or checks executions.

To bound evidence age, use policy version 2:

```json
{"version":2,"required_cases":["paid-invoice","declined-payment"],"max_candidate_age_seconds":3600}
```

Version 2 requires a positive integer `max_candidate_age_seconds` (at most
9,223,372,036 seconds, the representable duration range). Version 1 rejects this
field and retains its existing semantics and canonical hash. The limit is part
of the version 2 policy hash.

Age is measured from the oldest candidate run's `created_at` to the CLI evaluation
clock, conservatively covering the whole suite. Checking assertions, updating
suite links, grouping old runs, or obtaining a fresh comparison does not reset age. The gate uses
its own clock rather than the response's observation timestamp. An age strictly
greater than the limit adds `candidate_evidence_stale`; a run creation timestamp in
the future adds `candidate_evidence_future`. Either returns inconclusive (7),
even if the old candidate contains failed assertions; all reasons remain visible.
Exactly the allowed age is accepted, subject to every other gate requirement.
The historical baseline is not age-limited.

Missing timestamps (including older servers or unstarted/unavailable runs) add
`candidate_evidence_age_unavailable` and return inconclusive (7).

When at least one run timestamp is available, version 2 reports include `evidence_age` with `evaluated_at`,
`oldest_run_created_at`, `age_seconds` and `max_age_seconds`. Human output shows
the same age information. Missing timestamps still block the gate even when
other retained timestamps allow a partial age observation. Request/protocol errors do not invent an age; they
retain the selected policy and hash. Version 1 and gates without a policy omit
age information. Keep the CI host clock synchronized; future timestamps fail
closed without a clock-skew allowance. A long-running suite may need a larger
age budget or a newly executed suite. The gate remains a single read.

### Promote and use a named baseline

A baseline name such as `production` or `staging` identifies a release track within
an app and stored workflow. It does not select an execution environment or change
routing. Promotion is a deliberate metadata write; it never starts workflows or
checks assertions. It requires a token with deploy-write scope. Reading the head,
history or gate requires app-read scope.

Discover existing names and their current evidence health with:

```sh
gregale workflow stored baseline list --app billing --name paid-invoice --json
```

The dashboard's **Named baselines** catalog shows each head, policy and target,
retained case results, renewal blockers and inspection links. **Prepare replacement
suite** navigates to the saved-suite controls; starting real work and promoting
replacement evidence still require their own confirmations. An empty catalog
means this stored workflow has no named baselines. Missing retained evidence leaves
its audit receipt visible.

Evidence availability and renewal readiness are separate. Passing retained
reports can remain usable for a historical baseline even when its target is old
or its runs exceed the policy's age limit for a new promotion. The catalog checks
renewal with the original run timestamps; it does not refresh evidence or declare
a CI gate verdict. CI still evaluates its distinct candidate and the named
baseline through the gate command. Discovery requires app-read scope, returns at
most 20 heads sorted by name in one coherent read, and works with execution off.

In the dashboard, select a retained live suite and open **Named baselines**.
Enter a baseline name to view its current receipt, policy and promotion history.
Review the version 2 policy and choose **Preview promotion** to see qualification,
blocking reasons, missing required cases and original-run age. This preview only
reads recorded evidence. New names initially suggest all selected cases and a
one-hour age limit; review these defaults for your release process.

A passing preview offers a separate confirmation showing the exact suite,
target and expected baseline revision. Confirming promotes through the same
backend checks as the CLI. If the result is uncertain, retry the same form to
preserve its promotion UUID and intent. A conflict requires reviewing the current
head and preparing a new preview. The panel also provides a CI command for the
selected candidate; select a different suite from the baseline before using it.
Opening or refreshing the panel never promotes, starts runs or checks assertions.

After explicitly running and checking a retained suite, promote it with a version
2 policy. Use a new UUID for each deliberate promotion, and preserve it for retries:

```sh
gregale workflow stored baseline promote --app billing --name paid-invoice \
  --baseline-name production --suite 00000000-0000-4000-8000-000000000010 \
  --promotion-id 00000000-0000-4000-8000-000000000020 --expected-revision 0 \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --policy stored-workflow-gate-policy.json --json

gregale workflow stored baseline get --app billing --name paid-invoice \
  --baseline-name production --json
gregale workflow stored baseline history --app billing --name paid-invoice \
  --baseline-name production --json
```

`--expected-revision 0` explicitly creates a new name. For later promotions, read
the current receipt and supply its revision. Concurrent writers cannot silently
overwrite each other: a stale revision returns conflict (409; CLI exit 5).
Promotion re-evaluates the exact target, every selected case, required coverage and
original-run age from retained evidence. A failed, unchecked, incompatible,
incomplete or outdated suite cannot be promoted. Reads and promotion work with
the execution runtime disabled.

Receipts record the promotion UUID, revision, predecessor, authenticated account
and API-key ID, promotion time, suite target, policy/hash, passed count and oldest
run age. They contain no run inputs, outputs or assertion values. An uncertain
write must be retried only with its original promotion UUID, expected revision,
suite, target, policy and authenticated actor/key. An identical retained retry
returns the original receipt, even after a later promotion; it does not move the
head back or renew evidence. A returned receipt certifies what happened at that
promotion time, not current evidence availability.

Use the name in CI instead of copying a baseline suite UUID:

```sh
gregale --non-interactive workflow stored live gate --app billing --name paid-invoice \
  --baseline-name production --candidate 00000000-0000-4000-8000-000000000011 \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 --json
```

Choose exactly one of `--baseline` and `--baseline-name`. Named gates inherit the
reviewed version 2 policy from the receipt. If `--policy` is also supplied, its
canonical hash must match; otherwise `baseline_policy_mismatch` is inconclusive
(7). Promote a policy change explicitly before gating by name. Existing UUID
mode retains its original optional-policy behavior.

The named gate makes two GETs under one ten-second budget: resolve the receipt,
then compare its exact suite UUID. It never changes a baseline. Reports add
`baseline_name` and `resolved_baseline` (the exact receipt), alongside the resolved
`baseline_id` and evaluated policy hash. If another promotion happens between
reads, the report still identifies the original resolved revision. A candidate
must be distinct from that resolved baseline; otherwise the command exits 1.
Invalid receipts fail as protocol errors (3), and missing heads or suite groups
use normal not-found handling (4), without fallback to a different suite.

There are at most 20 active and 100 lifetime baseline names per stored workflow, and 100 retained promotion
receipts per name, newest revision first. Names use 1–63 lowercase ASCII letters,
digits or hyphens, beginning and ending with a letter or digit. Promotion requests
are at most 20 KiB; stored receipts at most 32 KiB. Promotion policy JSON must
fit the existing 16 KiB policy limit after canonical serialization as well as on input. Reuse an existing release-track
name instead of creating a new name for each build. Stored workflow deletion removes
its history. Clearing saved cases retains audit receipts but removes evidence.
Active current baseline suite groups are protected from normal suite pruning within the
existing 100-group budget; linked run/report retention still applies. Completed
workflow runs and their reports normally expire 30 days after completion. This
retention applies regardless of the baseline policy; renew the baseline before
its evidence expires. Replaced groups can be pruned normally. Missing or invalidated evidence blocks gates;
receipts do not recreate or execute it.

### Share suite results with your team

Add `--share` when creating a new CLI suite to retain its selection and run links
in the app dashboard:

```sh
gregale workflow stored live run --app billing --name paid-invoice \
  --case happy --case expected-failure \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --idempotency-key deploy-42-suite --journal suite-progress.json \
  --share --confirm-real-actions --json > suite-result.json
```

Open the stored workflow and find **Shared live verification suites**. Select a suite
to inspect its frozen target, observation time and ordered case results, then
open an individual verification for details or an explicit result check.
**Refresh suite results** reads current retained records. It does not start cases
or evaluate assertions. A terminal run without a current assertion report appears
unavailable until checked. Changed targets are marked outdated. Expired records
keep their known run IDs and appear unavailable rather than passing.

The journal remembers sharing automatically. Resume with the original
`--resume suite-progress.json --confirm-real-actions` command; do not add new
selection flags or `--share`. The private journal remains necessary for execution
recovery. Shared history contains no admission keys, sample inputs, output values
or frozen assertions. JSON CI results include the stable `shared_suite_id`.

The CLI saves the shared selection before admission and links each accepted run
after recording it privately. A sharing failure stops later admissions. An
already admitted run may continue; keep the journal and resume explicitly after
resolving the failure. Existing local-only suites do not contact these endpoints.

Shared history retains 100 recent suites per stored workflow and lists the newest 20.
Older retained suites remain accessible by their suite ID. Workflow verification
records have their own retention; a suite does not extend their lifetime. Clearing
saved test data or deleting the stored workflow also removes shared suite history.

Clients can save metadata with
`PUT /v1/apps/{slug}/workflows/stored/{name}/live-suites/{suite_id}`, read it with GET
of the same path, or list the collection. Choose a stable UUID and send
`expected_version`, `expected_published_version`, `expected_deployment_id` and
ordered `cases: [{name, run_id?}]`. Requests are capped at 16 KiB and 32 distinct
cases. Run IDs must reference existing verifications for the same frozen case and
target. Selection and existing run links cannot be replaced; older partial saves
preserve links already recorded. Saves never execute workflows or accept verdicts.
The metadata APIs are available in the Go, Node and Python clients.

### Retire an unused baseline

Use **Retire this baseline** in the dashboard, review the name and revision, then
confirm permanent retirement. Or use an explicit stable UUID from the CLI:

```bash
gregale workflow stored baseline retire --app billing --name paid-invoice \
  --baseline-name production --retirement-id 00000000-0000-4000-8000-000000000012 \
  --expected-revision 3 --confirm-retirement --json
```

Retirement reserves the name permanently, frees one of the 20 active-name slots,
and releases its suite retention pin. It does not immediately delete evidence;
other active names or recovery windows can still pin the same suite. Each
stored workflow can have at most 100 lifetime names to bound retained audit data.

Name lookup now returns HTTP 410. Named CI gates exit 4 with
`baseline_retired`, so CI must explicitly choose another active baseline. A gate that resolved the old receipt before retirement keeps its
snapshot. Active listings omit retired names. Read `baseline history` with the
retired name to inspect its promotion receipts and attributed retirement receipt.
Old promotion retries return their historical receipt without reactivation.
Retry an uncertain retirement with the original UUID and expected revision. A
revision conflict requires reviewing the new head before submitting a new intent.

## Check reusable scenarios

Run the supplied starter scenarios together:

```sh
gregale workflow stored check --app billing --scenarios scenarios.yaml
```

A suite references one definition and sample files. All file paths are resolved
relative to the suite file, so the command can run from another directory.
The command requires login and simulation read scope for the app. Integration
bindings are checked by the server as in ordinary simulation. It creates no runs
and does not save or publish definitions.

```yaml
definition: stored_workflow.yaml
scenarios:
  - name: approved
    input_file: sample-input.json
    mock_outputs_file: mock-approval-outputs.json
    mock_attempts_file: mock-approval-attempts.json
    expect:
      approval:
        state: mocked
        reason: event_received_mocked
        output: {approved_by: reviewer-42}
      fulfill: {state: mocked, when_matched: true}
      expire: {state: skipped}
```

Each scenario must have a unique name and at least one expectation for a root
step. Assertions support `state`, `reason`, `when_matched`, and exact JSON
`output`. Omitted assertion fields are ignored. An explicit `output: null`
requires a supplied JSON null, rather than a missing output. Objects compare
without regard to key order, arrays retain order, and integer precision is
preserved. `when_matched` checks the recorded guard decision; `reason` checks
the branch or resume reason. Individual loop items are not assertion targets;
use the aggregate root output to check a loop's results.
Expected outputs require string object keys and finite JSON numbers, reject
duplicate keys, YAML aliases and custom tags, and allow up to 64 nesting levels.

Traces must be complete by default. Set `require_complete: false` in a scenario
to deliberately check a partial trace. Invalid definitions, simulation issues,
request errors, missing expected steps, and assertion mismatches always fail.
Unused-mock warnings do not fail checks. Scenario fields include `input_file`,
`mock_outputs_file`, `mock_item_outputs_file`, and `mock_attempts_file`.

Suites accept YAML or JSON with strict field checking, at most 32 scenarios,
3 MiB of suite text, and 24 MiB of combined simulation requests. Referenced
samples retain ordinary simulation size limits. All files and expectation
names are checked before API requests. Execution is sequential with a two-minute
deadline for the suite, and failures do not stop later scenarios from reporting.
The human report lists PASS/FAIL per scenario and assertion failures without
printing sample values. The global `--json` option returns a structured report.
Exit code 0 means every scenario passed; malformed suites, assertion failures,
and request failures return 1. This makes the command suitable for CI.

## Check scenarios before publishing

Attach the same suite to a publish command:

```sh
gregale workflow stored publish --app billing --name approval-flow \
  --expected-version VERSION --scenarios scenarios.yaml
```

Use the version returned by applying your definition. The command loads all
scenario files, fetches that saved draft, and requires the suite definition to
have the same JSON content, ignoring object key order and equivalent number
formatting. A different local definition or stale version
blocks publication; apply your intended changes first rather than checking one
definition and publishing another.

Scenarios run against the fetched draft with complete traces required, including
when the suite specifies `require_complete: false`. Validation issues, request
errors, or assertion failures prevent a publish request. After checks pass, the
command fetches the draft again and compares its version and definition. The
publish request uses the original expected version; the server atomically
rejects concurrent changes that occur after this recheck. It never advances to a
new version automatically. Existing manifest ownership and takeover rules apply.

The global `--json` option returns one combined report with `checked_version`,
`definition_hash`, `checks`, `publication_attempted`, `publication_outcome`,
`published`, and a stored workflow summary on success. The outcome is `not_attempted`
when checks or the draft recheck block publication, `confirmed` on success,
`rejected` for definitive validation, authorization or conflict responses, or
`unknown` when a transport or server failure prevents confirmation. API failures
include `publication_http_status` and `publication_error_code` without service
detail or sample values. Failures after simulation include an `error`; malformed
suites, initial fetch failures, and initial draft mismatches use ordinary CLI
errors. A publication transport failure can leave the outcome uncertain:
`publication_outcome: unknown` means publication was not confirmed. Inspect the
stored workflow status before retrying. HTTP transport replays retain the same
idempotency key and expected version. A conflict requires reloading the draft
and rerunning checks. Exit code 0 requires
both passed checks and confirmed publication. The check and publish operations
share a two-minute deadline and require simulation read scope plus publish
permission. Omitting `--scenarios` retains the ordinary publish command.

This is an opt-in CLI check; direct API publication and publishing without a suite
do not enforce scenario assertions. Simulation does not prove real handler side
effects or live event and callback delivery.

New runs capture the app's live default deployment as `deployment_id`. A run
that starts on deployment A continues using A's handler code after deployment B
becomes live, including after a timer, event/callback wait, retry, or manual
resume. Condition checks, failure handlers, and `for_each` items use the same
pin. Internal events and verified webhooks capture and retain code when accepted,
even if run admission waits for capacity. Publishing a stored workflow changes future
definition snapshots; it does not change an existing run's code or definition.

The run API and generated SDKs expose `deployment_id`; CLI run lists and
inspection show the deployment. Historical runs without a recorded pin omit this
field and display `legacy (unpinned)`. Those runs keep best-effort routing.
Pinned code stays retained while its run/event history exists, including the
existing 30-day terminal run retention for resumptions. Unreferenced code becomes
eligible for ordinary cleanup. Retention does not extend public revision access
or keep a VM resident. If pinned code is unavailable, execution fails instead of
switching deployments. Start a new run to use newly deployed handlers.

Pins guarantee this app's deployed handler code. Runtime configuration and
credential revocation follow existing deployment and integration rules. External
provider behavior and downstream project service releases are not pinned. Side
effects still require idempotency.

For example, save an event stored workflow through the API:

```http
PUT /v1/apps/billing/workflows/stored/paid-invoice
Content-Type: application/json

{
  "expected_version": 0,
  "definition": {
    "name": "paid-invoice",
    "trigger": { "type": "event", "source": "billing", "event_type": "invoice.paid" },
    "max_concurrent_runs": 4,
    "max_concurrent_actions": 8,
    "steps": [
      { "name": "record", "path": "/record-payment", "input": { "invoice_id": "{{input.data.invoice_id}}" } },
      { "name": "receipt", "path": "/send-receipt", "depends_on": ["record"], "retry": { "max_attempts": 4, "backoff": "exponential" } }
    ]
  }
}
```

An attempt Gregale refuses before your app receives it (the gateway could not
wake the app or check traffic security) does not end a step. The step retries
after at least 5 seconds, up to two attempts beyond `retry.max_attempts`. The
refused attempt still counts in `X-Faas-Workflow-Attempt`.

Set `max_concurrent_runs` to cap active instances of one stored workflow. Omit it or
set it to `0` to use only the app plan limit; valid explicit caps are 1–200.
Runs waiting in the queue have status `pending` and have not started. Started
runs keep their slot through retries and event/callback waits. Pending queue size
remains bounded by the plan's total active-run quota.

For scheduled stored workflows, `overlap: "allow"` admits each due occurrence and
queues it when the per-stored workflow cap is full. `overlap: "skip"` keeps its
existing behavior and records a skipped occurrence instead. Event and manual
starts queue by default when a workflow's concurrency cap is full, provided
the app still has plan quota. The cap is part of the run's definition snapshot;
publishing a new cap affects newly admitted runs.

Set `max_concurrent_actions` to cap active app-handler, managed outbound, and
condition-check calls across runs of the same stored workflow. Omit it or set it to
`0` to keep the current behavior. Event, callback, and duration waits do not use
an action slot. A run that reaches the cap stays pending and the scheduler
retries it as slots become available; each run keeps the action limit from its
immutable definition snapshot.

Manual run creation accepts an optional `Idempotency-Key`. Reuse the same key
and input after a timeout or lost response to receive the original run, even if
the workflow was published again in the meantime. The request fingerprint
ignores JSON whitespace and object-key order. Reusing a key with different input
returns `409`; use a new key for a new run. The key remains reserved while its
run is retained.

```sh
gregale workflow run --app billing --input '{"invoice_id":"inv_42"}' \
  --idempotency-key invoice-paid-inv_42 paid-invoice
```

The API accepts the same header on `POST /v1/apps/{slug}/workflows/{name}/runs`.
The Go, Node, and Python SDKs expose the optional header; the CLI flag is useful
when a script needs to repeat a start request after an uncertain result.

Use the returned `version` in the publish request:

```http
POST /v1/apps/billing/workflows/stored/paid-invoice/publish
Content-Type: application/json
Idempotency-Key: publish-paid-invoice-1

{ "expected_version": 1 }
```

The revision above is illustrative: always send the revision returned by your
actual save. A version conflict means another editor changed the saved state;
reload it before applying your changes. Revisions are opaque and need not be
consecutive. Read/validation use read scope; writes use deployment write scope.

YAML continues to own a name until you explicitly take it over when publishing
through the API (`take_over_manifest: true`). Later app deployments preserve
the API publication. Deleting it can restore the current YAML definition,
which requires explicit acknowledgement (`restore_manifest: true`). The CLI
delete command also requires the current version and `--yes`; pass
`--restore-manifest` to acknowledge that the YAML definition may take ownership
again. Drafts do not change the published stored workflow. Definition quotas count
YAML and saved draft names together, with the plan's existing workflow limits.

## Simulate before publishing

### Preview managed integration requests

Outbound steps can use run input and previous step outputs in their path and
query values. For example, this step retrieves one contact from an integration
already bound to the app:

```yaml
name: lookup-contact
trigger:
  type: manual
steps:
  - name: lookup
    outbound:
      integration_id: 00000000-0000-0000-0000-000000000001
      method: GET
      path: "/v1/contacts/{{input.contact_id}}"
      query:
        email: "{{input.email}}"
        source: stored workflow
```

Replace the example UUID with your managed integration ID. Path templates must
occupy an entire segment. Query keys are fixed; values can contain templates.
References to `steps.<name>.output` require that step in `depends_on`. Loop
actions can use `input.item`, `input.index`, and `input.input` for the original
run input. GET and HEAD carry no request body.

Simulate with `--input-file` before publishing. With
`{"contact_id":"contact 42","email":"a+b@example.com"}`, the CLI shows
`GET /v1/contacts/contact%2042?email=a%2Bb%40example.com&source=stored workflow`.
The API and Go, Node, and Python SDK traces expose the resolved `path` and
encoded `raw_query`. Empty or unresolved queries omit `raw_query`. Simulation
resolves eligible root and loop actions before accepting their success or
attempt mocks. Invalid or missing URL values produce
`outbound_target_resolution_failed` and an incomplete trace; skipped or blocked
actions do not resolve their targets. `--require-complete` rejects these
incomplete traces in CI.

Dynamic path values cannot contain `/`, `\\`, `%`, or dot segments. Requests
are bounded to 32 query parameters, 2 KiB per query value, 4 KiB of encoded query,
and 512 bytes per dynamic path value. Integration and app binding route policies
are checked at publication and again against the resolved request at execution.
Origins and credentials stay managed by Gregale, and existing provider
idempotency requirements apply to mutating retries. Simulation shows sample
URLs; runtime route authorization and provider behavior still require real
execution.

### Supply sample inputs and mocks

Run the same simulation from the CLI before saving or publishing. Provide the
definition, sample input, and action mocks as separate files:

```sh
gregale workflow stored simulate --app billing --file paid-invoice.yaml \
  --input-file sample-input.json --mock-outputs-file mock-outputs.json \
  --mock-attempts-file attempt-mocks.json
```

`sample-input.json` contains one JSON value. `mock-outputs.json` maps action
step names to their simulated JSON results; a loop can use
`--mock-item-outputs-file` with a map of `for_each` step names to arrays.
`--mock-attempts-file` maps action or wait names to ordered outcomes so you can test
retries and failure handlers without invoking an action. For example:

```json
{
  "charge": [
    { "outcome": "failure", "http_status": 503 },
    { "outcome": "success", "output": { "charged": true } }
  ]
}
```

Attempt outcomes are `success` with any JSON `output`, `failure` with either an
`error` message or non-2xx `http_status`, or `timeout`. Ordinary actions retry
server errors and transport errors up to their configured maximum (three
attempts by default); managed integrations also require safe-to-repeat behavior
and retry selected transient statuses. If another retry outcome is needed but
not supplied, the trace says `would_retry` and dependent or exception-handler
steps remain unresolved. A terminal failure activates `on_failure` and makes
`{{failure}}` available to that handler with the source step, status, attempt
count and mocked message. HTTP failure messages contain the status code only,
since response bodies are not part of the mock format.

For a wait step with an `on_timeout` route, provide one timeout outcome in this
file, such as `{ "payment_event": [{ "outcome": "timeout" }] }`. It resolves
the wait with `{"timeout":true}` and activates its timeout handler. Action
timeout outcomes also require `on_timeout`. Outcomes after a success, timeout,
non-retryable failure or exhausted retry limit are rejected. The existing
`--mock-outputs-file` format remains a shorthand for one successful action
result and cannot be combined with attempt mocks for the same step.

Event and callback waits also accept exactly one successful outcome with the
received payload in `output`, including explicit JSON null:

```json
{
  "approval": [
    { "outcome": "success", "output": { "approved_by": "reviewer-42" } }
  ]
}
```

The payload becomes the wait's output for downstream mappings and guards.
The trace uses `mocked` with reason `event_received_mocked` or
`callback_received_mocked`, and includes the payload. A successful wait skips
its timeout route. Waits reject multiple outcomes, failure outcomes, and the
reserved `{"timeout":true}` payload as a success. Duration and condition waits
accept only the existing timeout mocks; simulation does not advance a clock or
evaluate a live condition. Missing wait mocks still produce `would_wait`.
These samples do not exercise event routing, callback tokens, authorization,
expiry races, or delivery deduplication.

The human trace shows mapped sample input/output values, attempt summaries,
branch decisions, blocked steps, and warnings. `--json` returns the complete
bounded trace. Simulation
does not invoke the app, call integrations, create runs, or publish events. Add
`--require-complete` in CI to fail when the supplied mocks leave steps unresolved;
without it, partial traces remain useful during interactive authoring.

Send a definition and sample workflow input to
`POST /v1/apps/{slug}/workflows/stored:simulate`. Simulation needs read scope and
app ownership, but no live deployment or enabled workflow runtime. It does not
save a draft, create a run, publish events, invoke your handlers or call managed
integrations. It reads managed integration bindings without opening credentials.

```json
{
  "definition": {
    "name": "receipt",
    "steps": [
      { "name": "lookup", "run": "lookup_invoice", "input": { "id": "{{input.invoice_id}}" } },
      {
        "name": "send", "run": "send_receipt", "depends_on": ["lookup"],
        "when": { "ref": "steps.lookup.output.paid", "op": "eq", "value": true },
        "input": { "email": "{{steps.lookup.output.email}}" }
      }
    ]
  },
  "input": { "invoice_id": "inv-1" },
  "mock_outputs": { "lookup": { "paid": true, "email": "customer@example.com" } }
}
```

The trace shows `lookup` as `mocked` with its resolved input, and `send` as
`would_execute` with `when_matched: true` and the mapped email. Supplying
`paid: false` skips `send` with `when_false`. Removing the lookup mock leaves
`send` blocked with `dependency_output_missing`. An explicit null is a supplied
successful result; an omitted mock is an unknown result. Mock values retain
their JSON types and embedded template text is never evaluated again.

For loops, use `mock_item_outputs: { "batch": [<first result>, <second result>] }`.
These are a sequential prefix: the next item would execute, later items wait
for their predecessor, and the parent remains `expanded` until every result is
provided. An empty loop resolves to `[]`. Completed loops and joins have state
`resolved`, with the same ordered aggregation and branch selection as execution.
False guards and dependency skips propagate through the trace. Unused mocks
produce warnings. Unknown step names or mocks for control steps return 400.

Waits without a timeout mock remain `would_wait`; dependent actions are blocked.
Simulation does not advance time, deliver callbacks/events or call condition
checkers. Exception handlers remain blocked until the source outcome is known,
and are skipped with `route_not_taken` after an ineligible terminal outcome.
Input/guard evaluation errors appear as trace errors and issues. An action with
`on_timeout` cannot use the exact reserved output `{"timeout":true}` in
`mock_outputs`; represent that outcome explicitly with `mock_attempts` instead.

`definition_valid` reports structural, plan and binding validation independently
of sample-data issues. Invalid definitions return 200 with issues and an empty
trace. `complete` means every root reached a known terminal outcome under your
supplied mocks; a fully modeled failed execution can be complete even though it
is not a successful workflow run. It does not verify real handler behavior,
trigger admission or live availability.
`definition_hash` is SHA-256 of the serialized submitted definition, not a saved
revision or a semantic hash of differently ordered raw JSON. Root rows follow
deterministic dependency order, with loop item rows immediately after their parent.

Limits are 3 MiB per request, 1 MiB per definition and each input/mock value,
128 root steps, 1024 trace entries and 4 MiB per response. Existing loop limits
also apply: 128 items, 1 MiB total resolved item inputs and 1 MiB aggregate output.
Oversized requests, template expansions or traces return 413 with limit metadata.
Use representative, nonsecret sample data; the trace includes resolved values.
Go `Client.SimulateStoredWorkflow`, Node `WorkflowsService.simulateStoredWorkflow` and
Python `workflows.simulate_stored_workflow` expose this API.

## Coverage and publishing checks

### Review draft changes before publishing

```sh
gregale workflow stored diff --app billing --name approval-flow
```

This read-only command compares the saved draft with the published definition from
one stored workflow snapshot. It reports added, removed and modified fields, including
triggers and schedules, step dependencies and guards, input mappings, outbound
integration targets, retry policies, waits and failure routes. Steps are matched
by name; `/step_order` records changes to their stored order. Field paths use JSON
Pointer escaping (`~0` for `~`, `~1` for `/`), with step names under `/steps`.
Arrays are compared in order. Missing fields and explicit `null` remain distinct.

The report identifies both versions and the current live automatic-starts state.
That live state is metadata, not a prediction of whether publishing will enable
starts: use `workflow stored pause` or `resume` to change the live state. A stored workflow
with no published definition is labeled as a first publication. An unchanged
definition reports no changes and exits successfully.

Values are omitted from human and JSON reports so input payloads and integration
values are not echoed. To inspect actual values, explicitly export the draft and
published definitions with `workflow stored get --definition-out` and `--published`.

Publishing with `--scenarios` shows this same summary before scenario simulation
and the publication request. JSON output includes it under `diff` in the final
combined publication report. Scenario checks and the version checks still block
publication on failure or a changed draft; the diff itself does not require
confirmation and does not block a publication with no definition changes.

### Scenario coverage hints

`workflow stored check` reports advisory coverage hints after the scenario results.
The JSON report includes a `coverage_hints` array with stable `step`, `code` and
`message` fields. Checked publishing includes the same array under
`checks.coverage_hints`. Hints do not change the pass result or prevent publishing.

Coverage comes from root-step and loop-item traces in passing scenarios. Declared expectations
and unused mocks do not establish coverage. Hints identify guards without both a
matched and skipped outcome, failure routes whose handler was never reached after
a terminal failure, event/callback waits without a mocked success, configured wait
timeout routes without a mocked timeout, and retry policies allowing multiple
attempts without an observed second attempt. A pending retry alone does not count.
Condition waits can report missing timeout coverage; successful condition and
duration waits are outside the simulator's supported mocked-success outcomes.
Loop coverage also identifies missing empty-collection and multiple-item scenarios, and missing matched/skipped guard outcomes for the item action. Item action hints use `loop-name/action` because a `for_each` definition has one unnamed action. Outcomes from different items and passing scenarios are combined, but only within the same loop. Skipped or unexpanded loops do not establish collection coverage. Per-item retry hints can be cleared by a passing scenario with `mock_item_attempts_file` that exercises a second attempt. The workflow schema does not allow loops inside loop actions. These hints describe
observed paths, not assertion completeness or production execution guarantees.
Reports omit input, output and mocked error values.

### Simulate per-item retries and failures

Use `--mock-item-attempts-file item-attempts.json` with `workflow stored simulate`, or
`mock_item_attempts_file: item-attempts.json` in a scenario. The file maps loop names
to canonical zero-based item indexes and ordered outcomes:

```json
{
  "sync": {
    "0": [
      {"outcome": "failure", "http_status": 503},
      {"outcome": "success", "output": {"updated": true}}
    ],
    "1": [{"outcome": "timeout"}, {"outcome": "success", "output": null}]
  }
}
```

Each item accepts 1–25 outcomes with the same success/failure shapes as root
attempt mocks. Indexes must be within the materialized collection (maximum 128
items). Sparse indexes are allowed; a reached item without a mock remains
unresolved. Do not combine item-output mocks and item-attempt mocks for the same
loop. Guard-skipped items and items after a stop-on-failure may leave unused mocks,
which produce warnings. Input and outbound targets are resolved before mocks.

Item timeouts require a configured action timeout and are execution errors subject
to the action's retry policy. They do not create a successful timeout sentinel or
an `on_timeout` route. Unsafe outbound actions retain their runtime retry limits.
Outcomes after success or a terminal failure are rejected. A retry without its
next outcome leaves the trace incomplete.

Simulation traces retain the item index, parent loop and safe attempt summaries.
With the default stop policy, a terminal item failure skips later items and the
parent exposes the collected successful prefix. With `on_item_failure: continue`,
later items are simulated and the result preserves positions with `null` for
failed or guard-skipped items. In both cases the parent remains failed or dead,
so ordinary dependent steps are skipped. This is deterministic data-flow
simulation; it does not reproduce concurrent admission or timing of parallel
items. Mocked error text is not echoed in trace summaries or coverage hints.

### Assert individual loop items

Scenarios can use `expect_items` alongside root `expect` assertions, or by itself:

```yaml
scenarios:
  - name: retry-first-contact
    input_file: input.json
    mock_item_attempts_file: item-attempts.json
    expect:
      sync: {state: resolved, output: [{updated: true}, null]}
    expect_items:
      sync:
        "0":
          state: mocked
          when_matched: true
          output: {updated: true}
          attempt_count: 2
          attempts:
            - {outcome: failure, http_status: 503}
            - {outcome: success}
        "1": {state: skipped, when_matched: false, attempt_count: 0, attempts: []}
```

Selectors use the loop name and canonical zero-based item index, not internal
step names. Each selected item must appear exactly once in the trace; missing or
duplicate items fail the scenario. Unknown loops, non-loop selectors, invalid
indexes and empty expectations are rejected before simulation.

Items support `state`, `reason`, `when_matched` and `output`, with the same exact
JSON comparison as root steps. Explicit `output: null` requires a supplied null;
it does not match an absent output. A skipped or failed item's collected slot may
be null while its own trace output is absent, so assert the collected array on the
parent separately.

Both root and item assertions also accept `attempt_count` (0–25) and `attempts`.
An `attempts` list asserts the complete ordered sequence, including its length;
each entry requires `outcome` (`success`, `failure`, or `timeout`). Optional
`http_status` checks the status of a failure attempt. `attempts: []` asserts no
observed attempts. If both fields are provided, their counts must agree. Assertions
use observed attempts, not the configured maximum retries. Unspecified fields are
not asserted. Reports identify the failing loop and item without echoing output,
expected output or mocked error text. Item assertion failures also block publishing
with `--scenarios`.

### Turn coverage hints into scenario starters

```sh
gregale workflow stored check --app billing --scenarios scenarios.yaml --suggest
# Optional new destination:
gregale workflow stored check --app billing --scenarios scenarios.yaml \
  --suggest --suggest-out review-scenarios.yaml
```

After checking the existing suite, `--suggest` writes a separate
`scenario-suggestions.yaml` beside that suite. Existing files and symlinks are
refused; the new file is private (0600). `--suggest-out` requires `--suggest`.
With no coverage hints, no file is created. Suggestion writing does not change the
check result: a failing suite still exits unsuccessfully.

The generated YAML references the original definition by absolute path and includes
root or item expectations for missing guard outcomes, failure handlers, wait
outcomes, retry attempts and loop collection cases. Comments provide starter JSON
for the referenced mock files. Input and mock paths start with `TODO-`; create
those files, replace `TODO_REPLACE` outputs, and adapt inputs, prerequisite mocks
and handler expectations before running the new suite. Guarded loops and waits
can require further adaptation; suggestions are not verified simulations.
Existing sample inputs, outputs and mocked errors are never copied. The current
suite and definition are unchanged.

A file contains at most 32 suggested scenarios, matching the suite limit. The
human summary and JSON `suggestions` object report the destination, generated
count and remaining hints. Rerun the completed suites and reassess coverage rather
than treating generated starters as proof that a path is covered. Loop action
coverage hints include a `loop` identity so suggestions can distinguish them from
root steps whose names contain `/action`.

### Require coverage before release

Coverage remains advisory by default. Opt in to a failing check or a publishing
gate when unexcluded hints remain:

```sh
gregale workflow stored check --app billing --scenarios scenarios.yaml --require-coverage
gregale workflow stored publish --app billing --name approval-flow \
  --expected-version 7 --scenarios scenarios.yaml --require-coverage
```

Publishing requires `--scenarios` with this flag. A failed gate stops before the
publication request; draft version checks and complete-trace requirements still
apply. `--suggest` can be combined with the check flag to write starters even when
the coverage gate fails.

Declare deliberate exceptions in the suite:

```yaml
coverage_exclusions:
  - step: approval
    code: wait_timeout_missing
    reason: Timeout behavior is covered by the integration suite.
  - step: sync/action
    loop: sync
    code: guard_skip_missing
    reason: This item branch is exercised by an external integration test.
```

Each exclusion must match a configured hint path and supply a nonblank single-line
reason (at most 512 bytes). Item-action exclusions require their `loop` identity;
root exclusions omit it. Unknown paths, misspelled codes and duplicates are
rejected before API calls. Exclusions remain valid if the path becomes covered,
but are rejected if a later definition removes that coverage path.

Human reports show exclusion reasons and a separate coverage gate result. JSON
reports include `coverage.required`, `coverage.passed`, `coverage.remaining` and
`coverage.exclusions`; checked publishing includes these under `checks.coverage`.
Original hints remain visible for review, while suggestion files omit excluded
paths. Exclusions never suppress assertion failures, invalid definitions,
simulation issues or incomplete traces. Coverage describes observed paths in
passing scenarios, not production behavior or exhaustive output assertions.

### Published check evidence

Publishing with `--scenarios` saves a server-verified check summary with the
immutable published revision. Use `gregale workflow stored revisions show` to inspect
its checked draft version, definition hash, check time, scenario results, coverage
status, and explicit exclusions. JSON output includes `check_evidence` when present.
The server independently evaluates the submitted assertions and coverage, then
validates its receipt against the saved draft inside the publication transaction.
Older client-reported evidence remains marked as unverified. Plain publication
without a receipt can record unverified evidence only when policy is optional.
Restoring a revision copies its definition into a draft; publishing that draft
requires fresh scenario checks to record new evidence.

The stored summary omits sample inputs, mock outputs, mocked errors, and assertion
failure messages. Scenario names and exclusion reasons are authored metadata and
should not contain secrets. Evidence accepts up to 32 passing, complete scenarios,
256 exclusions, and 128 KiB of metadata. Its check timestamp must be within the
past day, with five minutes allowed for clock skew. Advisory coverage gaps remain
visible; `--require-coverage` must pass before publication.

### Required publishing checks

Apps can enforce checks for dashboard publication. The default `optional` policy keeps plain publishing available. `scenarios` requires a server-issued receipt for complete, valid scenarios with passing assertions. `coverage` also requires all configured coverage paths to be observed or explicitly excluded with a reason. This policy covers dashboard stored workflows; YAML deployment remains a separate release surface.

Inspect and change the policy with:

```sh
gregale workflow stored policy publish --app billing
gregale workflow stored policy publish --app billing --mode coverage --expected-version 0
gregale workflow stored publish --app billing --name paid-invoice --expected-version 7 --scenarios scenarios.yaml
```

Changing the policy requires an admin credential and its current policy version. Publishing credentials with `deploy:write` can request checks and publish, but cannot weaken the policy. Checked CLI publishing also needs `apps:read` to read the draft and simulations. Changing a policy invalidates existing receipts, including when switching back to the previous mode.

Checked publishing reads and displays the app policy, enforcing mandatory coverage locally even without `--require-coverage`. It reports local assertion failures, then sends the preloaded suite to `POST /v1/apps/{slug}/workflows/stored/{name}/publish-check`. The server independently simulates the saved draft, evaluates root and per-item assertions (including exact JSON values and ordered attempts), and evaluates coverage. Every submitted scenario must contain at least one assertion. The policy overrides a client's request to skip required coverage. Invalid exclusions, incomplete traces, failed assertions and mismatched definitions produce no receipt.

Receipts expire after 30 minutes and are bound to the app, stored workflow name, draft hash and version, account/API-key identity and policy version. The server validates these bindings atomically with publication. Draft changes, publication, restore or delete/recreate make an earlier receipt stale. Each successful check replaces the previous receipt for that stored workflow and identity. HTTP publication retries continue to use the existing idempotency mechanism; a new publication cannot reuse a receipt against a later version.

Only the receipt hash and safe evidence metadata are saved with the receipt. Sample inputs, mocks, expected values and trace payloads are processed transiently and are not saved there. Published revision evidence uses `server_verified: true` when it came from a stored receipt; clients cannot set that flag on plain publication. Revision inspection labels trusted evidence as **Server-verified** and older evidence as **Client-reported**. Server verification establishes what the supplied simulations demonstrate; it does not execute or verify external service behavior. Keep scenario names and exclusion reasons free of secrets.

The check endpoint accepts at most 32 scenarios, 24 MiB total and 3 MiB per simulation, with the existing simulation/value/loop limits. A request cannot supply a definition different from the saved draft or override stored server evidence during publication.
