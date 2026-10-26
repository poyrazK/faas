# Build automations in Gregale

An automation runs steps against an app, with a manual, scheduled, or event start.
The app must have a live default deployment. Automations use the existing durable
workflow executor, with retries, dependencies, waits, and run history.

Create and manage definitions through the automation authoring API or generated
SDKs. Save a draft with a trigger and steps, including input mapping, event filters,
waits, and failure handlers as needed. Validate reports errors without executing
or saving steps; publishing makes the saved draft available to future runs.
The app dashboard also offers starter forms and an advanced JSON editor, described below.

## Start from the app dashboard

Open **Build and manage automations** in the app's Workflows section, or visit
`/dashboard/apps/APP_SLUG/automations`. Choose one of the same four starters
available through `gregale automations init`. Selection does not save a draft
or start a run. Deploy the starter's handler routes and configure its required
integrations or event sources separately.

1. Choose a unique automation name. Scheduled reports offer form fields for cron,
   timezone, collect/deliver paths, overlap and missed-start recovery. Other
   definitions use JSON; keep its name matched to the automation name. Validate
   the configured definition before saving.
2. Review the sample input and mock outcomes, then simulate. The trace shows step
   decisions without calling handlers or connected services.
3. Save the draft, then choose **Save test data** to retain the sample, mock
   outcomes, and named test cases. Reopening the automation restores that saved
   data, including for custom names. Draft saves alone keep test edits in the editor.
4. Review the scenario JSON and choose **Check and publish**. The server checks
   the saved draft and issues the receipt required by the app's publishing policy.
   Unsaved changes, stale revisions, failing scenarios or missing required
   coverage stop publication. Explicitly choose manifest takeover only when
   API-managed publication should replace the deployed definition's ownership.
5. After publication, confirm real side effects and choose **Run published
   automation**. Only the sample's `input` is submitted. Mock results never
   control real execution. Repeating the same form submission reuses its admitted
   run through the existing idempotency mechanism.

The page shows recent health, queue observations, run states, pinned deployments,
and step attempts. Run input/output bodies and raw errors remain behind explicit
API/CLI inspection. Run links are scoped to the selected app and automation.
A failed history read is reported as unavailable instead of an empty history.

Scheduled starters remain disabled after publication until **Enable automatic
starts** is selected. Manual runs remain distinct from that setting. The console
cannot enable a disabled workflow runtime, satisfy tenant admission requirements,
configure outbound worker flags, or establish native KVM qualification. The
existing permissions, publication policies and execution gates still apply.

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

The API exposes `PUT /v1/apps/{slug}/automations/{name}/tests` with
`{expected_version, suite: {sample, cases}}`; `suite: null` clears saved data.
`POST /v1/apps/{slug}/automations/{name}/tests/run` takes `{expected_version}`.
Automation reads return optional `test_suite` and `test_report` fields. Test
inputs omit definitions; runs bind the authoritative saved draft. The suite is
bounded to 32 uniquely named cases, 3 MiB per sample/simulation, 24 MiB for the
combined cases, and 27 MiB overall. Each case accepts up to 4,096 assertions;
reports retain up to 1,024 step summaries per case and indicate truncation.

### Verify one saved case against real execution

Live verification executes the **published** automation against the app's current
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

- `GET /v1/apps/{slug}/automations/{name}/live-verifications` for current target IDs
  and up to 20 recent verifications.
- `POST` to that collection with a required stable `Idempotency-Key` and
  `{expected_version, expected_published_version, expected_deployment_id, case_name,
  confirm_real_actions: true}` to admit one case.
- `GET /v1/apps/{slug}/automations/{name}/live-verifications/{run_id}` to read a
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

### Verify a deployed automation from CI

`gregale automations live` exposes the same saved-case verification through the
CLI. Authenticate the CI job with `FAAS_TOKEN`; use `FAAS_API` when selecting a
different control-plane endpoint.
Use `live list --app billing --name paid-invoice --json` to read the current
published revision, default deployment and recent verification IDs. Read the
saved automation version with `automations get`. Review the synthetic case and
its integrations before confirming real actions.

Start one case against explicitly selected target versions:

```sh
gregale automations live start --app billing --name paid-invoice \
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
gregale automations live wait --app billing --name paid-invoice \
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
gregale automations live suite --app billing --name paid-invoice \
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
gregale automations live suite --resume suite-progress.json \
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
ten minutes and reuse its exact ID and body after a lost response. An identical
retained start request reads progress without advancing it. POST
`live-suites/{suite_id}/advance` with `confirm_real_actions: true` to continue.
Each admission and its stored run link commit together; missing suites are never
recreated by continuation. Go, Node and Python expose both operations.

Suite history keeps at most 100 records per automation. Recent dashboard start
receipts are protected for the creation window so history churn cannot erase
recovery identity while the original creation request can still be admitted.
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
GET form sends only the automation and suite selectors. Clients can call:

```text
GET /v1/apps/{slug}/automations/{name}/live-suites/{candidate_id}/compare/{baseline_id}
```

Go `CompareAutomationLiveSuites`, Node `compareAutomationLiveSuites`, and Python
`compare_automation_live_suites` expose the same read. Both suites and their
linked runs are observed in one database snapshot. The response contains safe
per-case observations and disjoint counts, with at most 64 case rows; no inputs,
outputs, assertion values or admission keys are returned. Suite and verification
retention still apply, so a removed suite returns not found. This feature does
not enforce a CI or release gate.

### Gate CI on retained suite evidence

Use the read-only gate after explicitly running and checking a dashboard suite
or a shared CLI suite. Pin the intended candidate's complete target:

```sh
gregale --non-interactive automations live gate --app billing --name paid-invoice \
  --baseline 00000000-0000-4000-8000-000000000010 \
  --candidate 00000000-0000-4000-8000-000000000011 \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --json > automation-gate.json
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

The [GitHub Actions example](../examples/automation-ci/README.md) installs a
pinned CLI, reads existing suite evidence with an app-read token, preserves
nonzero exit codes and uploads the JSON artifact even when the gate fails. It is
opt-in and does not execute real actions. Use a CLI release containing this
command or build the current source. This observation does not enforce publishing
or deployment, impose a maximum age by default, or certify cases that were never
selected. Retention still applies to suites and their linked run records.

To require coverage even when both suites omit the same saved case, commit a
versioned JSON policy and add `--policy automation-gate-policy.json` to the gate:

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
an app and automation. It does not select an execution environment or change
routing. Promotion is a deliberate metadata write; it never starts workflows or
checks assertions. It requires a token with deploy-write scope. Reading the head,
history or gate requires app-read scope.

Discover existing names and their current evidence health with:

```sh
gregale automations live baseline list --app billing --name paid-invoice --json
```

The dashboard's **Named baselines** catalog shows each head, policy and target,
retained case results, renewal blockers and inspection links. **Prepare replacement
suite** navigates to the saved-suite controls; starting real work and promoting
replacement evidence still require their own confirmations. An empty catalog
means this automation has no named baselines. Missing retained evidence leaves
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
gregale automations live baseline promote --app billing --name paid-invoice \
  --baseline-name production --suite 00000000-0000-4000-8000-000000000010 \
  --promotion-id 00000000-0000-4000-8000-000000000020 --expected-revision 0 \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --policy automation-gate-policy.json --json

gregale automations live baseline show --app billing --name paid-invoice \
  --baseline-name production --json
gregale automations live baseline history --app billing --name paid-invoice \
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
gregale --non-interactive automations live gate --app billing --name paid-invoice \
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

There are at most 20 active and 100 lifetime baseline names per automation, and 100 retained promotion
receipts per name, newest revision first. Names use 1–63 lowercase ASCII letters,
digits or hyphens, beginning and ending with a letter or digit. Promotion requests
are at most 20 KiB; stored receipts at most 32 KiB. Promotion policy JSON must
fit the existing 16 KiB policy limit after canonical serialization as well as on input. Reuse an existing release-track
name instead of creating a new name for each build. Automation deletion removes
its history. Clearing saved cases retains audit receipts but removes evidence.
Active current baseline suite groups are protected from normal suite pruning within the
existing 100-group budget; linked run/report retention still applies. Replaced
groups can be pruned normally. Missing or invalidated evidence blocks gates;
receipts do not recreate or execute it.

### Share suite results with your team

Add `--share` when creating a new CLI suite to retain its selection and run links
in the app dashboard:

```sh
gregale automations live suite --app billing --name paid-invoice \
  --case happy --case expected-failure \
  --expected-version 7 --expected-published-version 4 \
  --expected-deployment-id 00000000-0000-4000-8000-000000000002 \
  --idempotency-key deploy-42-suite --journal suite-progress.json \
  --share --confirm-real-actions --json > suite-result.json
```

Open the automation and find **Shared live verification suites**. Select a suite
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

Shared history retains 100 recent suites per automation and lists the newest 20.
Older retained suites remain accessible by their suite ID. Workflow verification
records have their own retention; a suite does not extend their lifetime. Clearing
saved test data or deleting the automation also removes shared suite history.

Clients can save metadata with
`PUT /v1/apps/{slug}/automations/{name}/live-suites/{suite_id}`, read it with GET
of the same path, or list the collection. Choose a stable UUID and send
`expected_version`, `expected_published_version`, `expected_deployment_id` and
ordered `cases: [{name, run_id?}]`. Requests are capped at 16 KiB and 32 distinct
cases. Run IDs must reference existing verifications for the same frozen case and
target. Selection and existing run links cannot be replaced; older partial saves
preserve links already recorded. Saves never execute workflows or accept verdicts.
The metadata APIs are available in the Go, Node and Python clients.

### Configure and preview a scheduled report

Choose **scheduled-report**, enter its schedule and an IANA timezone, and replace
the collect/deliver paths with routes deployed by your app. Choose whether an
active run skips a new occurrence or allows overlap. Missed starts can be skipped
or recover only the latest occurrence within a bounded window (default 1h,
allowed 1m–24h); leave the window empty when using skip.

Select **Use report settings below** to apply these fields. They patch the
submitted definition while preserving advanced inputs, retries and other
options. Choose **Use the JSON editor** to edit those same fields directly.
Different step graphs remain available through JSON rather than being replaced
by a starter. New reports configured through the form have starts disabled.

**Preview edited schedule** validates the submitted definition and shows five
upcoming nominal times in local time with numeric UTC offsets and in UTC. It
uses the scheduler's shared timing helper, including daylight-saving rules.
It saves no draft, changes no durable cursor, enables no starts and calls no
handlers. A separate **Published schedule preview** reads the effective published
definition and its current cursor through the existing schedule preview API.
Unsaved edits do not change that preview or the published definition. Preview
times reserve no capacity and do not guarantee execution.

The **Setup checklist** reports observed runtime availability, execution
prerequisites, publication and enabled state. A successful prerequisite read
confirms app eligibility and a live default deployment at that observation.
When an earlier execution gate blocks the read, deployment readiness is shown
as unverified. Handler routes and credentials always remain unverified by this
page: mocked scenarios do not exercise a deployed handler. Test those routes
before explicitly enabling automatic starts. Save and checked publication still
require the submitted revision and definition to match the current saved draft.

### Configure a verified webhook automation

Choose **webhook-enrichment** to configure platform event source/type, event-filter
JSON and lookup/record handler paths through form fields. The editor preserves
advanced inputs and retries. New guided event starters publish with starts paused;
JSON authoring retains its existing activation semantics. Saving can retain an
incomplete draft; validation and publication checks still reject invalid execution
definitions. Select the JSON editor for different graphs or input mappings.

After publication, use **Connect a verified webhook** to select an existing Stripe
or generic signed inbound endpoint on the same app. Endpoint metadata never exposes
the one-time public routing token or signing secret. Create and enable endpoints
through the existing inbound webhook API or CLI; this page does not create them,
rotate secrets, change their enabled state or send a test event.

Review the displayed binding target and revision. Set its verified-webhook event
pattern/filter, confirm routing takeover, then choose **Save webhook binding**.
This replaces ordinary app delivery or the displayed automation binding for future
acceptance. A stale binding revision conflicts; repeated identical submitted binds
reuse their result through API idempotency. Failed actions require reloading and
confirming again. Callback/trigger ownership conflicts remain enforced by the API.

The platform event trigger and verified webhook binding are separate routes.
Verified ingress uses its endpoint's reserved event source and its binding's
pattern/filter, independent of the authored platform trigger. Input data is the
signed JSON body for generic endpoints, or the full Stripe event for Stripe
endpoints, wrapped in a CloudEvents envelope. Review mappings for the chosen
provider: a generic order ID might be `{{input.data.order_id}}`, while a Stripe
object ID might be `{{input.data.data.object.id}}`.

Enable automatic starts explicitly when ready. An enabled endpoint bound to an
enabled automation can perform real actions immediately on matching deliveries.
Pausing the automation makes future verified events receive ignored receipts.
**Remove webhook binding** has its own confirmation and revision check, restores
ordinary delivery for future events, and does not cancel accepted work. Bindings
target a published automation name, so later publications affect future events;
already accepted definitions and filters remain captured.

Enter a **Provider event ID** to inspect acceptance, ignored reason and routing
status. A resulting run link appears only after verifying its app and automation
identity. Receipts from another automation are not shown. Read failures are reported
as unavailable or not found, rather than as successful acceptance. Provider retries
reuse the original receipt and accepted work; receipt lookup does not retain retry
counts or delivery history. Real payloads and signing material stay out of this view.
Native execution qualification remains separate from this dashboard acceptance.

### Diagnose and recover a dashboard run

Open a run from **Recent runs** to see its observed queue/wait reason, next wake,
expired worker lease, deployment pin, and step kinds. Select a step to inspect
its attempt states, HTTP statuses and timing. Attempt errors and managed-operation
effect records are excluded from this page. Failed diagnostics or history reads
are reported as unavailable, rather than as an eligible or empty result.

The **Recovery preview** lists steps to reopen, steps to preserve and current
blockers. When eligible, review that list, confirm that reopened steps may perform
real actions again, and choose **Resume failed run**. The existing API rechecks
eligibility, admission requirements and the preview's continuation count.
Concurrent or stale requests cannot admit a second continuation. Repeating the
same submitted form replays its original result through API idempotency.
An unsuccessful action requires refreshing the run and confirming again.

**Recovery history** shows continuation numbers, previous states, reopened step
names and timestamps. It omits historical errors and account identifiers.
Successful results and prior attempts remain retained by the workflow runtime;
resuming keeps the captured definition, inputs and deployment routing rules.

For an active run, expand **Cancel this run**, confirm, and choose **Cancel run**.
Cancellation applies to that run's current state, including a continuation that
may have been admitted since the page was loaded. Completed actions are not
undone, and active work may need time to stop. A completed run is left unchanged
by the existing cancellation API. Cancellation does not require the execution
runtime to be enabled. Resume and cancellation require write permission,
verified email, the app-bound CSRF envelope and a separate action confirmation.
The run's app and automation identity are checked before requesting diagnostics,
history, attempts or a mutation. These dashboard controls do not open native
Customer Workflow Operations admission or establish KVM qualification.

## Start from a template

Create a local starter without logging in:

```sh
gregale automations init --list
gregale automations init --template scheduled-report --path daily-report
```

The destination must be a new directory with an existing parent. The command
refuses existing directories and creates private files: `automation.yaml`,
`sample-input.json`, `mock-outputs.json`, `mock-attempts.json`, `scenarios.yaml`,
and `README.md`.
It does not save or publish an automation.

| Template | Behavior | Required setup |
| --- | --- | --- |
| `webhook-enrichment` | Enrich an incoming order with customer details | Deploy lookup/record handlers and configure a verified webhook binding |
| `scheduled-report` | Collect and deliver a weekday report | Deploy report handlers, choose a timezone, and explicitly enable the initially disabled schedule |
| `contact-sync` | Read a provider contact and synchronize it into the app | Replace the placeholder integration UUID with a real app-bound integration and deploy the sync handler |
| `approval-flow` | Request approval, fulfill an approved order, or expire the request | Deploy handlers and deliver approval events from an authorized service |

Follow the generated README setup instructions before validating against an app.
Validation and simulation use your configured account and app; contact sync also
requires valid integration bindings even when provider responses are mocked.
From the generated directory:

```sh
gregale automations validate --app APP_SLUG --file automation.yaml
gregale automations simulate --app APP_SLUG --file automation.yaml \
  --input-file sample-input.json --mock-outputs-file mock-outputs.json \
  --mock-attempts-file mock-attempts.json --require-complete
```

The approval starter also includes `mock-approval-outputs.json` and
`mock-approval-attempts.json` for its successful branch. Use those two files
instead of the default mocks to preview fulfillment. Fulfillment requires an
`approved_by` field in the approval event. Live event delivery and authorization
still need to be checked separately.
Simulation checks sample control flow and mappings, without executing handlers.
After reviewing the trace, follow the README instructions to apply and publish.

## Check reusable scenarios

Run the supplied starter scenarios together:

```sh
gregale automations check --app billing --scenarios scenarios.yaml
```

A suite references one definition and sample files. All file paths are resolved
relative to the suite file, so the command can run from another directory.
The command requires login and simulation read scope for the app. Integration
bindings are checked by the server as in ordinary simulation. It creates no runs
and does not save or publish definitions.

```yaml
definition: automation.yaml
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
gregale automations publish --app billing --name approval-flow \
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
`published`, and an automation summary on success. The outcome is `not_attempted`
when checks or the draft recheck block publication, `confirmed` on success,
`rejected` for definitive validation, authorization or conflict responses, or
`unknown` when a transport or server failure prevents confirmation. API failures
include `publication_http_status` and `publication_error_code` without service
detail or sample values. Failures after simulation include an `error`; malformed
suites, initial fetch failures, and initial draft mismatches use ordinary CLI
errors. A publication transport failure can leave the outcome uncertain:
`publication_outcome: unknown` means publication was not confirmed. Inspect the
automation status before retrying. HTTP transport replays retain the same
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
even if run admission waits for capacity. Publishing an automation changes future
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


For example, save an event automation through the API:

```http
PUT /v1/apps/billing/automations/paid-invoice
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

