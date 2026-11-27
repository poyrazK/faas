# Share an API sandbox

Create a sandbox from a release to hand a working API to a developer or coding
agent. The sandbox runs the selected release artifact on a new app with a
fresh managed PostgreSQL database. It expires after 24 hours.

## Create and share

1. Open a live or retained release and expand **API sandboxes**.
2. Optionally enter a GET route such as `/notes` to check that the application
   can serve a request against its fresh database. Choose **Create sandbox**.
   Gregale prepares PostgreSQL, runs the release command, and starts the app
   in the background. You can close the page after submitting.
3. Gregale requests `/openapi.json` and the optional smoke route through the
   public sandbox URL. Once the checks pass for the exact sandbox deployment,
   choose **Create invite**. Anyone holding the link can use the sandbox until
   the sandbox expires. If setup fails, the panel shows which step failed.
4. To close access early, choose **End sandbox**. Gregale also removes the
   sandbox resources when the lease expires.

The first version supports single-process HTTP apps in personal workspaces
with plans that support developer PostgreSQL and consumer keys. Use a release
command that can create its schema using `MIGRATION_DATABASE_URL`. The
serving process gets the sandbox's `DATABASE_URL`. The release command can
check `GREGALE_API_SANDBOX=1` to load test data. The
`rest-api-postgres-python` template demonstrates this.

Gregale provisions new bindings and omits source app environment values,
stored secrets, service bindings, and database contents. Use an artifact and
release command designed to run against test data. Sandbox egress uses the
app's dedicated database path and denies general outbound traffic.

## Reuse a scenario

Open **Define scenario** on the release page to save a name, synthetic fixture
data, seeded IDs, and example requests. Use **Create fresh copy** to provision a
separate sandbox and PostgreSQL database for that scenario. Editing the scenario
changes future copies. Existing sandboxes keep the fixture and guide they started
with.

The release command can read the `seed` object from
`GREGALE_API_SANDBOX_SEED` and insert its fixtures using
`MIGRATION_DATABASE_URL`. Your release command defines the shape of the fixture
and the IDs it creates. Use synthetic data for the seed and any examples. The
invite includes the scenario name, `seeded_ids`, request examples, and expected
HTTP statuses. The raw seed is available through the owner API.

The Python Notes template accepts this scenario.

```json
{
  "name": "Create a note",
  "seed": {
    "notes": [{ "id": 42, "body": "A note for integration testing" }]
  },
  "seeded_ids": { "Sample note": "42" },
  "requests": [
    { "title": "Find the sample note", "method": "GET", "path": "/notes", "expected_status": 200 },
    {
      "title": "Create another note",
      "method": "POST",
      "path": "/notes",
      "body": { "body": "My integration test" },
      "expected_status": 201
    }
  ]
}
```

Use `PUT /v1/apps/{slug}/deployments/{deployment}/api-sandbox-scenario` to
save a scenario or `GET` on the same path to read it. The response includes a
`revision`. Pass it as `scenario_revision` when
creating a sandbox with a new UUID. A retry for an accepted UUID can use the
original revision, even after an edit. A new copy requires the current
revision. The example requests are for the recipient to run after sharing.
The existing OpenAPI and optional GET smoke checks control readiness.

## Save scenarios in the app library

Open **API sandboxes** on any eligible release and use **New library scenario**
to save synthetic fixtures and requests under a unique name. The app can hold
up to 50 library scenarios. Use **Scenario for this release** to select a
library scenario for a fresh copy, run, partner handoff, or comparison. Editing
or removing a library scenario leaves the definition pinned to existing runs.

List names, stable IDs, and current revisions with
`GET /v1/apps/{slug}/api-sandbox-scenarios`. Read or save a definition at
`/v1/apps/{slug}/api-sandbox-scenarios/{scenario}`. To create, PUT a new UUID
and send `expected_revision: ""` with the scenario. To edit, send the current
revision. To remove, send DELETE with the current `expected_revision` as a query
parameter. Pass the selected revision to the existing run and comparison APIs.

### Create a scenario from Debugger

Open an individual request in Debugger and choose **Create sandbox scenario**.
Gregale starts an editable draft on the retained release that served the
request. It fills in the HTTP method and uses a matching route declaration or
stored OpenAPI path when available. Enter a concrete path with synthetic values,
any request body and seed data the check needs, the expected status, and any
JSON assertions. Choose **Save to library** to keep the scenario for future
releases. The source request ID appears in the private scenario editor.

