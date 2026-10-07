# Build automations in Gregale

An automation runs steps against an app, with a manual, scheduled, or event start.
The app must have a live default deployment. Automations use the existing durable
workflow executor, with retries, dependencies, waits, and run history.

Create and manage definitions through the automation authoring API or generated
SDKs. Save a draft with a trigger and steps, including input mapping, event filters,
waits, and failure handlers as needed. Validate reports errors without executing
or saving steps; publishing makes the saved draft available to future runs.
This change contains backend APIs only; no dashboard editor is included.

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
`--mock-attempts-file` maps action names to ordered outcomes so you can test
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
  },
  "on_item_failure": "continue"
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
lasts for the existing 30-day delivered-event identity retention. Side effects
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
retained ID returns 409. Deduplication follows the existing 30-day event
identity retention.

An accepted automation run receives a CloudEvents envelope with source
`gregale.inbound.generic.ENDPOINT_ID`, the signed event ID and type, and the
complete submitted JSON as `data`. The durable receipt and existing event
fanout handle retries and filters. Without an automation binding, Gregale
delivers the verified JSON to the configured app path and includes the verified
event metadata in `x-gregale-webhook-*` request headers.

Apply migration `20261006062359228_generic_inbound_webhook.sql` before creating
generic endpoints. The down migration is forward-only; remove generic endpoints
before rolling back application binaries.

## Resume after a terminal failure

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
continue to use the current default deployment, as they do during retries.

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

Tenant/workflow pairs share the app's active-run quota and are evaluated in
least-recently-admitted order. Admission priority persists through scheduler
restarts, skipped minutes, deployment changes, and run retention. Paused
schedules are excluded. This distributes scarce admissions across customers;
quota skips and downtime still do not produce catch-up runs.

Existing alert rules accept four notification-only workflow metrics:

| Metric | Observation |
| --- | --- |
| `workflow_failures` | Failed or dead runs completed in the selected window; excludes cancellation |
| `workflow_schedule_quota_skips` | Due schedule occurrences skipped for app quota in the selected window |
| `workflow_pending_age_seconds` | Age of the oldest eligible pending run since it became eligible; excludes future retries; zero if none |
| `workflow_waiting_age_seconds` | Age of the oldest currently awaiting step, zero if none |

Scope an alert to one app or the authenticated account. Age signals describe
current state rather than a window average. Waiting age includes intentional
timers, callbacks, conditions, and event waits, so choose thresholds suitable
for the workflow. These metrics support webhook notifications only.

App-handler steps retry HTTP 408, 425, and 429 using the configured attempt
budget. A downstream `Retry-After` (seconds or HTTP date) extends ordinary
backoff, capped at one hour, and survives scheduler restarts. Ordinary 4xx
validation/authorization errors remain terminal. All attempts retain the
same step idempotency key; app handlers must deduplicate their side effects.
Unsafe outbound actions keep their existing no-retry policy.