Set `max_concurrent_runs` to cap active instances of one automation. Omit it or
set it to `0` to use only the app plan limit; valid explicit caps are 1–200.
Runs waiting in the queue have status `pending` and have not started. Started
runs keep their slot through retries and event/callback waits. Pending queue size
remains bounded by the plan's total active-run quota.

For scheduled automations, `overlap: "allow"` admits each due occurrence and
queues it when the per-automation cap is full. `overlap: "skip"` keeps its
existing behavior and records a skipped occurrence instead. Event and manual
starts queue by default when a workflow's concurrency cap is full, provided
the app still has plan quota. The cap is part of the run's definition snapshot;
publishing a new cap affects newly admitted runs.

Set `max_concurrent_actions` to cap active app-handler, managed outbound, and
condition-check calls across runs of the same automation. Omit it or set it to
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
gregale workflows run --app billing --input '{"invoice_id":"inv_42"}' \
  --idempotency-key invoice-paid-inv_42 paid-invoice
```

The API accepts the same header on `POST /v1/apps/{slug}/workflows/{name}/runs`.
The Go, Node, and Python SDKs expose the optional header; the CLI flag is useful
when a script needs to repeat a start request after an uncertain result.

Use the returned `version` in the publish request:

```http
POST /v1/apps/billing/automations/paid-invoice/publish
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
again. Drafts do not change the published automation. Definition quotas count
YAML and saved draft names together, with the plan's existing workflow limits.

## Author automations with the CLI

The CLI accepts the same definition as YAML or JSON. Validate it before saving a
draft, then pass the version returned by each write to the next command:

```sh
gregale automations list --app billing
gregale automations get --app billing --name paid-invoice
gregale automations health --app billing --name paid-invoice
gregale automations pause --app billing --name paid-invoice --expected-version 8
gregale automations resume --app billing --name paid-invoice --expected-version 9
gregale automations get --app billing --name paid-invoice --definition-out paid-invoice.json
gregale automations get --app billing --name paid-invoice --definition-out published.json --published
gregale automations revisions list --app billing --name paid-invoice
gregale automations revisions show --app billing --name paid-invoice --revision 42
gregale automations validate --app billing --file paid-invoice.yaml
gregale automations apply --app billing --file paid-invoice.yaml --expected-version 0
gregale automations publish --app billing --name paid-invoice --expected-version 1
gregale automations restore --app billing --name paid-invoice --revision 42 --expected-version 47
gregale automations delete --app billing --name paid-invoice --expected-version 48 --yes
```

List and get show the current version, publication version, ownership, and enabled
state without returning definition contents. Definition export is explicit: use
`--definition-out` to write the draft as JSON to a new file, or combine it with
`--published` to export the published definition. Exported files use mode `0600`,
and the command will not overwrite an existing path. Machine-readable get/list
output remains metadata-only.

Use `--expected-version 0` only when creating a new draft. For an existing
automation, use its current version from the most recent response or API read. A
version conflict means another writer changed the draft; reload it before
retrying. Publishing is a separate step. If the name is currently owned by the
app's YAML manifest, pass `--take-over-manifest` only when you intend the saved
automation to take ownership.

Revision listing defaults to 50 entries and accepts `--limit` (1..100) and
`--offset` for paging. Revision details show the timestamp, definition hash, and
whether the snapshot was migrated from legacy publication history. Definitions
stay out of normal terminal and JSON output; `revisions show --definition-out`
exports one to a new mode-`0600` JSON file. `automations restore` requires the
current `--expected-version` and saves the selected snapshot as a new draft. Use
`0` if the automation was deleted. The restore command does not publish; validate
and publish the new draft separately.

Delete an automation with its current version and explicit `--yes` confirmation.
If the published automation took over a YAML-owned name, include
`--restore-manifest` to allow the current YAML definition to own that name again:

```sh
gregale automations delete --app billing --name paid-invoice --expected-version 48 --yes --restore-manifest
```

Pause automatic starts to stop future scheduled/event admissions. Existing runs
and already accepted events continue. Republishing preserves pause. Resume to
start new automatic admissions again; schedules skip missed minutes and re-arm
at the next evaluation. Manual sample runs remain possible while paused and
execute the published definition with real app side effects.

Use `automations pause` and `automations resume` for a published automation.
Both require the current `--expected-version`; a successful change advances the
automation version but does not publish a definition or add a revision. Read the
automation again before retrying after a version conflict.

## Find workflow runs

The CLI can narrow an app's run history by exact workflow name, status, and
inclusive creation-time bounds:

```sh
gregale workflows list --app billing --workflow-name paid-invoice --status failed
gregale workflows list --app billing --created-after 2026-10-01T00:00:00Z \
  --created-before 2026-10-05T23:59:59Z --limit 50
```

Timestamps must use RFC3339, and `--created-after` must not be later than
`--created-before`. The CLI keeps the existing 50-run page size and supports
`--limit` up to 100 plus `--offset` for paging. The same filters are available
through the API:

```http
GET /v1/apps/billing/workflows/runs?workflow_name=paid-invoice&status=failed&created_after=2026-10-01T00:00:00Z&created_before=2026-10-05T23:59:59Z&limit=50
```

The filtered `total` and page use the same criteria. Results remain ordered newest
first; `limit` and `offset` keep their existing behavior. Go, Node, and Python
clients expose these filters. No dashboard editor is included.

## Cancel queued workflow runs

Pending runs can include a retry or a previously parked run, so use
`started_at` to identify work that has never begun. Preview only the run IDs you
intend to cancel, then repeat that exact selection with the explicit action:

```sh
gregale workflows list --app billing --workflow-name paid-invoice --status pending
gregale workflows cancel-queued-preview --app billing --workflow-name paid-invoice \
  --run-id <run-id>
gregale workflows cancel-queued --app billing --workflow-name paid-invoice \
  --run-id <run-id> --yes
```

You can select up to 20 runs per request by repeating `--run-id`. The optional
`--workflow-name` guards the selection against a name mismatch. Only pending
runs with no `started_at` value are eligible. The preview is advisory; the
action rechecks each run while holding the dispatcher claim lock. If dispatch
claims a run after preview, that run is reported as `already_started` or
`not_queued` and is left alone. A previously cancelled run is reported as
`already_cancelled`. Started runs, including retries and parked
waits, continue to use the single-run `workflows cancel` command.

The action commits eligible cancellations as one bounded batch. Outcomes are
returned for every selected ID; an eligible run becomes `failed` and receives
`cancelled_at`. Missing IDs and runs outside the selected app share the
`not_found` outcome.

The API exposes the same flow through
`POST /v1/apps/{slug}/workflows/runs:cancel-preview` and
`POST /v1/apps/{slug}/workflows/runs:cancel-queued` with a JSON body containing
`run_ids` and optional `workflow_name`.

## Inspect automation health

Get a bounded operational summary for one saved automation from the CLI, or use
the API directly:

```sh
gregale automations health --app billing --name paid-invoice
gregale automations health --app billing --name paid-invoice \
  --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z
```

The CLI accepts RFC3339 bounds and prints run totals, current active and queued
counts, success rate, p50/p95 durations, latest run/success/failure IDs, and the
most frequently failed steps.
Use `--json` for the bounded machine-readable response. The summary omits run
inputs, outputs, and errors.

The API endpoint is:

```http
GET /v1/apps/billing/automations/paid-invoice/health?created_after=2026-10-01T00:00:00Z
```

The default window is the previous seven days, and callers can select at most
30 days with inclusive RFC3339 `created_after` and `created_before` timestamps.
The response includes run counts by status, current `active_run_count` and
`queued_run_count` (independent of the requested time window), success rate, p50/p95 durations,
the latest run/success/failure, and up to ten frequently failed logical steps.
Failures from individual loop items are grouped under their parent step. Run
inputs, outputs, and error messages are never included. Go, Node, and Python
clients expose this endpoint.

The optional `queue` object reports current waiting reasons, independently of
the historical window. The CLI prints its observation time, app dispatch
occupancy and limits, waiting/due/stale counts, oldest due age, and nonzero
reason counts. App occupancy includes all automations and tenants in the app;
waiting counts belong to the selected automation. Live running claims consume
dispatch capacity; parked waits and pending retries can still consume a
workflow's `max_concurrent_runs` budget.

| Queue reason | Meaning at observation |
| --- | --- |
| `ready` | Due and passes the app, tenant and workflow run limits |
| `scheduled` | Its next scheduling deadline is in the future |
| `retry_backoff` | Waiting for a pending step's retry deadline |
| `parked_wait` | An intentional wait has a future wake or timeout |
| `app_capacity` | The app's live dispatch claims fill its budget |
| `tenant_capacity` | That tenant's live claims within the app fill its budget |
| `workflow_capacity` | The run's captured workflow concurrency limit is full |

Each waiting run contributes to one reason. Future deadlines take precedence,
followed by app, tenant and workflow limits. Due timers, event/callback timeouts
and stale leases are included in admission checks; expired leases do not occupy
capacity. Oldest due age includes blocked work and starts at eligibility rather
than an intentional wait's start. The reason counts sum to `waiting_run_count`,
which includes started pending retries and parked waits as well as fresh runs.

`ready` describes dispatch admission. Fair turns, runtime gates, action budgets
and handler invocation limits can still delay execution. `parked_wait` groups
timers, conditions, events and callbacks; inspect the run's steps for details.
The snapshot does not estimate queue position, completion time or global worker
saturation. Older servers may omit `queue`; the CLI reports diagnostics as
unavailable. No customer payloads, errors or tenant identities are returned.

The preview runtime requires `FAAS_WORKFLOWS_ENABLED=1` on apid and schedd and the
existing gateway executor configuration. The list response reports when runtime,
plan, app maintenance, tenant requirements, or missing deployment prevents
execution. Saving and validating drafts alone does not enable the runtime.

## React to finished workflow runs

Subscribe an app webhook to `workflow.finished` to notify your service or an
external automation tool when a durable run succeeds, fails, or reaches the dead
state:

```http
POST /v1/apps/billing/webhooks
Content-Type: application/json

{
  "target_url": "https://automation.example.com/gregale",
  "webhook_secret": "replace-with-a-random-secret",
  "event_filter": ["workflow.finished"],
  "delivery_format": "cloudevents"
}
```

The event is committed with the terminal status change and delivered through
the durable webhook outbox. A resumed run emits another event if it later
finishes; use the CloudEvent ID or webhook delivery ID to deduplicate retries.
The payload includes `app_id`, `run_id`, `workflow_name`, `status`,
`finished_at`, and `resume_count`. It omits run input, output, and error text.
Use `GET /v1/workflows/runs/{id}` with an authorized API key to fetch run
details. The webhook event filter is app-scoped; account-wide release
subscriptions do not receive workflow outcomes.

## Review and restore published revisions

Every successful publish appends an immutable revision containing the definition,
publication version, recording time, account, and API key when key authentication
was used. Saving drafts and pausing or resuming automatic starts do not create
history entries. The response includes `definition_hash`, the SHA-256 hash of the
canonical JSON encoding of that revision's definition.

List revisions newest first, with up to 100 entries per page:

```http
GET /v1/apps/billing/automations/paid-invoice/revisions?limit=50&offset=0
```

Read one immutable definition with
`GET /v1/apps/{slug}/automations/{name}/revisions/{version}`. Restore it as a new
draft using the current automation version from the latest read:

```http
POST /v1/apps/billing/automations/paid-invoice/revisions/42/restore
Idempotency-Key: restore-paid-invoice-42
Content-Type: application/json

{"expected_version": 47}
```

Restoring does not publish the definition or alter runs and accepted events.
Validate and publish the restored draft separately; a YAML-owned name still
requires explicit takeover at publish time. If the automation was deleted, use
`expected_version: 0` to restore its draft. Draft updates remain protected by
optimistic version checks.

When history is introduced, Gregale seeds the latest available publication for
each existing API-owned automation as a `legacy_snapshot`. Its `recorded_at` is
the migration time, and earlier definitions and the original publication time
cannot be recovered. Future publishes are recorded individually. Go, Node, and
Python clients expose list, get, and restore methods. No dashboard editor is
included. Export the history before rolling this migration back; its down
migration removes the retained revisions.

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
        source: automation
```

Replace the example UUID with your managed integration ID. Path templates must
occupy an entire segment. Query keys are fixed; values can contain templates.
References to `steps.<name>.output` require that step in `depends_on`. Loop
actions can use `input.item`, `input.index`, and `input.input` for the original
run input. GET and HEAD carry no request body.

Simulate with `--input-file` before publishing. With
`{"contact_id":"contact 42","email":"a+b@example.com"}`, the CLI shows
`GET /v1/contacts/contact%2042?email=a%2Bb%40example.com&source=automation`.
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
Origins and credentials stay managed by outboundd, and existing provider
idempotency requirements apply to mutating retries. Simulation shows sample
URLs; runtime route authorization and provider behavior still require real
execution.

### Supply sample inputs and mocks

Run the same simulation from the CLI before saving or publishing. Provide the
definition, sample input, and action mocks as separate files:

```sh
gregale automations simulate --app billing --file paid-invoice.yaml \
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
`POST /v1/apps/{slug}/automations:simulate`. Simulation needs read scope and
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
Go `Client.SimulateAutomation`, Node `WorkflowsService.simulateAutomation` and
Python `workflows.simulate_automation` expose this API. No new migration is needed.
See [ADR-575](adr/575-automation-simulation.md).

## Conditional actions

Add `when` to a step to run it only when workflow data matches a predicate:

```json
{
  "name": "remind-overdue-customer",
  "steps": [
    { "name": "lookup", "run": "lookup_customer" },
    {
      "name": "remind",
      "run": "send_reminder",
      "depends_on": ["lookup"],
      "when": { "ref": "steps.lookup.output.overdue", "op": "eq", "value": true }
    },
    {
      "name": "receipt",
      "run": "send_receipt",
      "depends_on": ["lookup"],
      "when": { "not": { "ref": "steps.lookup.output.overdue", "op": "eq", "value": true } }
    }
  ]
}
```

Guard references use `input.field` or `steps.NAME.output.field`, without
`{{ }}`. Managed integration responses use `steps.NAME.output.body.field`.
The referenced step must appear in `depends_on`. Array paths use indexes such
as `input.items.0.amount`. Comparisons use literal values; strings are not
templates. For explicit field absence use
`{"ref":"input.email","op":"exists","value":false}`.

Use `eq`/`ne` with a string, number, boolean or null. Numeric operators
`gt`, `gte`, `lt`, and `lte` require a number. Types are preserved: `"1"` differs
from `1`. Missing fields fail comparisons, including `ne`, while null is a
present value. Combine nonempty predicate lists with `all` (AND) or `any` (OR),
or negate one predicate with `not`. Negation also applies to missing-field
results; use an existence predicate alongside comparisons when needed.

The first decision is durable across retries and recovery. A false decision
skips the step before any call, timer, callback wait or checker starts. Ordinary
steps that depend on a skipped step are skipped too. Use a native join for a shared
if/else continuation (below). Guards cannot be placed on failure or timeout
handler targets or native joins.

Inspect steps through `GET /v1/workflows/runs/{id}/steps`. Guarded steps expose
`when_matched` and `when_evaluated_at`; skipped steps expose `skip_reason`.
Reasons contain no referenced values. A false guard consumes zero attempts.
Descendants skipped by a dependency have no guard decision of their own.

Each guard permits 32 predicate nodes, eight levels and 16 KiB. Compared
numbers permit 4096 bytes and exponent magnitude 4096. Invalid definitions
fail validation; invalid runtime numbers fail the step without running its
action. Apply the guard migration before deploying apid/schedd and publishing
definitions. See [ADR-490](adr/490-declarative-workflow-step-guards.md).

## Shared branch continuations

A native `join` brings conditional branches back into one shared continuation.
The [branch join example](../examples/branch-joins/gregale.yaml) contains a full
manifest. Add these steps after `remind` and `receipt` in the example above:

```json
[
  {
    "name": "merge",
    "depends_on": ["remind", "receipt"],
    "join": { "output_from": ["receipt", "remind"] }
  },
  {
    "name": "update-crm",
    "run": "update_crm",
    "depends_on": ["merge"],
    "input": {
      "branch": "{{steps.merge.output.source}}",
      "result": "{{steps.merge.output.value}}"
    }
  }
]
```

The join waits for every branch to finish. It accepts succeeded branches and
branches skipped by false guards, including skipped descendants of those
branches. If several branches succeed, `output_from` selects the first listed
success, regardless of completion order. Every direct dependency must appear
exactly once in that list; joins permit 2-128 dependencies.

The stored output is `{"source":"receipt","value":<receipt output>}`. Empty
successful outputs become null. Reference fields with existing templates such
as `{{steps.merge.output.value.customer_id}}`; arrays and scalar values retain
their types. The selected output persists across retries and recovery. Referencing
a skipped branch directly still fails; use the join's selected output instead.

If all branches are conditionally inactive, the join and its continuation are
skipped. Failed/cancelled branches, failure ancestry and skips without a known
conditional cause never activate the continuation. Joins wait for unfinished
ancestors even if an inactive descendant has already been skipped.

Joins consume zero execution attempts. Inspect the selected `source` and `value`
through the existing step output API. Put guards, input mappings, retries and
exception routes on action steps; those options cannot be configured on a join.
Joins cannot be or depend directly on failure/timeout handlers.

No additional migration is needed beyond the existing guard migration. Deploy
updated apid and schedd before publishing joined definitions. Before downgrading,
pause starts, drain/cancel joined runs and replace joined definitions. See
[ADR-491](adr/491-native-workflow-branch-joins.md).

## Process a list

Use `for_each` to apply one action to every item in a JSON array. The
[batch example](../examples/foreach/gregale.yaml) contains a full manifest:

```json
{
  "name": "send",
  "for_each": {
    "items": "input.recipients",
    "max_parallel": 4,
    "on_item_failure": "continue",
    "action": {
      "run": "send_receipt",
      "input": {
        "recipient": "{{input.item}}",
        "position": "{{input.index}}",
        "invoice_id": "{{input.input.invoice_id}}"
      },
      "timeout": "30s",
      "retry": { "max_attempts": 3, "backoff": "exponential" },
      "when": { "ref": "input.item.active", "op": "eq", "value": true }
    }
  }
}
```

Gregale snapshots the array and prepares every mapped input before executing the
first item. `max_parallel` limits active item calls to 1–16; omit it or set it to
`0` for sequential execution. Items are admitted in input order within a bounded
window, while collected results always keep their original order. Retried and
recovered items reuse their inputs, and completed items stay complete. Without an
action `input`, the handler receives the item itself. Explicit mappings read
`input.item`, the zero-based `input.index`, and `input.input` for the original run
input. They may also read outputs of the parent's direct dependencies. Data
containing template delimiters stays literal; it is never evaluated again.

`items` is a reference without braces, such as `input.recipients` or
`steps.lookup.output.recipients`. A referenced step must be in the parent's
`depends_on`. A missing or non-array source fails before any action runs.
The action accepts exactly one `run`, `path` or managed `outbound` target and may
specify input, method, timeout, retry and an optional `when` guard. The guard is
evaluated once per item against `input.item`, `input.index` and `input.input`, and
may read outputs of the parent's direct dependencies. A false guard skips that
item without making a handler call. Its position is preserved as `null` in the
parent output. A parent guard can still skip the whole batch. Nested loops,
per-item dependencies, waits, joins and exception routes are not supported.

Downstream steps depend on `send` and read `{{steps.send.output}}`. An empty list
succeeds with `[]`. Successful non-JSON handler responses become JSON strings;
empty responses become null. By default processing stops on the first item
failure, retains the completed prefix and skips remaining items. Set
`on_item_failure` to `continue` to attempt the remaining items; Gregale then
returns an input-order array with `null` for guarded and unsuccessful positions, and
marks the parent failed after all items finish if any item failed, or dead if
any item is dead. Downstream
actions requiring the batch do not run after a failed parent. Resuming a failed
batch retries failed items while completed items remain complete.

With `max_parallel` above `1`, an item failure stops Gregale from starting further
items under the default policy. Calls that were already active are allowed to
finish before the parent is marked failed. With `on_item_failure: continue`, the
batch keeps admitting remaining items up to the configured limit.

Each item has a stable Idempotency-Key across retries and recovery. App handlers
must deduplicate that key for side effects: an interrupted request can execute
again. For managed integrations, declare provider idempotency support only when
the operation honors the key. An unknown mutating result without that support
fails the batch. Credentials, route permissions and revocation remain enforced
by outboundd.

Each batch permits 128 items. Parent names permit 64 UTF-8 bytes. The source
array and total prepared inputs each permit 1 MiB; collected output permits
1 MiB. The `_foreach.` name prefix is reserved for internal items. Exceeding
input limits causes no item calls; exceeding the output limit stops processing
after the response that exceeded it.

Inspect the parent and individual items with the existing steps endpoint.
Parents expose `for_each_count`; items expose `for_each_parent` and
`for_each_index`, plus input, output, status and attempts. Use the returned item
name with the existing attempts endpoint to inspect retries. Parents consume
zero execution attempts.

Apply the iteration migration before deploying apid, schedd and outboundd.
Before downgrade, pause starts, drain/cancel runs and replace iteration
definitions. Migration rollback removes item and item-attempt history; export it
first if needed. See [ADR-572](adr/572-bounded-workflow-iteration.md).

## External service actions

An `outbound` step calls an existing managed integration bound to the automation's
app. Use its canonical UUID and a relative path within the integration's allowed
route. A whole path segment can reference run input or an earlier step output;
the resolved value is URL-escaped as one segment. Static query keys can have
templated scalar values, which Gregale URL-encodes. For example:

```json
{
  "name": "update-crm",
  "outbound": {
    "integration_id": "00000000-0000-0000-0000-000000000001",
    "method": "POST",
    "path": "/v1/contacts/{{input.contact_id}}",
    "query": { "source": "{{input.source}}" },
    "idempotency_supported": true
  },
  "input": { "email": "{{input.data.customer.email}}" },
  "retry": { "max_attempts": 3, "backoff": "exponential" }
}
```

Replace the example UUID with your bound integration. Set
`idempotency_supported` only when that provider operation deduplicates the
Idempotency-Key header. Mutations without it have one attempt; an uncertain
result after a crash is terminal. GET/HEAD may retry and send no request body.
Workflow input and declared dependency outputs can be used in path segments and
query values. Gregale escapes every value and checks the resolved path against
the integration and app binding permissions at execution time. Permissions and
revocation apply on every call. Customer definitions still cannot select an
arbitrary URL, origin, or authentication header.

Successful output is `{ "status": 200, "body": { ... } }`. Later steps can use
`{{steps.update-crm.output.body.id}}`. Failures expose HTTP status and a bounded
error without saving the provider error body. Read runs, steps, and attempts
through the existing workflow inspection APIs.

Outbound execution additionally requires `FAAS_WORKFLOW_OUTBOUND_ENABLED=1` on
schedd and outboundd, an active cluster signing key, and the attempt-token
migration. See [ADR-489](adr/489-workflow-outbound-integration-steps.md) for rollout
and recovery semantics. These flags are independent of saving draft intent.

## Start from a Stripe webhook

Create a signed Stripe inbound endpoint and publish an automation on
its app. Bind the endpoint to that published name:

```http
PUT /v1/apps/billing/inbound-webhooks/ENDPOINT_ID/automation-binding
Idempotency-Key: bind-paid-invoice
Content-Type: application/json

{
  "expected_version": 0,
  "take_over_delivery": true,
  "workflow_name": "paid-invoice",
  "event_type": "invoice.paid",
  "filter": {"data": {"data": {"object": {"amount_paid": {"$gt": 0}}}}}
}
```

Use the returned `version` when changing the binding. Event types accept the
existing edge wildcard syntax, such as `invoice.*`. Filters use the existing
event filter language against the canonical event envelope. Inspect with GET
at the same path; DELETE requires `?expected_version=VERSION` and restores
ordinary app delivery for future events.

Binding requests allow 64 KiB, including a filter of at most 32 KiB. Workflow
names allow 128 bytes; event patterns and provider event IDs allow 256 bytes.
The Go, Node and Python clients expose binding and receipt methods.

Binding explicitly takes over the endpoint's app-delivery path. An endpoint
cannot also have callback or managed-operation bindings; remove those first.
The target must be published on the same app. A binding may add a webhook start
to a manual, scheduled or event automation. The original definition is preserved
in the accepted run. No frontend configuration changes are included.

Gregale verifies the exact Stripe body before storing a receipt and durable
workflow start intent. The workflow input is a CloudEvents envelope whose
`data` is the complete Stripe event. For example, the Stripe object ID maps with
`{{input.data.data.object.id}}`. The source is
`gregale.inbound.stripe.ENDPOINT_UUID`, the type is Stripe's event type, and
`id` is its provider event ID. The event publishing API reserves this source;
customers cannot use it to forge verified ingress. Signing secrets are never
included in the input.

Provider retries return the original receipt and do not start another run.
Different content for the same endpoint/event ID conflicts. Retries still use
the original receipt after binding removal; retained events accepted through
ordinary app delivery before binding keep that app delivery. Deduplication
lasts for the plan's [settled-receipt window](event-driven.md#settled-receipt-retention). Side effects
inside steps still require the normal action idempotency contract.

Paused/unpublished targets and nonmatching event types receive an `ignored`
receipt. Content filters are evaluated by schedd: a mismatch reports
`routing_status: filtered` and starts no run. Pausing or editing a definition
affects future events; already accepted definitions and filters stay captured.
The workflow runtime, account/app eligibility and live deployment must be
available for new acceptance. Integration bindings remain validated. A
runtime/configuration outage returns 503 without accepting new work.

Inspect `GET /v1/apps/billing/inbound-webhooks/ENDPOINT_ID/automation-receipts/STRIPE_EVENT_ID`
for the receipt, ignored reason, routing status and admitted `run_id`. Use the
existing workflow run/step APIs for execution history. Quota pressure uses the
existing bounded event fanout retry and failure replay APIs; look up failures
with the receipt's `event_source` and `provider_event_id`.

Apply `20261003200000001_workflow_webhook_starts.sql` and update all apid and
schedd workers before creating bindings. Before downgrade, stop new ingress,
drain accepted webhook fanout, export bindings/receipts and remove bindings
before stopping updated binaries and rolling the migration back. See
[ADR-574](adr/574-verified-webhook-automation-starts.md).

## Start from a generic signed webhook

Use a generic inbound endpoint when the sender is not Stripe. Create it with
`provider: "generic"` and a random secret containing at least 32 bytes, then
bind it to a published automation using the same automation-binding API above.
The one-time endpoint URL is the public routing capability; the signing secret
is sealed at rest and can be rotated through the endpoint update API.

Send a JSON request with these headers:

```text
X-Gregale-Event-ID: order_123_paid
X-Gregale-Event-Type: order.paid
X-Gregale-Timestamp: <unix-seconds>
X-Gregale-Signature: sha256=<lowercase hex HMAC-SHA256>
```

Compute the signature over the exact request bytes using the endpoint secret:

```text
HMAC-SHA256(secret,
  timestamp + "\n" + event_id + "\n" + event_type + "\n" + raw_json_body)
```

The timestamp is Unix seconds and must be within five minutes of Gregale's
clock. Event IDs must contain 1–256 visible ASCII bytes with no spaces. Event
types must match `^[a-z][a-z0-9_.]{0,255}$`. The ID, type, timestamp and raw
body are covered by the signature. Re-sign each retry with a fresh timestamp
while reusing the same event ID and content; changing content or type for a
retained ID returns 409. Deduplication follows the plan's
[settled-receipt window](event-driven.md#settled-receipt-retention).

An accepted automation run receives a CloudEvents envelope with source
`gregale.inbound.generic.ENDPOINT_ID`, the signed event ID and type, and the
complete submitted JSON as `data`. The durable receipt and existing event
fanout handle retries and filters. Without an automation binding, Gregale
delivers the verified JSON to the configured app path and includes the verified
event metadata in `x-gregale-webhook-*` request headers.

Apply migration `20261006062359228_generic_inbound_webhook.sql` before creating
generic endpoints. The down migration is forward-only; remove generic endpoints
before rolling back application binaries.

## Preview scheduled fire times

Inspect a deployed schedule before relying on its cadence or catch-up behavior:

```sh
gregale workflows schedules preview --app billing --workflow nightly
gregale --json workflows schedules preview --app billing --workflow nightly --count 8
gregale workflows schedules preview --app billing --workflow nightly \
  --at 2027-03-28T00:00:00Z --since 2027-03-27T00:00:00Z
```

The preview uses the effective schedule and durable evaluation cursor. `--at`
simulates an evaluator time; `--since` simulates the prior evaluation time so
you can see how a delayed scheduler would handle missed fires. Both accept
RFC3339 timestamps. The preview reports upcoming fires in the schedule's IANA
timezone with numeric UTC offsets. Fixed wall times shifted into a spring DST
gap run at the first valid minute; repeated fall-fold wall times run once at
their first occurrence. Interval expressions follow cron interval behavior as
the local clock changes, so their fall-fold intervals can repeat.

The catch-up result distinguishes a current fire, coalescing to the latest
eligible fire, skipped missed fires, and fires outside the configured recovery
window. A new or changed schedule arms its durable cursor on the next evaluator
pass before admitting a later occurrence. The preview changes no cursor and
starts no workflow; it does not reserve quota or promise worker availability.
Its result is an observation, so live schedule edits or cursor advancement can
change the next decision.

The account route is
`GET /v1/apps/{slug}/workflows/schedules/{name}/preview`; tenant-bound callers
use `GET /v1/platform-tenant-self/apps/{slug}/workflows/schedules/{name}/preview`
to preview only their own configurable schedule. Go clients expose
`GetWorkflowSchedulePreview` and `GetPlatformTenantSelfWorkflowSchedulePreview`;
Node and Python SDKs expose the same two operations. No migration is needed.
See [ADR-645](adr/645-workflow-schedule-preview.md).

## Resume after a terminal failure

Find runs for an existing automation without knowing a run ID:

```sh
gregale automations runs --app billing --name approval-flow --status failed
gregale automations runs --app billing --name approval-flow --status awaiting_event \
  --created-after 2026-10-01T00:00:00Z --limit 25 --offset 0
gregale automations diagnose --app billing --name approval-flow \
  --run 00000000-0000-4000-8000-000000000001
```

Run history uses the exact automation name and the app's workflow history.
Supported statuses are `pending`, `running`, `awaiting_event`, `succeeded`,
`failed`, and `dead`. Omit `--status` for all statuses, including cancelled
runs as represented by the existing workflow status model. `--created-after`
and `--created-before` accept inclusive RFC3339 timestamps. Page size is 1..100
(default 50), with a nonnegative offset. The JSON report contains app/name,
total matching runs, limit/offset, and metadata summaries; it omits input,
output, tenant identifiers, and raw error text. Human output shows run IDs,
statuses, pinned deployment IDs, and creation times. Use `--status dead` as well
as `--status failed` when investigating terminal failures.

Diagnosis resolves the app and automation, checks the run's app identity and
workflow name, then fetches its read-only diagnostics. A run from another app
or automation is rejected before requesting diagnostics. The JSON report wraps
the diagnostics snapshot with the selected app and name. Human output shows
queue/wait reasons, lease state, step status, resume eligibility and blockers,
and an eligible resume command. This command does not retry, resume or cancel
the run. Both commands require read access to the app, automation, and workflow
history; normal server permissions apply to every request. The automation must
still exist. Historical runs of a deleted automation remain inspectable through
the existing `workflows` commands with appropriate access.

The human `automations health` report includes a run-history command using its
exact returned creation-time window, with fractional seconds preserved. It also
offers `--status failed` and `--status dead` commands when those failures appear
in the report, plus a scoped diagnosis command for the latest failure when its
run ID is valid. Copy these commands to inspect the runs behind the reliability
metrics. Printing health performs no additional history or diagnosis requests;
the commands remain read-only when you execute them. Healthy windows still
include a general history command. Health JSON retains its existing API format.

Inspect a run and preview its continuation before changing it:

```sh
gregale workflows diagnose 00000000-0000-4000-8000-000000000001
gregale --json workflows diagnose 00000000-0000-4000-8000-000000000001
```

`GET /v1/workflows/runs/{id}/diagnostics` returns the same read-only snapshot.
It shows the durable queue reason, future wake, due age, expired worker lease,
original code pin and step status/kind. Step kinds distinguish timer, event,
callback and condition waits. `ready` means dispatch admission is open; it does
not guarantee an available worker or immediate execution.

The `resume` object reports `eligible`, the observed `expected_resume_count`,
sorted `reopened_steps` and `preserved_steps`, and `blockers` with a stable `code`,
fixed `message` and optional `step_name`. Preserved names include successful
actions and branches whose persisted state remains unchanged. A structurally
valid plan can have temporary admission blockers, such as maintenance or a full
active-run quota; its proposed reopened names remain visible.

When the snapshot is eligible and has no blockers, the human CLI output includes
a copyable `gregale workflows resume RUN_ID --expected-resume-count N` command
using the observed resume count. Review the reopened steps before executing it.
Diagnosis remains read-only and does not run the command. Blocked or ineligible
snapshots do not offer a resume command, and JSON retains the existing snapshot
format. The eventual resume request still rechecks eligibility and generation.

| Code | Meaning |
| --- | --- |
| `run_not_failed` | The run is still active or has succeeded |
| `handler_executed` | A failure/timeout handler or its continuation already executed |
| `unsafe_mutation` | An attempted external mutation lacks declared provider idempotency |
| `failed_control_step` | A failed wait or join cannot be reopened |
| `failure_before_dispatch` | No executor attempt exists for the failure |
| `active_step` / `active_attempt` | A call or wait is still active |
| `tenant_unavailable` | The tenant is inactive or its app link was removed |
| `pinned_deployment_unavailable` | The original handler code cannot be served |
| `integration_unavailable` | A current credential or permitted integration binding is unavailable |
| `active_run_quota` | Other active runs fill the app's admission quota (runs waiting on an event count only beyond the plan's parked allowance) |
| `runtime_disabled` | Execution is disabled on the responding API server |

The planner reports its first deterministic blocker plus independent admission
blockers. The snapshot omits inputs, outputs, error text, credentials and tenant
identity; use authorized run/step inspection for payloads and error details.
Reads do not execute actions or reserve capacity. Resume rechecks the gates and
generation, so an eligible preview can become stale before submission. Read-only
API keys can preview; resume still requires write permission. Tenant-bound read
tokens use `GET /v1/platform-tenant-self/workflows/runs/{id}/diagnostics`, restricted
to their own runs and current app links. Both routes use `Cache-Control: no-store`.
Go `GetWorkflowRunDiagnostics`, Node `WorkflowsService.getWorkflowRunDiagnostics`
and Python `workflows.get_workflow_run_diagnostics` expose the account route,
with corresponding tenant-self methods. No new migration is needed.
See [ADR-644](adr/644-workflow-run-diagnostics-and-resume-preview.md).

After resolving a provider outage or integration configuration issue, inspect
`GET /v1/workflows/runs/{id}` and send its current `resume_count`:

```http
POST /v1/workflows/runs/{id}/resume
Idempotency-Key: invoice-batch-resume-1
Content-Type: application/json

{"expected_resume_count":0}
```

The response is the queued run with `resume_count: 1`. Use the same request
idempotency key to retry this request after a lost response. A new continuation
must send the latest count; concurrent requests with the same count have one
winner. Each run permits up to 16 resumptions and uses the normal active-run
quota. Resume requires a live default deployment, an eligible account/app,
valid integration bindings and the workflow runtime to be enabled.