Choose **Open release sandbox checks** after saving to run the scenario on
the affected release. For a fix awaiting release approval, select the same
scenario and use **Run sandbox check** on the candidate. For a retained fix,
select the scenario on the candidate and choose **Compare with previous
release** with the affected release as the baseline.

CI and agents can read
`GET /v1/apps/{slug}/debug/requests/{req_id}/sandbox-scenario-draft`.
Pass its `source_request_id` in the library PUT with the reviewed scenario
and an empty `expected_revision`. Use `deployment_id` to run a check on the
release that served the source request.

## Run and share a scenario

Select **Run scenario** on a release with a saved scenario. Gregale provisions a
fresh sandbox and database, checks readiness, and runs the saved requests in
order. The run pins both the release and the scenario revision. The sandbox
closes for cleanup as soon as the run finishes, and the report remains on the
release page.

Each request checks `expected_status`. To check a JSON field, add an assertion
with a JSON Pointer and a scalar value.

```json
{
  "title": "Check the order was refunded",
  "method": "GET",
  "path": "/orders/42",
  "expected_status": 200,
  "assertions": [{ "pointer": "/status", "equals": "refunded" }]
}
```

A failed check stops the run. Reports show the expected and received HTTP
statuses, elapsed time, request IDs when supplied by the gateway, and the
failed assertion's field. Reports omit request and response bodies, assertion
values, URL query strings, and credentials. An interrupted worker leaves its
completed checks in the report. Start a fresh run to retry an interrupted
workflow.

Choose **Share report** on a finished run to give a partner or support a
read-only link. Links expire after 24 hours, and generating a new link replaces
the previous one.

For CI or an agent, use
`PUT /v1/apps/{slug}/deployments/{deployment}/api-sandbox-runs/{run}` with a new
run UUID and `{"scenario_revision":"..."}`. Poll the same path with `GET`.
A repeated PUT with the same run UUID and scenario revision returns the existing
report. Issue a report token with POST on the run's `/report-link` subresource.
Recipients can read `GET /v1/api-sandbox-runs/{run}/report` using that token as
a bearer credential.

## Compare API releases

Open **API sandboxes** on the candidate release and select a scenario. Choose an
earlier eligible **Baseline release** and select **Compare with previous
release**. Each run gets a separate sandbox and database. Both use the same
pinned scenario revision and seed data.

- **Both passed** means both releases passed every scenario check.
- **Regression detected** means the baseline passed and the candidate failed an
  observed behavior check, such as a response status or JSON assertion.
- **Inconclusive** covers a failed baseline, interrupted runs, and failures to
  provision or reach a sandbox.

The report includes both retained release IDs and run receipts, the pinned
scenario revision, and the first failing step when available. Copy its request
ID to investigate that request. Run receipts stay on the release page after
sandbox cleanup.

For CI or agents, list comparisons and eligible earlier baselines with
`GET /v1/apps/{slug}/deployments/{candidate}/api-sandbox-comparisons`. Use
`PUT /v1/apps/{slug}/deployments/{candidate}/api-sandbox-comparisons/{comparison}`
with a fresh comparison UUID and this request body.

```json
{
  "baseline_deployment_id": "5d5aa6f5-1132-4a7a-99e7-26f1f22e3541",
  "scenario_revision": "e1f62f28-09d5-4d33-8313-d1c095a914da"
}
```

A repeated PUT with the same UUID and inputs returns the existing comparison.
Poll the same path with GET until `verdict` is `passed`, `regression`, or
`inconclusive`. Treat only `passed` as a successful comparison. This workflow
uses retained live or superseded deployment artifacts.

## Check a build before release approval

Open the release approval page for a build waiting before its production
release command. Select **Run sandbox check** to compare the held artifact
with the current live production release. Choose a library scenario or the
live release's saved scenario for the check. Both runs use the same seed data
in separate disposable databases.

The check is available for supported single-process HTTP builds when one
eligible release serves all production traffic. Save a library scenario from
the live release’s API sandboxes if you need a scenario to run.

The approval page shows the current verdict and the first failing step
when available. The sandbox worker validates both artifact pins before
copying the builds. A change to the candidate artifact, serving baseline,
baseline artifact, or saved scenario makes the evidence **Stale**. Run
another check against the current inputs. Review the evidence and record
your decision using the approval controls.

For CI or agents:

1. Read `GET /v1/deployments/{id}/approvals/sandbox-checks` on the candidate.
   With `availability: ready`, use the returned `baseline_deployment_id`
   and a revision from `library_scenarios` or the live release's
   `scenario_revision`.
2. Send those two fields to
   `PUT /v1/deployments/{id}/approvals/sandbox-checks/{check}` with a new
   check UUID. Retry with the same UUID and original inputs when needed.
3. Poll the same URL with GET. Use the top-level `verdict` for current
   evidence. The nested `comparison` retains the original run receipts.
   Only a top-level `passed` verdict represents a passing check against
   the current inputs.

## Run a regression suite before release approval

On the approval page for a held build, choose **Set up suite** and save up to
five scenarios from the app’s library. Use **Run regression suite** to compare
the held build with the live baseline. The suite runs the comparisons in the
saved order. Each comparison uses a fresh pair of sandboxes, and the worker
waits for available capacity between comparisons.

To launch automatically, enable **Run suite before approval** when editing the
app’s suite. An eligible build queues one advisory suite when it reaches the
hold before its release command. Replaying that hold or restarting the worker
reuses the same run. The approval page shows queue progress and any wait for an
eligible baseline or sandbox capacity.

Starting the suite pins both release artifacts, the suite revision, and each
scenario definition. The report includes the result and first failing assertion
for each comparison. An edited suite, changed scenario, or changed serving
baseline makes earlier evidence **Stale**. Review the suite report as part of
the release approval decision.

CI and agents can use the same workflow.

1. Read or save the app suite at `/v1/apps/{slug}/api-sandbox-suite`.
   Send `scenario_ids` in the desired order and `expected_revision` from
   the last read. Use an empty revision for the first save. Set
   `run_before_approval: true` to turn on automatic runs.
2. Read the eligible baseline from
   `GET /v1/deployments/{id}/approvals/sandbox-checks`. PUT its
   `baseline_deployment_id` and the saved `suite_revision` to
   `/v1/deployments/{id}/approvals/sandbox-suite-runs/{run}` with a new
   run UUID. Retry with the same UUID and inputs.
3. Poll that run URL until `finished_at` is present. Read the run’s
   `verdict` and the comparison for each scenario. GET the collection
   URL to retrieve recent run IDs, progress, and `automatic_status` for
   a held release. An automatically queued run includes `automatic: true`.

### Require a passing suite for production release commands

Save a suite and turn on **Require a passing sandbox suite** in the app’s
release approval policy. A production release command that waits for human
approval now needs both the required approvals and a passing suite. Gregale
queues the suite automatically when the build is held. The approval page
shows the reason for the hold and lets you rerun the suite after a failure or
a change to the release artifacts, suite, or scenarios. You can rerun it after
human approval while the release is still within its approval timeout.

Gregale checks the latest run against both current release artifacts, the
saved suite, and every selected scenario revision when it queues the release
command. Each comparison must pass on both releases. The command stays held
while evidence is missing, failed, or stale. An unresolved hold expires at
the policy timeout.

CI and agents can set `require_passing_sandbox_suite: true` on the app’s
`/v1/apps/{slug}/release-approval-policy`. Read `release_gate` on the suite-run
collection to see the current requirement status and the evaluated
`suite_run_id`. Once the command is queued, `release_gate.status` is
`admitted` and the ID identifies the run accepted at admission.

## Invite a partner

Save a scenario for the release, enter a **Partner label**, and choose
**Invite partner**. Share the one-time link after it appears. The partner gets
a fresh copy and can accept the link as soon as the API checks pass. If
they try early, they can retry the same link. The link and the sandbox key
both expire with the copy.

The release panel shows redemption and the first successful request served
by the API using that partner’s key. **Revoke partner access** disables the
link or the redeemed key immediately. The owner can replace an unredeemed
link or issue a new link after revoking access. Use a new partner copy when
you want a fresh database.

To automate a handoff, pass `partner_label` and `scenario_revision` on the
sandbox creation request. Use `POST` on its `/partner/invite` subresource to
get the one-time token. A recipient or CI job can redeem the token directly.

```sh
curl --fail-with-body -X POST -H "Authorization: Bearer $INVITE_TOKEN" \
  "$GREGALE_CONSOLE/v1/api-sandboxes/$SANDBOX_ID/redeem"
```

The response returns the sandbox guide and a new API key in `token`. Keep the
key for future API calls. The exchange returns it once. Use `GET` on the
sandbox's `/invite` endpoint with the redeemed key to read the guide again.
Use `DELETE` on the owner's `/partner` subresource to revoke access.

## Use the invite from an agent or script