The CLI exposes the same recovery flow. `workflows status` displays the current
resume count; pass that value to `workflows resume`. Keep the count and key the
same if you repeat a request after a timeout, and use `workflows resumes` to
inspect the continuation audit history:

```sh
gregale workflows status 00000000-0000-4000-8000-000000000001
gregale workflows resume 00000000-0000-4000-8000-000000000001 --expected-resume-count 0 --idempotency-key invoice-batch-resume-1
gregale workflows resumes 00000000-0000-4000-8000-000000000001
```

Successful actions and batch items retain their results. Failed actions receive
a fresh configured retry budget; attempt numbers continue increasing. A failed
batch resumes at its failed item and processes the remaining snapshotted items
in order. The original workflow definition, inputs and guard decisions remain
unchanged. Publishing a new definition does not change this run. App actions
retain the original handler deployment across retries and resumptions. Legacy
unpinned runs retain their existing best-effort routing; diagnostics label them
explicitly. Start a new run to use newly deployed code.

The same logical action Idempotency-Key is reused. App handlers must deduplicate
it across the full retry/resume period. Managed mutations must declare actual
provider idempotency support whose deduplication window covers that period.
GET/HEAD actions may be repeated. Previously attempted mutations without this
support are rejected, even if they returned an HTTP error.

Cancelled runs, active calls or waits, already executed failure/timeout handlers,
failed control steps and failures before dispatch cannot be resumed. This first
version does not override inputs, replace the definition or select an arbitrary
step. Correct a bad definition and start a new run when those changes are needed.

`GET /v1/workflows/runs/{id}/resumes` returns the requesting account, previous
failure, reopened step names, timestamp and resume number. The existing step
attempts endpoint retains all previous attempts. Step inspection exposes
`retry_base`, the attempt number at the last continuation; later attempts consume
the current retry budget. Resume history is retained for as long as the run.

Apply migration `20261003180000001_workflow_resume.sql` and update/drain scheduler
workers before customers use the endpoint. Drain or cancel resumed runs before
downgrading; export resume history before rolling the migration back.

## Admission history and reliability alerts

Inspect the last 30 days of due schedule outcomes with
`gregale workflows schedule-history --app reports --limit 100` or
`GET /v1/apps/{slug}/workflows/schedules/occurrences`. Both started and skipped
minutes are retained, including `skipped_quota` and `skipped_overlap`; each
started occurrence includes its run ID even after the run expires. History
contains no workflow input or output. Use `--platform-tenant-id` to inspect one
customer and `--cursor` with the returned next cursor for another page.
History starts when this version is deployed; earlier outcomes are not backfilled.
An expired history cursor returns an empty page.

Recover a selected skipped occurrence with a preview followed by an explicit
replay:

```sh
gregale workflows schedule-history replay-preview --app reports \
  --occurrence-id 00000000-0000-4000-8000-000000000001
gregale workflows schedule-history replay --app reports \
  --occurrence-id 00000000-0000-4000-8000-000000000001
```

Repeat `--occurrence-id` to select up to 20 occurrences; the service processes
them in scheduled order. Preview reports eligibility against the current live
deployment, definition, tenant settings, overlap state, and quota. It reserves
nothing, so replay rechecks all conditions. A replay uses the original nominal
`scheduled_for` time and current trigger input. It is blocked if the deployment
or definition changed, the schedule is disabled, a tenant link is no longer
active, overlap is active, or the app quota is full. History displays the
resulting replay run ID, and retries of the same occurrence return that ID
without creating another run. Legacy history without a definition fingerprint
cannot be replayed. See [ADR-654](adr/654-controlled-workflow-schedule-replay.md).

Tenant/workflow pairs share the app's active-run quota and are evaluated in
least-recently-admitted order. Admission priority persists through scheduler
restarts, skipped minutes, deployment changes, and run retention. Paused
schedules are excluded. This distributes scarce admissions across customers;
quota skips consume the selected occurrence and are not retried automatically.

### Recovering missed scheduled starts

Schedules skip missed fires by default. For reports or reconciliation jobs that
should recover after downtime, opt into the latest eligible missed fire:

```yaml
trigger:
  type: schedule
  schedule: '0 7 * * *'
  timezone: Europe/Istanbul
  catch_up: latest
  catch_up_window: 2h
```

`catch_up` accepts `skip` (the default) or `latest`. The recovery window defaults
to one hour and accepts duration strings between one minute and 24 hours. A
7:00 job observed again at 7:20 starts once with a nominal fire time of 7:00.
If several fires were missed, only the latest one inside the window is selected;
if the current minute is due, it takes precedence. Older missed fires are
discarded. No historical runs start on the first observation or after re-arming
for a deployment, trigger change, tenant cadence update, or automation resume.

Recovery still observes overlap and the shared run quota. A rejected occurrence
is recorded as skipped and consumed, including the older coalesced interval;
freeing a slot does not replay it. History exposes `scheduled_for` and
`evaluated_at` so the original fire and recovery time remain visible. Coalesced
and expired fires are not backfilled into history.

The policy also applies to gaps caused by maintenance, runtime gates, or an
unavailable tenant link. Tenant schedules inherit the owner's recovery policy
while applying their own saved cadence and timezone. Tenants cannot change the
policy or window. `gregale workflows schedules --app reports` and the schedule
inspection APIs show the effective policy and window.

Update validators and scheduler workers before publishing catch-up options.
Remove the options and drain or cancel affected snapshot runs before downgrading
to a runtime that does not understand them.

Existing alert rules accept five notification-only workflow metrics:

| Metric | Observation |
| --- | --- |
| `workflow_failures` | Failed or dead runs completed in the selected window; excludes cancellation |
| `workflow_schedule_quota_skips` | Due schedule occurrences skipped for app quota in the selected window |
| `workflow_pending_age_seconds` | Age of the oldest eligible pending run since it became eligible; excludes future retries; zero if none |
| `workflow_waiting_age_seconds` | Age of the oldest currently awaiting step, zero if none |
| `workflow_due_age_seconds` | Age since the oldest pending run, elapsed parked wake or expired lease became due; excludes future waits and live running claims; zero if none |

Scope an alert to one app or the authenticated account. Age signals describe
current state rather than a window average. Waiting age includes intentional
timers, callbacks, conditions, and event waits, so choose thresholds suitable
for the workflow. These metrics support webhook notifications only.

### Automatically pause after repeated failures

Failure notifications can be paired with an opt-in admission pause per
published YAML or dashboard automation. Configure a failure threshold, a
minimum completed sample count, and an observation window:

```sh
gregale automations failure-policy --app billing --name paid-invoice
gregale automations failure-policy --app billing --name paid-invoice \
  --enabled --expected-version 0 --failure-threshold 3 \
  --min-completed-runs 5 --window-seconds 300
```

The default policy is disabled. Defaults are three failures, five completed
samples and 300 seconds; thresholds and minimum samples accept 1..10000,
and windows accept 60..86400 seconds. Updates require the current policy
version. Enabling a new policy starts monitoring now; explicit failure resume
starts a new monitoring epoch. Policy edits preserve the existing epoch.
Failures count failed and dead runs completed within the window, while samples
also include successful runs. Cancellations, intermediate retries, and native
Customer Operations do not contribute.

The scheduler evaluates bounded pages of policies before scanning schedules.
A threshold breach latches a durable failure pause on an evaluator tick;
detection can take additional ticks when many policies are configured.
The pause blocks future app and tenant schedule admissions, new event routing,
inbound webhook admission, and admission of event recipients captured before
the pause. Captured recipients remain subject to normal routing retention and
retry budgets. Inbound webhook requests received during the pause have ignored
receipts with reason `automation_failure_paused`. Events published with no
eligible recipient are not replayed on resume. Existing pending, running and
waiting workflows continue; manual starts remain available for diagnosis.

The failure pause is separate from configured enabled intent. Publishing a
fix, ordinary `automations resume`, disabling monitoring, or deleting and
recreating a definition does not clear it. `automations list` and `get` show
`failure_paused` independently of `enabled`. Inspect the failure policy for
its generation, pause reason, observed counts, latest 100 pause/resume
transitions, and an advisory preview of retained work:

```sh
gregale automations failure-policy --app billing --name paid-invoice
gregale automations failure-resume --app billing --name paid-invoice \
  --expected-generation 1
```

`failure-resume` reads the preview before writing and rejects a stale
positive generation. The server records the acting account and starts a fresh
monitoring epoch so the previous failures do not immediately trip again.
Resuming re-arms schedule cursors without catch-up for the paused interval;
it does not change a manual pause. Preview counts can change concurrently.
Existing workflows continue; retained event recipients may create runs once
eligible, subject to quota, routing retry budgets and retention.

To receive a pause notification, configure an app webhook whose event filter
includes `automation.paused` (an empty all-events filter also receives it).
The existing signed webhook transport sends one logical event per pause
transition, even with concurrent evaluators. Delivery retries can repeat the
same event, so receivers should deduplicate by delivery identity. The payload
contains the app ID, automation name, generation, reason `failure_threshold`,
policy version, pause time, and aggregate failure/sample counts. It does not
contain run inputs, outputs, errors, or tenant identities. Resume is recorded
in history without a separate webhook. Failure alert rules remain independent
and can be enabled alongside the pause policy.

### Unified recovery preview

Inspect recovery candidates without clearing a pause, admitting events, moving
schedule cursors or resuming runs:

```sh
gregale automations recovery-preview --app billing --name paid-invoice \
  --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z
gregale --json automations recovery-preview --app billing --name paid-invoice \
  --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z --limit 10
```

The report combines current admission intent and failure-pause generation with
separate failed/dead run pages. Each run includes its server-observed status,
resume eligibility, expected resume count, blocker codes, and steps that would
be reopened or preserved. Run inputs, outputs, errors and tenant identities are
omitted. Failed/dead pages cover retained history regardless of creation time;
`--from` and `--until` apply only to event acceptance and schedule evaluation.

For a published event trigger, the report includes captured retained event
metadata, per-event admission reasons, existing admissions, potential admissions,
retention coverage and an
opaque continuation cursor. Potential admission is advisory: pauses, routing,
quotas, retention and current execution gates still apply. A retained-event
count does not guarantee that every event will produce a run.

For a published schedule trigger, it includes the existing schedule policy
preview from `--from` through the evaluation time `--until`. A failure-pause
resume **re-arms without catch-up**, even if the schedule's ordinary policy is
`latest`; the report explicitly labels this distinction. Disabled schedules
may report `disabled` rather than a count of missed fires. Other trigger types
have no retained trigger-work preview, but still show failed/dead run recovery.

The default limit is 10 and the maximum is 25 per run status and event scan
page. The command performs one page request per failed/dead status, one
metadata-only diagnostic request per returned run, and one applicable trigger
preview. Use `--run-offset` for additional run pages and `--event-after` for an
additional event scan page; preserve the same explicit `--from` and `--until`
when continuing events. `--until` defaults to the current time.

All requests use existing authenticated read APIs. Any required read failure
or scope mismatch fails the command instead of emitting a partial report.
`started_at`, `completed_at` and per-run observation times describe the multiple
reads; this is not an atomic snapshot, a complete archive, or a reservation.
Recovery actions recheck their own eligibility and generation fences.

### Controlled recovery of selected runs

Use the run IDs and `expected_resume_count` values from recovery-preview to
resume a bounded explicit selection:

```sh
gregale automations recover --app billing --name paid-invoice \
  --run aaaaaaaa-0000-4000-8000-000000000001:2 \
  --run bbbbbbbb-0000-4000-8000-000000000001:5 \
  --idempotency-key invoice-outage-batch-1
```

Repeat `--run UUID:COUNT` for at most 25 distinct runs. The count is required
and must be the original count reviewed for that run. The batch key is required;
keep it with the selection if the command may need to be retried. The CLI derives
an independent stable request key from the batch key, app identity, automation,
run ID and original count, so reordering the selection preserves those keys.

Before its first write, the command verifies that every selected run belongs to
the chosen app and automation. It then reads each run and its diagnostics again,
checks the original count and failed/dead state, and submits only eligible runs
to the existing resume API. The server rechecks admission and continuation safety
when committing. Completed steps and captured definitions retain the existing
resume semantics; the CLI does not construct a replacement run or workflow.

Each run commits independently. The command continues after an individual
blocked, changed or failed outcome and emits a human or `--json` report with:

- `resumed`: the API accepted the continuation with the next resume count.
- `blocked`: diagnostics or the resume API rejected safety, quota, plan or deployment gates.
- `changed`: run status or count changed, or the server rejected a stale count.
- `failed`: a required read, scope check, or resume request could not be confirmed.

Exit status is 0 only when every selected run reports `resumed`; mixed outcomes
return 1 after writing the report. A scope/read failure during the initial full
selection check exits before any write. Reports include metadata and blocker
codes, not run inputs, outputs, error details or tenant identities. Status and
resume count are the last observations, rather than a final execution result.

A `resume_unconfirmed` or `resume_response_unconfirmed` result can mean the
server committed but its response was lost or unusable. Retry the **same run
IDs, original counts and batch key**. The CLI never refreshes an expected count
to make an interrupted request pass. If the previous request advanced the run,
the retry reports `changed` and does not issue another resume for it. A changed
run might have been resumed by another operator; inspect `gregale workflows
resumes RUN_ID` for the recorded actor and continuation history. To request a
later intentional continuation, inspect a fresh preview and select its new
count with a new batch key.

This action does not clear manual/failure pauses, replay events, change schedule
cursors or replay missed schedule fires. Existing run continuations use their
own diagnostics and server admission checks independently of automatic starts.

The API provides `GET` and `PUT /v1/apps/{slug}/automations/{name}/failure-policy`
and `POST /v1/apps/{slug}/automations/{name}/failure-policy/resume`. Reads require
app read scope; configuration and resume require deploy write scope. Apply
migration `20261010071515271_automation_failure_pauses.sql` and update all
scheduler, admission and API replicas before enabling policies. Rollback
requires explicitly resuming every failure pause and removes policies, guard
state and guard history. Committed webhook events remain deliverable.
See [ADR-905](adr/905-automation-failure-admission-pauses.md).

For terminal failures, enable the opt-in `automation_failures` preset for an
app on Hobby or higher. It sends a signed webhook when at least one run has
failed or died in the last five minutes, with a 30-minute default cooldown:

```sh
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable automation_failures \
  --app billing --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

This policy covers all workflows in the app. Cancellations, successful runs,
and intermediate retry attempts do not count. Evaluation occurs on the next
alert evaluator tick; repeated ticks during cooldown do not create duplicate
deliveries. The same failure leaves the observation window after five minutes.
A later failure can notify again once cooldown has elapsed. Clearing a breach
returns the rule to `ok` without a separate recovery webhook. Customize the
threshold, observation window and cooldown using the existing alert rule API or
`gregale alerts update`. This is an aggregate notification policy rather than
one notification for every failed run.

App-scoped workflow notifications include `app_id`, `workflow_runs_path`, and
`automations_path`. Failure notifications also include
`workflow_dead_runs_path`; the two run links separately select `failed` and
`dead` runs. These are authenticated API paths relative to your Gregale endpoint,
not public links. They show current run lists, not a snapshot restricted to the
alert window: a long-running workflow can finish inside the window despite
being created earlier. Retrieve a run's diagnostics using
`GET /v1/workflows/runs/{id}/diagnostics`. Notifications do not copy run inputs,
outputs, error messages, or tenant identities. Account-wide rules omit app links;
app lookup failures omit links without suppressing the notification.

Apply migration `20261010071621984_automation_failure_alert.sql` to expose the
failure preset. Updating the alert evaluator adds the investigation paths to
all five workflow metrics. Existing rules and notification delivery history
remain intact; no new alert is enabled automatically. Disabling the rule stops
future evaluation. Rolling back removes the preset from the catalog while rules
already created from it continue to use the existing `workflow_failures` signal.

For sustained backlog, enable the opt-in `automation_backlog` preset for an
app on Hobby or higher. It sends a signed webhook when an automation has been
due for at least five minutes, with a 30-minute default cooldown:

```sh
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable automation_backlog \
  --app billing --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

The preset covers all automations in the selected app. Its age signal matches
queue health, including work blocked by concurrency limits, overdue timers and
event/callback timeouts, and expired worker leases. Intentional future waits and
retry backoff do not count. `window_spec` does not average or restrict this
current-state age. The next evaluator tick observes a threshold breach; a
sustained breach can notify again after cooldown. A cleared backlog returns
the rule to `ok` without a separate recovery webhook.

To choose a different threshold, create a custom alert using
`workflow_due_age_seconds`, comparison `gte`, and a threshold in seconds.
After a notification, inspect `gregale automations health --app billing --name
AUTOMATION` for waiting reasons and `gregale workflows list --app billing` for
the runs. Notifications contain aggregate values rather than run payloads or
tenant identities. Apply the backlog-alert migration and update all evaluators
before enabling this preset. Remove its rules before downgrading.

App-handler steps retry HTTP 408, 425, and 429 using the configured attempt
budget. A downstream `Retry-After` (seconds or HTTP date) extends ordinary
backoff, capped at one hour, and survives scheduler restarts. Ordinary 4xx
validation/authorization errors remain terminal. All attempts retain the
same step idempotency key; app handlers must deduplicate their side effects.
Unsafe outbound actions keep their existing no-retry policy.

Workflow dispatch uses up to 32 execution slots per scheduler. Every tick
offers four, and one more while every slot is busy, so capacity grows with
load; each slot drains at most eight runs. A slot waiting on a handler does not
hold up other slots' bookkeeping. Eligible applications
and tenant scopes within an application take turns according to their persisted
last claim; older due work wins within a scope. A large backlog cannot keep an
eligible scope behind all its older runs.

At most two unexpired running automation claims belong to one app, and at most
one belongs to one tenant within that app, across upgraded scheduler workers.
An app may queue runs while dispatch slots are idle, because these caps reserve
capacity for other apps.
Timers, callbacks, condition waits and pending retries release dispatch capacity
while parked. These dispatch caps are separate from the plan's active-run quota,
`max_concurrent_runs`, handler invocation limits and foreach parallelism. A wait
may still consume the workflow definition's concurrency budget.

Fair selection survives worker restarts and history pruning. Executing handlers
finish or time out before their slots become available; dispatch does not
preempt them or guarantee latency when all slots are busy. Apply the database
migration before upgrading workers; all workers must be upgraded for the new
dispatch caps and fairness to apply consistently.

### Review draft changes before publishing

```sh
gregale automations diff --app billing --name approval-flow
```

This read-only command compares the saved draft with the published definition from
one automation snapshot. It reports added, removed and modified fields, including
triggers and schedules, step dependencies and guards, input mappings, outbound
integration targets, retry policies, waits and failure routes. Steps are matched
by name; `/step_order` records changes to their stored order. Field paths use JSON
Pointer escaping (`~0` for `~`, `~1` for `/`), with step names under `/steps`.
Arrays are compared in order. Missing fields and explicit `null` remain distinct.

The report identifies both versions and the current live automatic-starts state.
That live state is metadata, not a prediction of whether publishing will enable
starts: use `automations pause` or `resume` to change the live state. An automation
with no published definition is labeled as a first publication. An unchanged
definition reports no changes and exits successfully.

Values are omitted from human and JSON reports so input payloads and integration
values are not echoed. To inspect actual values, explicitly export the draft and
published definitions with `automations get --definition-out` and `--published`.

Publishing with `--scenarios` shows this same summary before scenario simulation
and the publication request. JSON output includes it under `diff` in the final
combined publication report. Scenario checks and the version checks still block
publication on failure or a changed draft; the diff itself does not require
confirmation and does not block a publication with no definition changes.

### Scenario coverage hints

`automations check` reports advisory coverage hints after the scenario results.
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

Use `--mock-item-attempts-file item-attempts.json` with `automations simulate`, or
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
gregale automations check --app billing --scenarios scenarios.yaml --suggest
# Optional new destination:
gregale automations check --app billing --scenarios scenarios.yaml \
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
gregale automations check --app billing --scenarios scenarios.yaml --require-coverage
gregale automations publish --app billing --name approval-flow \
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
immutable published revision. Use `gregale automations revisions show` to inspect
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

Apps can enforce checks for dashboard publication. The default `optional` policy keeps plain publishing available. `scenarios` requires a server-issued receipt for complete, valid scenarios with passing assertions. `coverage` also requires all configured coverage paths to be observed or explicitly excluded with a reason. This policy covers dashboard automations; YAML deployment remains a separate release surface.

Inspect and change the policy with:

```sh
gregale automations publish-policy --app billing
gregale automations publish-policy --app billing --mode coverage --expected-version 0
gregale automations publish --app billing --name paid-invoice --expected-version 7 --scenarios scenarios.yaml
```

Changing the policy requires an admin credential and its current policy version. Publishing credentials with `deploy:write` can request checks and publish, but cannot weaken the policy. Checked CLI publishing also needs `apps:read` to read the draft and simulations. Changing a policy invalidates existing receipts, including when switching back to the previous mode.

Checked publishing reads and displays the app policy, enforcing mandatory coverage locally even without `--require-coverage`. It reports local assertion failures, then sends the preloaded suite to `POST /v1/apps/{slug}/automations/{name}/publish-check`. The server independently simulates the saved draft, evaluates root and per-item assertions (including exact JSON values and ordered attempts), and evaluates coverage. Every submitted scenario must contain at least one assertion. The policy overrides a client's request to skip required coverage. Invalid exclusions, incomplete traces, failed assertions and mismatched definitions produce no receipt.

Receipts expire after 30 minutes and are bound to the app, automation name, draft hash and version, account/API-key identity and policy version. The server validates these bindings atomically with publication. Draft changes, publication, restore or delete/recreate make an earlier receipt stale. Each successful check replaces the previous receipt for that automation and identity. HTTP publication retries continue to use the existing idempotency mechanism; a new publication cannot reuse a receipt against a later version.

Only the receipt hash and safe evidence metadata are saved with the receipt. Sample inputs, mocks, expected values and trace payloads are processed transiently and are not saved there. Published revision evidence uses `server_verified: true` when it came from a stored receipt; clients cannot set that flag on plain publication. Revision inspection labels trusted evidence as **Server-verified** and older evidence as **Client-reported**. Server verification establishes what the supplied simulations demonstrate; it does not execute or verify external service behavior. Keep scenario names and exclusion reasons free of secrets.

The check endpoint accepts at most 32 scenarios, 24 MiB total and 3 MiB per simulation, with the existing simulation/value/loop limits. A request cannot supply a definition different from the saved draft or override stored server evidence during publication.

### Saved run recovery plans

Save an explicit reviewed selection without resuming any runs:

```sh
gregale automations recover --app billing --name paid-invoice \
  --run RUN_UUID:2 --idempotency-key outage-batch-1 --plan-out recovery.json
gregale automations recovery-plan --plan recovery.json --journal attempt-1.jsonl
```

The versioned JSON plan pins the app identity, automation, original resume counts,
run IDs and stable batch key. Creation checks current scope and run state. Review
this file before execution; it contains no run inputs, outputs or error details.
Execution checks every run's scope before any resume, then rechecks eligibility
and counts immediately before each independently committed request.

Plan and journal paths must be new files; both use owner-only permissions. The
journal syncs a start record, an attempted record before each run, its result,
and a completion record. Failure to persist stops further attempts. An attempt
without a result may have committed; a completion record means all selections
were processed, not that all resumed or that their workflows completed.

Retry an interrupted batch with the same plan and a **new** journal path. Original
counts and keys remain fixed, including after a lost response. Advanced runs are
reported as changed and never automatically resumed at a newer count. A changed
result does not establish who resumed the run; inspect its resume history.
Plans are limited to 25 runs and 64 KiB. Each execution has a two-minute timeout.
The journal is local recovery evidence, not a server-side transaction or audit log.

### Reviewed recovery of automation event routing

Save selected retained events whose captured automation recipient currently has
one supported routing replay action:

```sh
gregale automations event-recovery-plan --app billing --name paid-invoice \
  --source billing --event invoice-42 --event invoice-43 \
  --idempotency-key incident-1 --out event-recovery.json
gregale automations event-recover --plan event-recovery.json --journal events-attempt-1.jsonl
```

Export is read-only. The plan pins the app identity, published automation version,
source/event IDs, captured subscription, routing generation/replay count, exact
routing replay action and stable batch key. It excludes event payloads and error
text. Creation fails for ambiguous recipients or unavailable actions. Execution
rechecks each event immediately before requesting replay and journals attempted
and outcome records with disk sync before continuing. Files must be new and use
owner-only permissions. Both commands have a two-minute timeout; selections are
limited to 25 events, plans to 64 KiB, and each receipt scan to 25 pages.

Outcomes distinguish queued, changed, blocked and failed. Expired retained events
are blocked without an age override. Missing retained evidence is unavailable;
it cannot establish whether an event expired or never existed. Changed revisions,
routing generations or actions require a new review. Already admitted workflows
use run recovery rather than routing replay. Admission pauses and other server
rules remain authoritative when the routing queue processes the replay.

Retry with the **original plan** and a new journal path. Per-event idempotency keys
remain stable and advanced routing generations are never adopted. A lost reply
is unconfirmed; consult the journal and receipt. The journal completion record
means all selected events were processed, not that all were queued or completed.
Saved plans send `recovery_expectation` with the published automation version,
routing replay count and generation (when present). The server compares these
values while holding the publication lock and routing receipt locks, then queues
the replay in the same transaction. Changes return HTTP 409
`event_recovery_changed` without mutating the receipt. Existing replay requests
that omit expectations retain their behavior. Guarded replay supports acceptance
recipients; historical backfill recovery uses its separate job controls.
Admission rules still apply when the queued recipient is processed.

### Durable recovery of reviewed automation events

Submit a saved event plan as a server-managed job:

```sh
gregale automations event-recovery-start --plan event-recovery.json --rate 5 --reason "invoice routing incident"
gregale events recovery-status JOB_ID
gregale events recovery-items JOB_ID
gregale events recovery-pause JOB_ID --yes
gregale events recovery-resume JOB_ID --yes
gregale events recovery-rate JOB_ID --rate 2 --yes
gregale events recovery-cancel JOB_ID --yes
```

The job continues after the CLI disconnects. It protects pending retained receipts
within the existing job lifetime and shares existing account job quotas and rate
limits. Pause and cancel stop future routing admissions; already queued workflows
continue. Each item uses the saved published version, replay count and routing
generation, checked atomically before admission. Changed items are skipped rather
than following newer failures. Delivery deadlines still apply; historical
backfills use their separate job controls.

Retry an uncertain submission with the original plan and rate. Its stable plan
key identifies the retained job, including after completion; changing the plan or
initial rate with the same key conflicts. Use the rate control to adjust an
existing job. Idempotency lasts for job retention. Preview only confirms retained
selection and preflight reports workflow fence eligibility as unknown; the worker
performs the authoritative checks. `queued` records routing admission, not
workflow success. Saved **run** recovery plans can also be submitted with `recovery-start`.

### Server-managed run recovery

Submit a reviewed saved run plan to continue recovery after the CLI disconnects:

```sh
gregale automations recovery-start --plan recovery.json --rate 2 --reason "incident recovery"
gregale events recovery-status JOB_ID
gregale events recovery-items JOB_ID
gregale events recovery-pause JOB_ID --yes
gregale events recovery-resume JOB_ID --yes
gregale events recovery-rate JOB_ID --rate 1 --yes
gregale events recovery-cancel JOB_ID --yes
```

The plan's original run IDs, workflow name and resume counts are frozen. The server checks each count and safe continuation atomically with resume admission. Changed or unsafe runs are skipped; concurrency pressure leaves the item pending for a bounded retry. Cancelling stops pending admissions and preserves admitted resumes. Items expose `workflow_run_id` and `expected_resume_count`; `queued` means the resume was admitted, so inspect the workflow run for its execution outcome.

Retry submission with the original plan and original rate after a lost reply. The retained job is returned even after completion; changing the selection or initial rate with the same plan key conflicts. Change the running job's rate through `recovery-rate`. The shared recovery quota applies across modes. Jobs expire after 24 hours and remain retained for 30 days; pausing does not extend expiry. Preflight is advisory and reports native resume eligibility as unknown. The local `recovery-plan --journal` command remains available.

### Observe resumed workflow execution

`events recovery-status JOB_ID` includes an execution summary for native run recovery jobs. `events recovery-items JOB_ID` includes each admitted run's execution observation, with `source: workflow_run`. The job and item admission counters keep their existing meaning: completing admission does not mean resumed execution succeeded.

Observations track the original reviewed resume count plus one, supported by the retained resume record. Pending runs report queued, running or awaiting-event runs report running, succeeded runs report succeeded, and failed/dead runs report failed. Operator-cancelled terminal runs report cancelled. Without an earlier saved terminal result, a later resume reports superseded and never contributes its completion time or success to this recovery job. Missing or pruned run/resume history reports unknown.

Native observations are read-only. The first confirmed terminal outcome is saved with its recovery item and survives workflow-history pruning for the recovery job retention period. Saved observations use `source: recovery_result`, `evidence_source: workflow_run`, and a `recorded_at` timestamp. `saved_results` counts these outcomes; invocation dispatch `attempts` stays zero. Once saved, an earlier failure remains authoritative if the run is resumed again. A later resume is superseded only when no earlier terminal outcome was saved. Use job status for execution summaries; recovery discovery lists remain admission metadata only.

### Native recovery execution-finished notifications

Subscribe an application webhook to `event_recovery.execution_finished`. After admission completes, is cancelled, or expires, the scheduler captures one notification when all admitted resumes have saved terminal results. `all_succeeded` means every admitted resume succeeded; otherwise the outcome is `finished_with_non_success`. Admission counters remain separate. Unknown or active outcomes block capture; jobs admitting no resumes produce no execution notification.

The payload includes outcome counts, `execution_finished_at`, and `items_url`, an authenticated relative API link to paginated recovery items. Items retain run IDs even when workflow history is gone; retained runs can be inspected through `/v1/workflows/runs/{id}`. Inspect `events recovery-notifications JOB_ID` for recipient and delivery evidence. Existing outbox retries apply; receivers should deduplicate by `event_id`.

### Retry saved native workflow recovery failures

Preview a terminal native recovery job's saved outcomes and current resume eligibility:

```sh
gregale automations recovery-retry-plan --app worker --job JOB_ID
```

Review the displayed positions, then save only the eligible failures you intend to retry:

```sh
gregale automations recovery-retry-plan --app worker --job JOB_ID \
  --position 2 --position 4 --key incident-retry-1 --plan-out retry.json
gregale automations recovery-start --plan retry.json --rate 2
```

Preview and plan creation use read-only calls. Saving requires explicit positions, a key, and a new file. A target retains the resume count of the saved failed outcome; it never follows a newer resume. Succeeded, cancelled, unsaved, pruned, changed, or unsafe runs cannot be selected. The server verifies the parent result when creating the child and rechecks count and safety at admission. Changed or unsafe targets are skipped. Use a new key for a different reviewed selection; repeating the same retained request returns the existing job.

Retry plans require durable `recovery-start` submission; `recovery-plan` cannot execute them locally. An admitted child retains parent job/item lineage and can continue after its parent is pruned. Creating a new child requires retained parent evidence. Retries remain explicit operator actions.

The retry preview now uses server-side preflight. It reports `eligible`, temporary `waiting` capacity pressure, and permanent blockers separately. A capacity-only wait remains selectable: the durable worker will wait for admission capacity. Safety blockers, missing saved outcomes, changed generations, missing runs, and exhausted resume limits cannot be selected. Permanent blockers take priority when capacity is also full.

API clients can use `GET /v1/event-recoveries/JOB_ID/preflight?retry=true` to review a terminal native parent's saved outcomes in one consistent read-only snapshot. `reviewed_count` counts parent items and `sample` contains all of them (at most 25); `pending_count` is zero in this view. Timing fields describe the parent job. Ordinary preflight also reports pending native workflow eligibility. Neither view reserves capacity or replaces the checks at durable creation and admission.

Recovery execution deadlines and stalls are advisory. When submitting a reviewed native workflow recovery plan, use `--execution-deadline 1h --execution-stall 15m` on `gregale automations recovery-start` to select thresholds. The deadline includes scheduled and capacity waits; the stall threshold excludes those waits. Without flags, deadlines are disabled and stalls use 15 minutes. Health observes only the admitted generation, and `event_recovery.execution_attention` captures the first attention signal per job through the normal durable notification pipeline. Inspect the linked run diagnostics before explicitly cancelling or preparing another retry.

To resolve an execution attention finding, inspect `gregale automations recovery-health --app billing`. Its run and diagnostics links identify the admitted generation. Preview one cancellation with `gregale automations recovery-cancel-preview --app billing --job JOB --position 1 --resume-count 1 --reason 'provider incident'`. To apply that reviewed action, use `recovery-cancel` with the same arguments and `--yes`. The server rechecks the frozen generation at commit time; a newer resume is refused. Cancellation is cooperative and does not undo external effects. Successful actions appear in `gregale events recovery-history JOB` with the actor, reason and target generation. Use `recovery-retry-plan --app billing --job JOB` to review confirmed failed results after admission ends.

### Saved recovery response plans

Save a reviewed mixture of cancellations and retries from one native recovery job:

```sh
gregale automations recovery-response-plan --app consumer --job JOB_UUID \
  --key incident-20261011 --reason 'provider incident' \
  --cancel-position 1 --retry-position 2 --plan-out response.json
gregale automations recovery-response-preview --plan response.json
gregale automations recovery-response-apply --plan response.json --yes
gregale automations recovery-response-status --plan response.json
```

Positions are explicit and repeatable; a target can appear only once. Cancellation requires an admitted active generation. Retry requires a retained failed execution and unchanged eligible generation. Plan creation and preview do not apply actions. The saved file pins the app, parent job, run generations, reasons and action IDs; protect and review it before applying.

Reapply the same saved plan after interruption. Cancellation receipts retain the original decision, including refusals, even after the run advances. Retry actions reuse the original child job. Status only reads retained receipts and outcomes. Retry admission does not imply completed execution. Unresolved, refused, pending and unknown outcomes return exit code 5; terminal retry execution outcomes are explicitly reported, including failures. Missing or expired evidence remains unresolved. A newly reviewed action needs a new plan key; editing an existing action's payload under its identifier conflicts with its retained request. Actions commit individually.

### Preview recovery policies

An app can opt into scheduled preparation of response plans from retained native recovery jobs. Policies match explicit workflow names and saved failures, stalls or deadline overruns. The initial mode is `preview`; operators still preview and apply exported plans explicitly.

Inspect the current version with `gregale automations recovery-policy --app consumer`. Save this configuration as `policy.json`, using the version you observed (`0` for a new policy):

```json
{
  "expected_version": 0,
  "policy": {
    "enabled": true,
    "mode": "preview",
    "workflow_names": ["invoice-notification"],
    "triggers": ["saved_failure", "deadline_exceeded"],
    "failure_statuses": ["failed", "dead"],
    "max_resume_count": 3,
    "cooldown_seconds": 300,
    "hourly_action_budget": 10,
    "rate_per_second": 1,
    "reason": "Review invoice delivery recovery"
  }
}
```

```sh
gregale automations recovery-policy --app consumer --file policy.json
gregale automations recovery-policy-reviews --app consumer
gregale automations recovery-policy-reviews --app consumer \
  --review REVIEW_UUID --plan-out prepared.json
gregale automations recovery-response-preview --plan prepared.json
gregale automations recovery-response-apply --plan prepared.json --yes
gregale automations recovery-response-status --plan prepared.json
```

Policies are disabled by default and only `preview` mode is supported. A stale `expected_version` conflicts; read the policy before changing it. Set `enabled` to false to stop future preparation. Existing plans remain available for explicit application.