An invite has the shape `/api-sandbox/<sandbox-uuid>#<bearer>` on the console
origin. Treat the fragment as a secret. An agent can read the invite without
a Gregale session:

```sh
curl --fail-with-body -H "Authorization: Bearer $SANDBOX_KEY" \
  "$GREGALE_CONSOLE/v1/api-sandboxes/$SANDBOX_ID/invite"
```

Use the returned `url` as the API base URL and `openapi_url` to download the
FastAPI document. Send the same authorization header to the sandbox URL.
The response also provides both deployment IDs and the expiry. The key works
only on the sandbox app, and it cannot manage the owner account.

Owners using the REST API create a sandbox with
`PUT /v1/apps/{slug}/deployments/{deployment}/api-sandboxes/{sandbox}`. Use a
new sandbox UUID and optionally send `{"smoke_path":"/notes"}`. Reuse that UUID
and the same request body if the response is interrupted. Poll
`GET /v1/apps/{slug}/deployments/{deployment}/api-sandboxes` for progress.

- `preparing_database`, `running_migrations`, `starting_release`, and
  `verifying_api` indicate setup is in progress.
- `live` includes a verification receipt with the checked deployment ID,
  timestamp, and HTTP statuses. Create an invite at the sandbox's
  `/invites` subresource.
- `failed` includes a failure code, an explanation, and the HTTP status
  when applicable. Fix the issue and create a sandbox with a new UUID.

The OpenAPI URL must directly return a 2xx response with an OpenAPI 3.0 or
3.1 JSON document no larger than 1 MiB. An optional smoke route must directly
return a 2xx response to a GET with a sandbox read credential. Both checks
require gateway evidence that the exact sandbox deployment served the
response. Setup retries transient failures for up to 20 minutes.

### Partner-driven scenario runs

Choose **Start partner run** on a saved scenario and create a one-time invite.
A partner run uses a fresh copy of the pinned release and scenario. Include at
least one request for the partner, followed by a GET with a JSON assertion.
Partner requests check HTTP status. Put JSON assertions on the final GET.

For scripting, create a run with `mode: "partner"` in the scenario-run PUT and
issue its invitation with `POST .../api-sandbox-runs/{run}/invite`. After the
report reaches `awaiting_partner`, the recipient can redeem the invitation
with `POST /v1/api-sandboxes/{run}/redeem` using the invitation token as a bearer.
The response includes the sandbox base URL, the scenario guide, the partner
step count, and a key scoped to that copy.

Run the partner requests from the actual SDK, app, or CI job. Send the saved
method, path, and query string in order and wait for each response before
starting the next step. The gateway rejects an out-of-order or concurrent step
with HTTP 409 before it reaches the app. A guest HTTP status that differs from
the saved expectation fails the run. After the partner requests pass, Gregale
sends the final GET and checks its assertions. If the sandbox expires before
the partner finishes, the report keeps the completed steps. If a dispatched
request produces no verifiable response within two minutes, the run is
interrupted and the sandbox closes.

The finished report shows the saved paths without queries, expected and
observed statuses, durations, and platform-format request IDs. It records no
request or response bodies, headers, or assertion values. Share the finished
report with the same report link used for automated runs.

### Test a Stripe webhook