`max_resume_count` bounds total resumes of a run, including manual resumes. The hourly budget counts prepared actions, and the cooldown applies across the app's selected workflows. Preparing a plan consumes budget even if it is never applied. These policy controls do not limit separate manually submitted recovery work. Repeated matches for the same run, generation and action reuse preparation evidence across policy versions. Matching failures use retained outcomes and existing retry eligibility; attention cancellation uses the original admitted generation.

Reviews freeze settings and explain each matched target's prepared or suppressed outcome. Evidence follows recovery-job retention. At most 1000 reviews are retained per app; `review_limit` stops new preparation until retention frees capacity. Evaluation scans five jobs per policy per minute in a cycling identifier order, so large backlogs can delay review. Unknown or changed generations are never substituted with newer targets. Native recovery monitoring is the source of attention thresholds; a policy does not introduce its own deadline or stall timer.

### Review policy-generated recovery plans

Record an operator decision on a retained prepared plan:

```sh
gregale automations recovery-policy-reviews --app consumer --review REVIEW_ID --decision approved --reason "Provider recovered; retry reviewed"
gregale automations recovery-policy-reviews --app consumer --review REVIEW_ID --decision dismissed --reason "Incident resolved externally"
gregale automations recovery-policy-reviews --app consumer --review REVIEW_ID --status
```

Use one operation per command. Decisions require a prepared plan and a reason.
The first decision is immutable; repeating the same decision and reason returns
its original actor and timestamp. A different decision or reason conflicts.
Approval records review only. Preview and explicitly apply the exported plan
using the existing recovery-response commands. Dismissal retains the original
plan, target deduplication and preparation budget evidence; manual apply remains
possible. Decisions share the parent recovery job's retention.

`--status` checks live generations and terminal targets without changing the plan.
It reports ready, stale, unknown, partially_applied, applied or no_plan, with
per-action reasons. Durable action receipts take precedence over live changes.
Applied means cancellation succeeded or a retry child was created; inspect retry
execution with `recovery-response-status`. Capacity waits remain eligible retries.
Review disposition is separate from freshness: approved plans can become stale.

### Recovery review-ready notifications

Subscribe an enabled app webhook to `automation.recovery_review_ready` (an empty
filter also receives it). Schedd captures one logical event per prepared plan,
including existing retained plans that still need review. The payload includes the
app, recovery job, frozen run IDs and generations, trigger, action, review/status
links, and CLI inspection/export commands. Review inspection shows capture status,
reason, receiver count and event ID; use webhook deliveries for HTTP outcomes.

Capture freezes the recipient list. No matching receivers produces `no_receivers`;
adding a webhook later does not replay old reviews. Capture is durable across
restarts, and failed HTTP deliveries reuse the existing retry/backoff machinery.
Receivers should deduplicate by payload `event_id`, since a lost acknowledgement
can result in another HTTP request for the same event.

Approval or dismissal, application, stale targets, and pruned reviews suppress
pending transmissions. Unknown freshness or temporary check errors retain work for
retry. Suppressed deliveries appear as dead with a suppression reason; retrying
manually still checks the review. An already started request cannot be recalled.
Approval and applying actions remain separate explicit operations.


Recovery review status also reports `outcome` and `complete` separately from
application `status`. An applied retry can still be pending or running. Each
action includes its workflow run, exact execution generation, retry child and
items link, and proof source and timestamp when final. Saved terminal retry
results prove success, failure or cancellation. Terminal skipped admission is a
failed action; cancelled admission is cancelled. Missing or superseded execution
evidence stays unknown. All actions must be final before the plan is complete;
mixed success and cancellation reports `completed`, and any failure reports
`failed`.

Subscribe an enabled app webhook to `automation.recovery_review_finished` to
receive one durable completion event per prepared plan. It carries the frozen
final status, proof for every action, app and parent job identifiers, review and
status links, and a CLI status command. Inspect `completion_notification` on the
review for the captured result, event ID and receiver list. A capture without
receivers is retained and does not replay to later subscriptions. Later review
decisions or workflow retries cannot change the captured result. Retention follows
the parent recovery job. Check the webhook delivery ledger for HTTP attempts and
acknowledgement; deduplicate deliveries by `event_id`.


### Bounded automatic recovery retries

Opt in with `mode: "automatic_retry"` using the existing versioned recovery-policy
configuration command. This mode requires `saved_failure` as its only trigger,
explicit workflow names and failure statuses, `hourly_execution_budget` (1..100),
and `pause_after_failures` (1..10). Preview remains the default and cancellation
remains manual.

```json
{
  "expected_version": 0,
  "policy": {
    "enabled": true,
    "mode": "automatic_retry",
    "workflow_names": ["paid"],
    "triggers": ["saved_failure"],
    "failure_statuses": ["failed", "dead"],
    "max_resume_count": 3,
    "cooldown_seconds": 300,
    "hourly_action_budget": 10,
    "hourly_execution_budget": 3,
    "pause_after_failures": 2,
    "rate_per_second": 1,
    "reason": "bounded incident recovery"
  }
}
```

```bash
gregale automations recovery-policy --app consumer --file automatic-policy.json
gregale automations recovery-policy --app consumer
gregale automations recovery-policy-reviews --app consumer
```

Automatic retries use the same exact-target, keyed child recovery path as manual
application. One automatic retry per app must reach a proven final result before
another starts. Unknown evidence defers progress. The hourly execution budget
counts child creation separately from the preparation budget, and spent execution
budget and cooldown survive policy updates. Inspect `automatic_execution` on each
review for its pending, applied or suppressed actions, child IDs and saved results.
Manual application is recognized and does not consume the automatic budget.

After the configured consecutive proven failure threshold, the policy reports
`status: "paused"` and `automatic_execution` contains the failure count, pause
time and reason. Both preparation and automatic execution stop. Subscribe an app
webhook to `automation.recovery_policy_paused` for a durable notification carrying
the policy version and inspection link. Receivers deduplicate by `event_id`; the
webhook ledger records delivery attempts. Existing `automation.recovery_review_finished`
notifications report completed plan results. Automatic plans do not send a
review-ready notification.

To resume while retaining queued retries, use `recovery-policy --control resume`
with an operator reason, as described below. A full policy replacement with the
observed `expected_version` also clears the pause and failure streak, but
suppresses older pending automatic intents.
Disabling or replacing the policy suppresses older pending automatic intents;
already created child jobs use the existing recovery controls. Only newly prepared
plans under the current enabled version execute automatically. Older preview or
superseded plans remain available for manual review and application. Cancellation
is never automatic.

### Inspecting and controlling the automatic retry queue

```sh
gregale automations recovery-policy --app consumer --queue
gregale automations recovery-policy --app consumer --control pause --reason "Investigating provider errors"
gregale automations recovery-policy --app consumer --control resume --reason "Provider recovered"
```

The queue shows total pending retries, unresolved applied children, remaining hourly execution budget, cooldown expiry, and each returned action's last waiting reason. `outstanding` means final proof has not been recorded; it does not assert the child is running. Use `recovery-policy-reviews --app consumer --review REVIEW_ID --status` for live execution evidence. JSON output includes the observed policy, review IDs and policy versions. The first 100 entries are returned in review order; `truncated` indicates more retained entries. Charges survive policy changes and review pruning.

Pause stops preparation and automatic application while retaining the current policy version and queued retries. Already committed children continue. Resume clears the pause and consecutive failure streak, then rechecks each frozen target; spent budgets and cooldowns remain in effect. Controls require an enabled `automatic_retry` policy, a reason, deploy write access and MFA. The CLI reads the current policy and sends the observed version, update timestamp and pause timestamp to `POST /v1/apps/{slug}/recovery-policy/control`. Concurrent changes produce a conflict. Each accepted operation retains its trusted actor and reason in durable control audit storage.

`GET /v1/apps/{slug}/recovery-policy/queue` requires read access and MFA. Queue reasons are observations from the last worker evaluation and do not authorize a retry. Operator pauses do not emit the automatic repeated-failure webhook; that event remains a historical failure fact once per configuration version.

### Suppressing a queued retry and receiving blocked-queue notifications

Inspect `gregale automations recovery-policy --app consumer --queue`, then suppress one pending intent using the returned review, action and current policy version:

```sh
gregale automations recovery-suppress --app consumer --review REVIEW_ID --action ACTION_ID --policy-version 4 --reason "Recovery handled externally"
```

Suppression requires deploy write and MFA. It records the operator reason, trusted identity and timestamp on the retained action, and removes that intent from automatic execution. Repeating the same reason returns the original receipt while the policy version remains current. Changed reasons, stale versions and applied intents conflict. Manual recovery remains available. No child is cancelled, no execution slot is released for an unresolved applied child, and no spent budget is refunded. Inspect the retained review to read the suppression receipt; suppressed intents no longer appear in the pending queue.

Subscribe an app webhook to `automation.recovery_queue_blocked` to receive a historical notification when fresh inspection finds missing or superseded evidence blocking a pending or applied automatic retry. Capture occurs once per action, with a stable `event_id`, frozen recipients and queue/review status links. Normal admission waits, running retries, budget or quota waits, inspection errors, dismissed pending reviews, and paused or disabled policies stay quiet. If no receiver exists at capture, `blocked_notification.status` is `no_receivers`, with no replay to later subscribers. `queued` means admitted to the webhook ledger; use the ledger for delivery results. The event may arrive after the block resolves or the intent is suppressed. Its receipt remains visible on the retained review and, for still-pending or unresolved actions, the queue.

### Queue age and last worker evaluation

Use `gregale automations recovery-policy --app consumer --queue` to see when each retained retry was prepared, its age, the last execution-worker check, and how long the current observed wait has persisted. JSON queue entries expose `queued_at`, `queued_age_seconds`, optional `waiting_age_seconds`, and `action.last_evaluation` (`evaluated_at`, `reason`, optional `waiting_since`). Ages are whole nonnegative seconds relative to the response's `observed_at`; future timestamps from clock skew produce zero age.

Queue age starts at frozen review preparation and includes time after child creation. Waiting age starts with the worker's first observation of the current consecutive reason, survives repeated checks and restarts, and resets when the reason changes. Reasons distinguish `hourly_budget`, `cooldown`, `execution_quota`, `evidence_unknown`, `outstanding_retry`, `admission_wait`, `execution_wait`, `retry_running` and `inspection_error`. Child creation, saved final proof and selected-intent worker suppression record `child_created`, `result_recorded` and `suppressed` without a waiting start. `retry_running` describes the last observed execution outcome; its wait measures time awaiting final proof.

These are historical observations. Reads, blocked-notification capture, policy replacement and operational pause/resume do not refresh them. A paused or disabled policy can retain an old reason even when circumstances have changed; compare its evaluation time and current policy state. Legacy intents and pending retries not yet selected have no evaluation or waiting age, displayed as “not recorded.” The worker evaluates the next pending intent when a committed child holds the slot; later pending entries may remain uninspected. Missing evidence never completes a child, releases its slot or refunds spent budget.

### Previewing a policy replacement

Save the proposed policy and the currently observed `expected_version` in the existing policy update JSON format, then inspect its impact:

```sh
gregale automations recovery-policy --app consumer --file policy-update.json --preview
```

The CLI sends only `POST /v1/apps/{slug}/recovery-policy/change-preview`. This endpoint requires read access and MFA, and returns conflict if the configuration version changed. Version zero previews initial configuration without creating a policy. Preview never rewrites your file or advances the policy version, worker evaluations, scheduling cursors, budgets, retries or notifications. Apply the reviewed file separately with the same command without `--preview`; deploy write access, MFA and the existing version check still apply.

Every full replacement suppresses old pending automatic retries, even with unchanged filters. The preview identifies them as `would_suppress`. Unresolved applied retries from any version remain `retain_slot`; changing filters, disabling automation or lowering budgets neither cancels nor completes them. Spent execution charges remain counted against the proposed hourly execution budget and cooldown. Retained preparation targets and preparation history also remain in effect: suppressed generations can still be `already_prepared`, so replacement does not necessarily create another plan. Existing preview-mode plans are not converted to automatic retries. Replacement clears an automatic pause and failure streak; inspect `current.automatic_execution` before applying.

The snapshot reports current/proposed settings, pending/outstanding counts, up to 100 queue effects, proposed preparation/execution headroom and cooldowns. It inspects up to five newest retained recovery jobs matching proposed workflow names, and returns up to 25 matching candidate decisions. `entries_truncated`, `jobs_truncated` and `candidates_truncated` explicitly indicate omitted results. `would_prepare` simulates preparation; it does not create an executable plan or guarantee quota, admission or immediate execution. Eligible disabled-policy targets report `policy_disabled`.

This sample describes retained recent evidence rather than all historical failures or the scheduler's next UUID-ordered poll. Queue progress, operational controls and failure pauses can change while the configuration version stays the same. Use the observation timestamp, review current queue state before applying, and rely on execution-time evidence and quota checks. Preview grants no additional write or retry authority.

### App recovery automation health

Use `gregale automations health --app APP` to inspect recovery policy health, or
add `--name NAME` for the existing named automation run statistics. App-wide
health accepts `--stall-after-seconds SECONDS` (default 300; range 1–86400) and
`--json`. This request-local threshold is not a saved notification setting.

The read-only `/v1/apps/{slug}/recovery-policy/health` endpoint requires app read
access and MFA. It returns `healthy`, `inactive`, `paused`, `blocked`, `stalled`
or `unknown`, a reason, and the consistent bounded recovery queue snapshot.
Paused takes precedence. Stalled means a child evaluation is overdue, or
pending work lacks recent worker evaluation while execution is permitted. A
fresh pending evaluation demonstrates activity across the pending queue; an
untouched backlog entry alone does not imply a stall. Pending work behind an
unresolved child, active cooldown and exhausted execution budget are expected
blocks. Fresh evidence
that a child is running does not imply a stall based on its execution age.
Disabled policies still expose unresolved applied children.

Queue inspection is bounded. An incomplete sample without a known blocker
reports unknown. `sample_last_progress_at` describes progress in retained queue
entries only; it can disappear after completion and is not an app-wide last
success. Healthy means no blocker observed, not proof of scheduler liveness.
Use `gregale automations recovery-policy --app APP --queue` for individual
worker observations. No health read sends alerts, changes policy or releases
an execution slot.

### Health alert and recovery webhooks

Opt in by adding `health_alerts` to your recovery policy JSON, then use the
existing preview and version-checked full replacement commands:

```json
"health_alerts": {
  "enabled": true,
  "stall_after_seconds": 300,
  "alert_after_seconds": 300
}
```

Both durations must be explicit integers from 1 to 86400 seconds. These saved
monitor settings are independent of `automations health --stall-after-seconds`.
Changing them uses a full policy replacement, which also suppresses old pending
retries; preview the file first.

Subscribe an app-scoped webhook to `automation.recovery_health_alert` and
`automation.recovery_health_recovered` (or use an empty filter). The scheduler
observes recovery health once per minute in bounded batches. A sustained
paused, blocked or stalled condition opens one incident and emits one alert.
Reason changes and repeated polls do not send more alerts for that incident.
Healthy or inactive health closes it and emits recovery after alert capture.
An inactive recovery reports disabled automatic execution, not child success.
Incomplete/unknown snapshots cannot declare recovery and defer an uncaptured
alert. Disabling health alerts closes an incident silently.

Alert receivers are frozen at capture. Recovery goes only to original alert
receivers still enabled and subscribed to recovery. No-receiver captures are
terminal and stay quiet when a webhook is added later. The signed delivery
payload has stable `event_id` and `episode_id`, app and policy identity, health
reason, observation times, queue counts and a health URL. Deduplicate by event
ID and correlate by episode ID; historical deliveries can arrive after health
changes or out of order.

`gregale automations health --app APP` displays the saved monitor settings and
latest incident receipts. Its JSON response includes `queue.health_incident`.
These reads do not advance monitoring. The monitor does not retry or cancel
workflows, refund charges or release a child's slot. Because it runs in the
scheduler, complete scheduler outage requires a separate external heartbeat.

### Recovery history

Use `gregale automations history --app APP` to connect retained review outcomes, operator controls, retry children, worker observations and health notification captures. Filter with `--workflow NAME`, `--run UUID`, or `--policy-version N`. `--created-after` and `--created-before` accept RFC3339 timestamps; the default window is seven days and the maximum is 30 days. Pages default to 25 entries and cap at 100. Pass `--after` the returned cursor with the same filters to continue; `--json` exports one page.

The timeline combines durable events (`durable: true` in JSON) and retained observations. New policy replacements preserve the accepted configuration, actor and reason. Automatic failure pauses, worker reason changes, incident openings/changes/closings and health notification captures preserve their original associations. `policy` contains the frozen configuration on `policy_changed` events; `episode_id` correlates incident events. `evaluation_reason` records the worker classification, while `reason` preserves the actual suppression cause. Result-recorded transitions retain the child outcome and its outcome reason. Repeated unchanged polls do not append events. Events survive removal of their source review/job and later health episodes.

Durable recording starts with installation of this feature; earlier transitions are not backfilled. Durable events retain 30 days from recording and cap at 10,000 per app. Writes enforce those bounds, reads omit expired events, and the scheduler removes expired rows in batches of 1,000 each minute. Scheduler outage or backlog can delay physical deletion. The cursor fixes the original time window and durable sequence ceiling, excluding later appends. Retention can still remove events during pagination.

Legacy policy, worker and health observation entries remain snapshots of the latest state. Legacy health receipts do not infer historical policy versions or reasons. Target-specific filters omit records lacking that association. Latest observations can move or disappear across pages. Reads never execute retries or refresh worker evaluations.

### Recovery effectiveness report

Run `gregale automations report --app APP --since 7d` to inspect observed retry
applications, successes, failures, cancellations, missing results and pauses.
Use `--workflow NAME`, `--policy-version VERSION`, or RFC3339 `--created-after`
and `--created-before` boundaries (maximum 30 days). `--since` accepts whole days
such as `7d`, or Go durations such as `24h`; it cannot accompany `--created-after`.
Global `--json` exports the full report, including evidence and coverage notes.

The report groups by workflow and policy version and lists distinct actions per
observed blocker, with links into history. Timing is application-to-recorded-result
for matched child jobs, including polling delay. Missing results mean missing
proof in the chosen window, not proof of a running retry. Results can come from
attempts preceding the window. Counts summarize at most 10000 newest entries in
one consistent snapshot and explicitly flag truncation. Durable capture is
prospective, without backfill, and retains 30 days / 10000 events per app. Small
samples and retention gaps limit comparisons; these counts do not prove a policy
caused a recovery. Reading the report does not change automation state.

### Historical recovery policy evaluation

Use `gregale automations recovery-policy-evaluate --app APP --file POLICY.json
--since 7d` to compare current configuration settings with a proposed policy
against retained historical review decisions. The file uses the existing
`{"expected_version": VERSION, "policy": {...}}` update shape; evaluation rejects
stale versions and does not update the policy. Use `--workflow NAME`, RFC3339
`--created-after` / `--created-before`, or global `--json` for a structured export.
The default window is seven days and maximum is 30 days. `--since` cannot accompany
`--created-after`.

The output separates exclusions, unknown eligibility, conditional preparation
results and observed execution pressure. Historical reviews don't capture each
run's failure status. Their policy filters prove a possible status set; narrowing
an overlapping status filter is unknown. The evaluator never uses a run's current
status as past evidence. Captured saved-failure decisions can establish workflow,
trigger and generation constraints. Other historical triggers and unproven
preflight conditions remain unknown.

Preparation replay starts with empty budgets, cooldown and deduplication state at
the window start and leaves unknown candidates uncharged. It models conditional
preparations in chronological review/position order, including same-review batches,
rolling hourly budgets and generation deduplication. Operational policy pauses are
not replayed. Recorded app-wide retry applications can establish execution budget
or cooldown pressure; an absence of that proof remains `insufficient_history`.
No hypothetical execution, admission, retry success or failure pause is predicted.

Sampling is bounded to the oldest 100 matching reviews, 500 decisions and 10000
recorded applications, with separate truncation flags. Application warmup reaches
back the longer policy cooldown or one hour and is still subject to durable
retention. Only candidates reviewed under past settings and still retained are
available: unseen failures, pruned reviews/jobs and pre-recording events cannot be
reconstructed. Follow review links and application evidence identifiers to inspect
the basis for each decision. The read-only API is
`POST /v1/apps/{slug}/recovery-policy/evaluate`, requiring read access and MFA.

New policy reviews capture durable candidate evidence: exact observed run status,
preflight outcome, generation, trigger and policy version. Historical evaluation
uses this evidence even after source reviews/jobs are removed, within the shared
30-day/10,000-event history limits. Older reviews retain uncertain status-filter
inference. Capture is prospective and does not include execution inputs/outputs.

Terminal workflow failures are now captured independently of recovery-policy
filters, including while no policy exists or a policy is disabled. Historical
policy evaluation includes this inventory when you widen workflow/status filters.
The CLI reports capture start, sampled failures, review coverage, missing retry
checks and failure-sample truncation. Never-reviewed failures have unknown retry
eligibility: a saved failure alone does not prove a recovery parent or safe resume.
The inventory has a separate 30-day/10,000-entry per-app retention budget and
survives run pruning. Capture excludes cancellations, inputs, outputs and error
text, creates no reviews, and never executes recovery actions.

Recovery policy evaluation includes separately timed `later_eligibility` checks for unreviewed failures. A bounded background observer runs even without a recovery policy and checks retained recovery parents, generation fences and resume blockers. Checks refresh no sooner than five minutes and become stale after 15 minutes. These observations do not change historical preparation totals or authorize retries. See [ADR-1026](adr/1026-workflow-failure-eligibility-observer.md).

## Recover a captured initial workflow failure

List retained initial failures, then inspect one with live resume diagnostics:

```sh
gregale automations failures --app billing --name invoice-flow
gregale automations failure-recovery-plan --app billing --failure FAILURE_UUID
```

The preview explains changed generations, missing runs, unsafe resumes, operation ownership and capacity blockers. The inbox is prospective and partial: evidence lasts 30 days and shares a 10,000-entry per-app limit across all captured failure generations. Use `--after` with the returned cursor to page through the inbox.

Save an eligible target and explicitly submit the reviewed plan:

```sh
gregale automations failure-recovery-plan --app billing --failure FAILURE_UUID --key incident-42 --plan-out first-recovery.json
gregale automations recovery-start --plan first-recovery.json --reason "Reviewed incident 42"
```

Plan preparation does not create a job or resume the workflow. The plan retains the captured failure ID and expected generation zero. Creation and admission recheck retained evidence and generation; resume rechecks safety and capacity. A run that advances after review cannot be resumed by the stale plan. Keep the same plan/key when reconciling an uncertain submission. Inspect the returned native job and its execution results to distinguish admission from workflow completion. Submit these plans through `recovery-start`; the per-run journal executor cannot preserve their evidence checks.

The API exposes `GET /v1/apps/{slug}/workflow-failures` and `GET /v1/apps/{slug}/workflow-failures/{failureID}/recovery-preview`. First recovery remains an explicit operator action; automatic policies still retry saved failed recovery generations only. See [ADR-1259](adr/1259-reviewed-initial-workflow-recovery.md).

### Dry-run initial-failure policy filters

Preview policies can opt in to `initial_failure`; this trigger requires explicit
`failure_statuses` (`failed`, `dead`, or both). Use a candidate policy file in the
same `{ "expected_version": 0, "policy": { ... } }` format as `recovery-policy`:

```sh
gregale automations failure-recovery-plan --app orders --failure FAILURE_UUID --policy candidate.json
```

The command checks enabled state, trigger, workflow and failure-status filters
against a fresh recovery preview. Add `--key first-recovery --plan-out first.json`
to save a plan only when both the policy and live checks qualify. Submission
remains explicit with `recovery-start`. JSON reports `policy_reason` and
`execution_pressure: "not_evaluated"`; cooldown and budgets are not reserved.
Setting this trigger on a preview policy does not schedule initial-failure
reviews. `automatic_retry` continues to accept only `saved_failure`.

### Durable first-recovery reviews

Save a preview recovery policy that includes `initial_failure`, explicit workflow
names and failure statuses. Preparation uses the saved version, rather than a
candidate file:

```sh
gregale automations failure-recovery-review --app orders --failure FAILURE_UUID --expected-policy-version 3
gregale automations failure-recovery-reviews --app orders --limit 25
gregale automations failure-recovery-review --app orders --review REVIEW_UUID
gregale automations failure-recovery-review --app orders --review REVIEW_UUID --decision submit --reason 'Provider recovered; reviewed safe resume'
gregale automations failure-recovery-review --app orders --review REVIEW_UUID --decision cancel --reason 'Handled manually'
```

Submission and cancellation require a non-empty operator reason. A stale submission
prints the recorded review and exits unsuccessfully, including in JSON mode.

A review stores the failure evidence, generation-zero target, saved policy and
actor-attributed transition history. It expires after 24 hours and is retained
for 30 days, with at most 1000 retained initial reviews per app. Preparation and
submission repeat live safety checks. Changed policies or unavailable evidence
make a prepared review stale; expiry blocks submission. Cancellation applies to
prepared reviews. Once submitted, use native recovery-job cancellation controls
for the returned `job_id`.

Preparation shares the policy cooldown and hourly-action budget with parent-job
review preparation. Submission independently enforces the same cooldown and
hourly-action budget across first-recovery submissions, including earlier policy
versions. Charges are retained after cancellation. All generation-zero native
run-job submissions prevent another pending job for the same run, and native
resume fences direct manual races by generation. Reads do not reserve capacity.
Pending reviewed-job admission also checks review expiry and policy version;
already admitted runs continue through the native workflow lifecycle.

Repeated preparation returns the same review for a failure and policy version;
repeated submission returns the same retained job without extra charges or audit
entries. Stale and expired dispositions are inferred on read and recorded when a
submission is attempted. Terminal reviews cannot be renewed under the same saved
policy version. A changed version creates a new review subject to existing charges.

Ready webhooks use `automation.recovery_review_ready` with `initial_failure_id`
and no parent `job_id`; delivery suppresses stale, cancelled or expired reviews.
Submitted jobs use existing recovery completion and execution-result notifications.
Review transitions are in `events`; job history and receipts are on the native
recovery job. Preparation and submission are operator actions. Automatic first
recovery remains disabled.

### Worker preparation of first-recovery reviews

An enabled `preview` policy that includes `initial_failure` now lets the policy
worker prepare durable first-recovery reviews. Explicit workflow names and
failure statuses are required. The existing `automation.recovery_review_ready`
notification includes the captured failure ID and the command to inspect the
review. Operators still submit or cancel the review explicitly.

Each policy poll scans a bounded page of retained failures. A durable cursor wraps
through the inbox, revisiting temporary blockers without allowing an early
blocked page to permanently hide later failures. Cancelled, expired or submitted
reviews are not recreated under the same policy version. Policy updates reset the
scan; fresh checks, cooldowns and shared preparation budgets still apply. Worker
preparation records the system actor `initial_recovery_policy`.

This is a preparation dry run: it does not create recovery jobs or resume runs.
Blocked candidates have no review until they become eligible, and ordinary budget
or safety blockers do not consume preparation budget. Large inboxes can take
multiple polls to traverse. A crash after advancing the cursor is retried when
the scan wraps.

### Opt-in automatic first recovery

Use `automatic_initial` to let the worker submit qualifying generation-zero
failures after preparing their durable reviews. The workflow runtime must be
available when enabling the policy through HTTP. Start with `preview` to inspect
which failures qualify, then explicitly change the saved policy mode and limits:

```json
{
  "expected_version": 3,
  "policy": {
    "enabled": true,
    "mode": "automatic_initial",
    "workflow_names": ["recover"],
    "triggers": ["initial_failure"],
    "failure_statuses": ["dead"],
    "max_resume_count": 1,
    "cooldown_seconds": 300,
    "hourly_action_budget": 10,
    "hourly_execution_budget": 2,
    "pause_after_failures": 2,
    "rate_per_second": 1,
    "reason": "Bounded first recovery after provider failures"
  }
}
```

Only `initial_failure` is allowed in this mode. The automatic worker submits one
prepared review at a time using fresh policy, expiry, retained evidence,
safe-resume and native quota checks. Preparation charges the action budget;
automatic job creation charges the separate execution budget. Automatic initial
and saved-failure execution charges survive policy updates and mode switches.
Cancellation, a paused admission or another unsuccessful admission does not
refund a submitted job's execution charge.

One unresolved recovery blocks another automatic first submission for the app.
Pending native jobs, including paused jobs, and unresolved automatic or running
recoveries retain the slot. Creating a job or finishing admission does not prove
that a workflow succeeded. The review's `automatic_result` requires saved proof
of the exact generation-one execution or a final admission refusal. Missing or
superseded evidence stays outstanding, including after normal history retention;
investigate it before enabling further automatic work. Manual operator recovery
remains governed by its own native fences and quotas.

Inspect worker waiting reasons and pressure with `automations recovery-policy --app SLUG --queue` and
inspect the corresponding initial review with `automations failure-recovery-review
--app SLUG --review UUID`. Queue entries use the initial review UUID as both review
and action ID. Pending entries can be suppressed through the existing queue action
API, or cancelled through the initial-review decision command. Use the existing
policy pause/resume controls to stop and restart automatic work. Pausing also
blocks pending automatic native admission; it does not cancel an already admitted
run. Repeated proven failures pause the current policy and emit
`automation.recovery_policy_paused` to the captured app receivers.

This mode recovers generation zero once. It does not automatically continue into
later generations. Further recovery can use a separately configured
`automatic_retry` policy for saved failures. Native recovery job lifecycle
notifications remain the source of execution completion. First-recovery waiting
reasons are retained on reviews and shown in queue snapshots; existing health
incident monitors consume the combined queue. Initial recovery also emits
blocked and recovered queue notifications as described below. Repeated pause cycles after operator
resume emit distinct pause events.

Policy-change previews include a bounded initial-failure sample for this mode.
Use the failure inbox and individual previews to inspect additional candidates.

### Preview automatic first recovery before enabling it

Run `gregale automations recovery-policy --app SLUG --file policy.json --preview`
with the proposed policy and its current `expected_version`. This command does
not update the policy, prepare reviews, submit jobs or schedule notifications.
Read-scoped credentials can use the preview; applying the file still requires
write permission and the unchanged expected version.

The `initial_failures` section separates three decisions for each captured failure:

- `eligibility`: policy filters and fresh safe-resume diagnostics.
- `preparation`: whether a review would be prepared, including duplicate pending
  recovery, retention quota, shared action budget and cooldown.
- `execution`: whether that prepared review would be submitted automatically,
  including the independent execution budget and cooldown, unresolved app work,
  account recovery-job quota and HTTP runtime availability.

For example, a qualifying failure can report `eligible`, `would_prepare` and
`hourly_execution_budget`: its review could be prepared, but submission would
wait. Preview policies report `not_enabled` for automatic execution. A disabled
proposal reports `policy_disabled` and neither prepares nor submits anything.
Replacing a paused policy clears the pause, but does not refund charges or free
an unresolved recovery slot; the preview models those replacement semantics.

Counts describe at most five retained initial failures. Matching workflow and
failure-status filters sort first, followed by ascending failure ID. `coverage`
is always `partial`, and `truncated` signals more retained failures beyond the
sample. Preparation and submission decisions simulate processing this sample in
order after sampled parent-job preparations: one predicted preparation can consume
cooldown or budget for the next candidate. Pressure fields show the snapshot
before simulation. Sample order is advisory and does not guarantee the worker's
next selected failure.

The memory and PostgreSQL previews include the same retained initial preparation
charges. Expired history is excluded from review quota as if it were pruned, but
previewing never performs that pruning. Unknown automatic outcomes continue to
occupy their slot even after normal retention. The actual worker rescans and
repeats policy, evidence, safety, budget and quota checks before submission and
native admission.


### Initial automatic recovery attention alerts

Subscribe an app webhook to `automation.recovery_queue_blocked`. For
`automatic_initial`, payloads include `initial_failure_id` and `state`:

| State / reason | Operator action |
| --- | --- |
| `blocked` / `evidence_unknown` | Inspect retained run and job evidence. Missing evidence keeps the execution slot occupied. |
| `blocked` / `terminal_outcome_not_saved` | Inspect the result recorder and retain proof for the exact resumed generation. |
| `blocked` / `execution_quota` | Inspect active recovery jobs across the account and free capacity. |
| `blocked` / `job_paused` | Inspect the job and resume it when safe. |
| `recovered` / `blocker_cleared` | Inspect current review/job progress. This means the blocker cleared; it does not prove success. |

Each alert includes `review_status_url`, `queue_url`, an `operator_action`, and
`job_url` when a job exists. `previous_reason` identifies the preceding blocker.
A changed actionable reason opens a new event; repeated worker polls with the
same reason stay quiet. Clearing and then reopening the same blocker produces
a new event ID. Normal budget/cooldown waits and running jobs do not open alerts.
Policy pauses use `automation.recovery_policy_paused`.

The worker records `automatic_block_reason`, `automatic_block_sequence` and
`automatic_block_notification` on the retained review. The combined queue exposes
the latest receipt as `blocked_notification`. `queued` means webhook ledger
admission, not successful delivery. Receivers are frozen for each transition;
`no_receivers` prevents historical replay to later subscribers. Deliveries are
historical and can arrive after a later change: deduplicate by `event_id` and
compare `observed_at`.

A clearance requires a fresh observation of admission, progress or a proven
result. Reads, policy changes, cancellation and expiry do not manufacture
clearance notifications. Notifications never authorize another recovery or
release an unresolved slot. Native recovery completion events retain their
existing meaning. Saved-failure retry payloads omit `state` and
`initial_failure_id` and retain their existing once-per-action behavior.

### Diagnose and repair missing initial recovery evidence

Inspect a submitted automatic first-recovery review:

```sh
gregale automations failure-recovery-evidence --app orders --review REVIEW_UUID
```

The read-only diagnosis reports the frozen run and expected generation, available
execution proof, a reason and `repairable`. It exposes no workflow input. Only
an exact retained generation-one terminal run with matching job and admission
history, verified timestamps and a missing saved result is repairable. Running,
unknown, superseded, missing-job, missing-finish-time and conflicting-result
cases remain blocked.

Save verified proof with a stable request UUID and operator reason:

```sh
gregale automations failure-recovery-evidence --app orders --review REVIEW_UUID \
  --repair --request-id REQUEST_UUID --reason 'Restore verified terminal proof'
```

Use the same request UUID and reason when retrying an uncertain response. An
identical request returns its retained receipt; changing either conflicts.
Repair never overwrites existing result evidence. If ordinary result capture has
already saved proof, diagnosis reports `already_recorded`; no repair is needed.
A review whose worker outcome is already final reports `already_confirmed`.

The review retains `evidence_repair` with request identity, actor, reason, time
and outcome, and an `evidence_repaired` audit event. Repair saves proof and
schedules the normal worker to re-evaluate; it does not release the slot,
create another recovery, refund budget or claim success itself. Existing policy
pauses and disablement remain in force. Resume or enable through the usual
policy controls when appropriate. A confirmed failed automatic result can still
trigger the normal policy failure pause; manual results leave its counters unchanged.

The HTTP endpoints are `GET /v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence`
and `POST /v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence/repair`.
Diagnosis requires app read access and MFA; repair requires deploy write and MFA.
Repair applies to submitted `automatic_initial` reviews and manually approved
`preview` initial reviews with expected generation zero. It can inspect
unresolved older policy versions but cannot reconstruct pruned history or
force-clear unknown evidence.

### Diagnose and repair automatic retry evidence

Automatic retries now have the same verified repair workflow as first recovery.
Use the review and action IDs from the recovery queue:

```sh
gregale automations recovery-retry-evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID
```