Save a scenario with `stripe_webhook` and one final GET with JSON assertions.
Choose **Run webhook scenario** or start the same automated run through the
scenario-run API. Set `delivery_path` to the app route that handles Gregale's
verified inbound webhooks. The route receives the event through the regular
invocation queue with `X-Faas-Invocation-Source: inbound_webhook`. Configure
your release command to seed the records your fixture refers to.
[Inbound webhook delivery](inbound-webhooks.md#delivery)

```json
{
  "name": "Apply a paid Stripe invoice once",
  "seed": {"subscriptions": [{"id": "sub_demo", "status": "pending"}]},
  "stripe_webhook": {
    "delivery_path": "/internal/stripe",
    "event_type": "invoice.paid",
    "data_object": {"id": "in_demo", "object": "invoice", "subscription": "sub_demo", "paid": true}
  },
  "requests": [{
    "title": "Check subscription and entitlements",
    "method": "GET",
    "path": "/subscriptions/sub_demo",
    "expected_status": 200,
    "assertions": [
      {"pointer": "/status", "equals": "active"},
      {"pointer": "/entitlement_count", "equals": 1}
    ]
  }]
}
```

Gregale creates a synthetic event ID and a temporary signing secret for the
run. It sends the same fixture twice through signature verification and checks
that both submissions return the same durable receipt and that the second is
marked as a duplicate. Delivery targets the pinned sandbox deployment.

The runner waits up to 40 seconds for the invocation to complete, then polls
the final GET for up to 20 seconds. The report shows which stage failed and
includes the receipt ID, delivery state and attempt count when available. It
keeps the fixture, signing secret, and assertion values private. This flow
uses local fixtures and runs without contacting Stripe.

### Expect an outbound webhook

Use **Load outbound webhook example** in the scenario editor to check a
Gregale-managed async job's `job.finished` callback. Save one POST job with
`expected_status: 202` and set `outbound_webhook`. The POST is admitted to the
invocation queue and runs on the pinned sandbox deployment. Set the path to a
handler that returns the JSON result to include in the callback.

```json
{
  "name": "Retry the export completion webhook",
  "seed": {},
  "requests": [{
    "title": "Start a test export",
    "method": "POST",
    "path": "/exports",
    "body": {"export_id": "export_demo"},
    "expected_status": 202
  }],
  "outbound_webhook": {
    "retry_once": true,
    "assertions": [{"pointer": "/export_id", "equals": "export_demo"}]
  }
}
```

Select **Run outbound webhook scenario** or start an automated run through the
scenario-run API. Gregale's existing webhook dispatcher signs and sends the
callback to a receiver owned by the run. By default, the receiver acknowledges
with HTTP 204. With `retry_once`, it returns HTTP 503 on the first verified
attempt and HTTP 204 on the retry. Both attempts must retain the same delivery
ID and carry the same job result. The receiver is handled inside the dispatcher
and uses Gregale's built-in aggressive retry policy.

The report includes job acceptance, callback enqueue, attempt numbers, response
statuses, timestamps, and the delivery ID. It includes any failing JSON pointer.
The runner waits up to 75 seconds for the job and callback together, then closes
the sandbox and removes its receiver. [Async webhook delivery](event-driven.md#notify-webhooks-and-set-retries-for-async-invocations)

### Verify a partner's webhook receiver

Use **Invite webhook partner** on a saved outbound webhook scenario. Share the
one-time link with the partner. The setup expires after 24 hours and reserves a
run ID without starting a sandbox.

1. The partner opens the link and configures the exact HTTPS test receiver URL
   to return the displayed proof in an HTTP 200 GET response. The URL uses
   port 443 and has no credentials, query parameters, or fragment.
2. The partner verifies that endpoint, saves the signing secret in the receiver,
   saves the outcome token in CI, and confirms readiness. Verification issues
   both credentials once. The test receiver handles POST at the same URL.
3. The owner selects **Run with partner receiver**. Gregale runs the saved async
   job on a fresh sandbox and sends its signed `job.finished` callback with the
   test result. After a successful first response, Gregale simulates losing the
   acknowledgement and retries the same delivery ID. A partner run always
   exercises this retry.
4. The receiver verifies `X-Faas-Webhook-Signature` against the raw request body.
   Use HMAC-SHA256 with the run signing secret and the message
   `<X-Faas-Webhook-Timestamp>.<X-Faas-Delivery-Id>.<raw body>`. The signature
   header has a `sha256=` prefix followed by the hex digest. The receiver can
   deduplicate by `X-Faas-Delivery-Id`. `X-Faas-Webhook-Attempt` is `1` on the
   first delivery and `2` on the retry.
5. After attempt 2, partner CI counts the committed business effects for that
   delivery and submits the count within the five-minute evidence window.
   Use the run ID from the invitation or `payload.data.job_id` in the callback.
   A 409 can occur while the dispatcher is still recording the retry. CI can
   retry the submission briefly using the same delivery ID and count.

```sh
curl --fail-with-body -X POST "$API_BASE/v1/api-sandbox-runs/$RUN_ID/receiver-outcome" \
  -H "Authorization: Bearer $SANDBOX_OUTCOME_TOKEN" \
  -H 'Content-Type: application/json' \
  --data "{\"delivery_id\":\"$DELIVERY_ID\",\"applied_count\":$APPLIED_COUNT}"
```

The run passes when both delivery checks pass and the partner reports one
committed effect. The shareable report identifies the attempts observed by
Gregale and the business-effect count reported by the partner. A missing outcome
or any other count fails the run.

Release automation can create the invite by POSTing `scenario_revision` to
`/v1/apps/{slug}/deployments/{deployment}/api-sandbox-runs/{run}/receiver-invite`.
Poll that path with GET. Once the receiver is ready, create the run using the
same ID and revision, with `"partner_webhook": true` in the run request.