Diagnosis is read-only and reports the child job, run, exact planned generation,
available execution proof, a reason and `repairable`. For example, a retry of a
saved generation-one failure must prove generation two. A generation-three live
run cannot substitute for missing generation-two evidence.

When diagnosis reports a repairable missing result:

```sh
gregale automations recovery-retry-evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID --repair \
  --request-id REQUEST_UUID --reason 'Restore verified retry outcome'
```

Reuse the same request UUID and reason after an uncertain response. Identical
retries return the receipt; changes conflict. The retained review exposes
`evidence_repairs`, keyed by action UUID, with request identity, actor, reason,
repair time and verified outcome. The diagnosis also includes that action's
receipt. Existing result evidence is never overwritten.

Repair requires an applied automatic retry action, matching immutable plan,
parent job and position, child request identity, workflow/run/count, retained
admission and exact terminal generation with verified timestamps. Pending or
suppressed actions, pruned jobs/history, changed lineage, newer generations,
missing finish times and conflicting results remain blocked. Diagnosis exposes
no workflow input.

Proof repair is atomic with its audit receipt. It schedules normal worker
re-evaluation; it does not submit a child, release a slot, refund budget, reset
failure counters or resume a paused policy itself. The worker performs ordinary
outcome accounting and can still pause after proven failures. Unresolved retained
older actions can be repaired after policy version or mode changes, but current
policy controls remain in force.

The HTTP endpoints are
`GET /v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence`
and the same path with `/repair` for POST. Read access and MFA are required for
diagnosis; deploy write and MFA are required for repair. First recovery continues
to use `failure-recovery-evidence` and its existing endpoints.

### Repair parent evidence before an automatic retry starts

A pending retry can be blocked when the parent's saved result is missing. Select
its parent evidence explicitly:

```sh
gregale automations recovery-retry-evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID --parent
```

Diagnosis reports `evidence_kind: parent`, the parent `job_id`, exact
`item_position`, run and required parent generation. For a planned generation-two
retry, this verifies generation one. It never substitutes a newer generation or
another item for missing evidence, and it exposes no workflow input.

When the exact retained parent terminal outcome is verifiable and its saved
result is missing:

```sh
gregale automations recovery-retry-evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID --parent --repair \
  --request-id REQUEST_UUID --reason 'Restore verified parent outcome'
```

Repair is available only while the automatic action is pending and has no child.
The native parent item, frozen workflow/run/generation and retained admission
history must agree. Update and non-null finish times must be valid for that
admission. Unknown, superseded, missing-history, missing-item and conflicting
proof stays blocked. An existing result is never overwritten. The actual
verified outcome is saved; the operator cannot force a failed outcome to make
an old retry eligible.

The review retains `parent_evidence_repairs`, keyed by action UUID, independently
of child `evidence_repairs`. Both use the same audit receipt format. Retry an
uncertain parent repair with the same request UUID and reason to get its retained
receipt, even after the worker submits a child. Changing either conflicts. A
later child repair uses its own receipt and request identity.

Parent repair creates no child, spends no execution budget, releases no slot and
does not resume a paused policy. The normal worker must recheck the current
policy version, eligibility, cooldown, budget, quota and unresolved work before
submission. Paused policies, changed versions and exhausted budgets still block
execution. Retained old pending reviews can have proof restored, but restoration
does not restore their policy authority. Suppressed and applied actions cannot
perform a new parent repair.

HTTP clients use GET
`/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/parent`
and POST to the same path with `/repair`. Diagnosis requires read access and MFA;
repair requires deploy write and MFA. Omit `--parent` or use the existing evidence
endpoint to inspect the applied child's outcome.

### Follow retry evidence alerts to the correct repair

`automation.recovery_queue_blocked` retry events now include `state`,
`evidence_kind` (`parent` or `child`), `run_id`, `resume_count`,
`diagnosis_reason`, `repairable`, `evidence_url`, `job_url` and `cli_command`.
Use the diagnosis URL or command to inspect current proof before taking action.
When `repairable` is true, the reason is `terminal_outcome_not_saved`; append
`--repair --request-id REQUEST_UUID --reason 'Restore verified outcome'` only
after diagnosis confirms the exact terminal outcome. Unknown, superseded,
conflicting or missing history cannot be forced.

A pending action points to `recovery-retry-evidence --parent`; an applied action
points to the default child diagnosis. Detailed diagnosis changes produce a new
capture. Repeated unchanged observations stay quiet. Exact saved terminal proof,
a confirmed result or verified child execution progress can emit
`state: recovered`, `reason: blocker_cleared`. This reports cleared evidence
attention, not successful execution or permission to start another retry.
Pending parents also require the exact live generation to remain retained. A
saved result cannot clear a missing or superseded parent run. Policy pauses,
suppression, dismissal and version changes alone cannot clear it.

The action's `blocked_notification` is the latest receipt, with optional `state`,
`sequence`, `evidence_kind` and `diagnosis_reason`. Old receipts remain readable.
Each transition has a stable event ID and freezes its receiver set, including
`no_receivers`. Later subscribers do not receive old captures. Historical blocked
payloads remain unchanged even when delivery happens after evidence restoration.
All execution, policy and budget decisions still belong to the normal worker.

### Trace evidence repairs and alerts in automation history

Use `gregale automations history --app orders`, optionally filtered by
`--workflow`, `--run` and `--policy-version`, to follow evidence attention:

- `retry_evidence_blocked` preserves a captured parent or child diagnosis.
- `retry_parent_evidence_repaired` and `retry_child_evidence_repaired` identify
  the verified outcome, operator actor, reason and repair request UUID.
- `retry_evidence_cleared` preserves the later evidence clearance notification.

Entries include `evidence_kind`, `resume_count`, `request_id` or
`notification_event_id`, `diagnosis_reason`, `review_url`, `evidence_url` and
`job_url` when applicable. The CLI prints these fields; JSON/API/SDK clients use
existing recovery history endpoints and models. Run, action, review, policy and
job associations let you connect these events to execution results.

New events are captured atomically with their receipts, including notification
captures without receivers. Replayed repairs and unchanged blockers create no
extra event. Old repair receipts appear as retained observations when no matching
durable event exists and their source review is retained. New durable events
survive job/review pruning; old receipts cannot be reconstructed after their
review is deleted. Reads never
backfill events. Latest notification observations describe the retained snapshot;
blocked and cleared transitions are separate immutable events recorded from now
on. Evidence clearance does not mean workflow success.

Existing account scope, filters, fenced pagination, 30-day durable retention and
10000-event per-app cap apply. History capture spends no execution budget and
changes no admission, slot or policy decision.

### Inspect current evidence from the retry queue

`gregale automations recovery-policy --app orders --queue` now displays current
parent or child evidence for returned retry entries. The API/SDK entry includes
an optional `evidence` object with `diagnosis`, `evidence_url` and `cli_command`.
The diagnosis identifies the exact run, generation, item, current reason,
repairability and observation time. Pending actions inspect parent proof; applied
unresolved actions inspect child proof. The inspection command selects `--parent`
when appropriate and performs no repair.

`wait_category` and `wait_reason` describe current pending policy controls or the
last saved worker wait. Budget and cooldown waits are separate from fresh proof:
a paused or budget-blocked action may still have repairable evidence. These fields
do not prove that all admission checks would pass. Worker evaluations and waiting
ages remain historical; reading the queue never refreshes them.

A missing or superseded live parent cannot be marked repairable by an older saved
result. Missing/expired review authority or changed plans return
`evidence_unavailable_reason`. Initial recoveries use their separate
`failure-recovery-evidence` command and omit the retry `evidence` object.

Inspection is bounded to the existing returned page and uses one consistent
read-only snapshot with the normal request timeout. It creates no child,
notification, repair receipt, budget charge or history event. Recheck the linked
diagnosis before repairing; the repair endpoint and worker independently recheck
proof, policy, quota and budgets.

### Inspect initial recovery evidence from the queue

Initial recovery entries in `gregale automations recovery-policy --app orders
--queue` now include `initial_evidence`, alongside the retry-only `evidence`
field. Its `stage`, `diagnosis`, `evidence_url` and `cli_command` point to the
initial review and `gregale automations failure-recovery-evidence --app orders
--review REVIEW_UUID` inspection command.

A prepared review has no submitted recovery job. The diagnosis identifies its
planned target generation, reports `not_submitted_automatic_initial`, and sets
`repairable: false`. The CLI explicitly explains that evidence repair is
unavailable before submission. A submitted review uses the existing exact
resumed-generation proof checks and exposes current repairability, including
running, missing, superseded and conflicting proof.

Policy pauses and saved budget/cooldown waits remain separate from diagnosis.
Every diagnosis uses the queue observation time within the same bounded read-only
snapshot. Reading prepares or submits nothing, saves no outcome, emits no alert
or history event, and changes no policy, worker evaluation or execution charge.
Recheck the linked diagnosis before an explicit audited repair.

### Follow initial-recovery attention through alerts and history

Initial `automation.recovery_queue_blocked` events now include
`evidence_kind: initial`, `run_id`, `resume_count`, `diagnosis_reason`,
`repairable`, `evidence_url` and the matching `failure-recovery-evidence`
inspection command. Terminal proof can recommend audited repair only when the
existing exact-generation checks verify it. Job pause and execution quota alerts
retain their operational guidance.

Changed evidence diagnoses create a new capture; unchanged blockers stay quiet.
Only committed worker admission, execution or recorded-result progress can clear
attention. Policy changes and queue reads cannot. `state: recovered` reports
cleared attention, not workflow success. Each capture freezes its receivers,
including `no_receivers`; historical payloads never change after repair.

The shared `automations history` timeline now records
`initial_evidence_repaired`, `initial_recovery_blocked` and
`initial_recovery_cleared`. Entries link the initial failure, review, run, exact
generation, job and repair request or notification event. Repair entries retain
the trusted actor, reason and verified outcome. Identical repair replay and
unchanged alerts add no event.

Older repair receipts appear as retained observations while their source review
exists; reads do not backfill durable events. New history capture is atomic with
receipts and survives review pruning within existing retention. Account scope,
workflow/run/version filters, fenced pagination, 30-day retention and the per-app
cap apply. SDK/receiver consumers should accept `initial` in the evidence-kind
enum and optional `initial_failure_id` in history. Apply the accompanying
migration before running the updated worker.

### Follow initial recovery lifecycle in shared history

`gregale automations history --app orders` now records new initial-recovery
lifecycle transitions: `initial_recovery_prepared`,
`initial_recovery_submitted`, `initial_recovery_cancelled`,
`initial_recovery_expired`, `initial_recovery_stale` and
`initial_recovery_result`. Use workflow, run and policy-version filters to connect
these with the existing evidence repair and blocked/cleared events.

Preparation and decision events preserve the trusted actor, reason, frozen policy
version, initial failure and review. Submission adds the committed job link.
Result events use the existing automatic worker's verified outcome and outcome
reason; creating a job or clearing attention does not prove success. Generation
on a prepared event identifies the planned recovery target.

Identical preparation/decision replay and inspection reads append no event. Expiry
or staleness becomes history only when committed by a mutation, not when observed
by a read. The events are written atomically with their state change, remain after
source review/job pruning, and follow existing account scope, filters, fenced
pagination, 30-day retention and per-app cap. Older review events are not backfilled.
Preparations and decisions cover preview and automatic-initial reviews; final
results use the existing automatic worker. Apply the accompanying migration
before deploying the updated worker.

### Manually approved initial recovery completion

A preview-mode review approved with `failure-recovery-review --decision submit`
is observed after submission even if the policy is subsequently changed,
disabled or paused. Inspect `result` on the review or the durable
`initial_recovery_result` in automation history for verified success, failure or
cancellation. The CLI review inspector prints the outcome and generation.
`automatic_result` remains reserved for automatic-initial execution.

Completion requires the exact job and generation-one saved execution evidence
(or a verified terminal admission refusal). Missing or unverified proof remains
unresolved and the submitted review survives normal retention pruning. Manual
observation does not spend automatic budgets, change automatic failure counters
or authorize additional retries. Use `failure-recovery-evidence` to diagnose or repair retained exact-generation
proof, as described below.

### Repairing manual initial recovery evidence

The existing `gregale automations failure-recovery-evidence --app APP --review REVIEW`
command diagnoses submitted preview-mode initial recoveries as well as automatic
initial recoveries. Add `--repair --request-id UUID --reason REASON` to save a
missing result when the retained job, generation-one admission and completed run
all match. The repair receipt and `initial_evidence_repaired` history event record
the operator and reason; replay the same UUID and reason to retrieve that receipt.

Repair wakes the manual completion observer even after the current policy is
changed, disabled or paused. The observer then records `result` and one durable
`initial_recovery_result`. Repair does not directly confirm completion, submit a
retry, change automatic budgets or resume the policy. Missing jobs/admission,
missing completion timestamps, superseded generations and conflicting saved
proof remain non-repairable. An already confirmed review reports
`already_confirmed`; an unsupported preview review reports
`not_submitted_manual_initial`.

### Attention for manually approved initial recoveries

Submitted preview-mode reviews expose `attention`: the latest committed observer
reason, diagnosis, unresolved `block_reason`, observation timestamp, evidence
URL, CLI command and operator guidance. Review reads leave it unchanged; use
`failure-recovery-evidence` for a fresh diagnosis. The review inspector prints
this information alongside the eventual verified `result`.

Subscribe to `automation.recovery_queue_blocked` for actionable missing proof,
unpersisted terminal outcomes or a paused manual job. Unchanged polls stay quiet;
a changed evidence diagnosis gets a new event. Recipients are frozen when an
event is captured, and `no_receivers` events do not replay to later subscribers.
Manual alert `queue_url` links to the initial review list.

A verified result or exact-generation execution progress can emit a recovered
transition. A recovered alert clears an evidence blocker and does not imply
successful execution. Policy changes, disablement, automatic pause, admission
waits or unverified progress cannot force clearance. Shared history retains
`initial_recovery_blocked` and `initial_recovery_cleared`, including the observer
actor, frozen version and notification event ID. Tracking continues independently
of current policy settings and does not alter automatic budgets or failure
counters, unpause a policy or authorize another retry.

### Independent recovery polling

Automatic execution and manual initial-recovery observation use separate bounded
scheduler passes. A large manual backlog no longer consumes automatic execution
slots. The scheduler also gives manual monitoring its own allowance when
automatic work remains continuously available.

An error ends only its affected pass; the other pass and downstream recovery
work still run within the shared scheduler context. Database outages or parent
context cancellation can still affect both. Manual completion, diagnosis,
attention and repair behavior remain independent of current policy settings.

### Manual observation failure retries

A deterministic data error while observing a submitted manual review is deferred
so later reviews can progress. Inspect `observation_failure` on the review or
`failure-recovery-review` output for the safe error code, consecutive attempts,
last failure time and next retry time. Retry delay doubles from one minute up to
30 minutes; successful observation clears this metadata. Attempts saturate at 32.
Reads do not retry work, and a retry does not grant another execution or prove a
final result. Failed PostgreSQL observation writes are rolled back before the
failure and next retry time are saved together.

Database outages, cancellation and unknown errors still end the affected pass.
Automatic execution errors retain their existing handling. Existing exact-proof
evidence repair can wake manual observation through its persisted due clock.

### Automatic policy polling failure retries

Inspect `automatic_execution.polling_failure` on a recovery policy or automatic
queue response for a safe error code, consecutive attempts, last failure time and
next retry time. The policy CLI prints the error and retry time. Deterministic
policy/data errors roll back execution writes, then defer only that policy so
other apps can progress. Retry delay doubles from one minute to at most 30 minutes;
attempts saturate at 32. Successful polling, a policy update or explicit operator
resume clears this metadata. Read-only inspection does not retry work.

Polling failures do not increment execution failure counters, consume budgets,
submit another job or grant execution authority. Database outages, cancellation,
transaction failures and unknown errors still end the affected pass. Apply
migration `20261014092444724` before deploying the updated automatic worker.

### Persistent polling failure alerts

Subscribe an app webhook to `automation.recovery_polling_status` to receive a
`failed` alert after three consecutive deterministic polling failures, followed
by `recovered` when polling succeeds. With normal backoff, the third failure is
observed after about three minutes. Unchanged code and policy version stay quiet;
changing the retry time or attempt count alone does not send another alert.

The payload includes the app, manual-review or automatic-policy scope, safe error
code, attempts, next retry time and inspection URL. `monitoring_only` is true:
recovered confirms successful polling, even if execution is still pending or no
new job was submitted. Policy updates, disablement or operator resume do not
prove recovery; a subsequent successful poll must confirm it.

Inspect `polling_notification` on the review or policy for the last frozen receipt,
including event ID and recipients. The CLI prints its state and code. `queued`
means durable outbox admission, while `no_receivers` never replays to subscribers
added later. New transitions use current recipients. Automation history retains
`polling_failed` and `polling_recovered` with their frozen receipts. Apply migration
`20261014092444767` before deploying the worker.


### Retire an unused baseline

Use **Retire this baseline** in the dashboard, review the name and revision, then
confirm permanent retirement. Or use an explicit stable UUID from the CLI:

```bash
gregale automations live baseline retire --app billing --name paid-invoice \
  --baseline-name production --retirement-id 00000000-0000-4000-8000-000000000012 \
  --expected-revision 3 --confirm-retirement --json
```

Retirement reserves the name permanently, frees one of the 20 active-name slots,
and releases its suite retention pin. It does not immediately delete evidence;
other active names or recovery windows can still pin the same suite. Each
automation can have at most 100 lifetime names to bound retained audit data.

Name lookup now returns HTTP 410. Named CI gates exit 4 with
`baseline_retired`, so CI must explicitly choose another active baseline. A gate that resolved the old receipt before retirement keeps its
snapshot. Active listings omit retired names. Read `baseline history` with the
retired name to inspect its promotion receipts and attributed retirement receipt.
Old promotion retries return their historical receipt without reactivation.
Retry an uncertain retirement with the original UUID and expected revision. A
revision conflict requires reviewing the new head before submitting a new intent.
