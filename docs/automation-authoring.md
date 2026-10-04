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
    "steps": [
      { "name": "record", "path": "/record-payment", "input": { "invoice_id": "{{input.data.invoice_id}}" } },
      { "name": "receipt", "path": "/send-receipt", "depends_on": ["record"], "retry": { "max_attempts": 4, "backoff": "exponential" } }
    ]
  }
}
```

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
which requires explicit acknowledgement (`restore_manifest: true`). Drafts do
not change the published automation. Definition quotas count YAML and saved draft
names together, with the plan's existing workflow limits.

Pause automatic starts to stop future scheduled/event admissions. Existing runs
and already accepted events continue. Republishing preserves pause. Resume to
start new automatic admissions again; schedules skip missed minutes and re-arm
at the next evaluation. Manual sample runs remain possible while paused and
execute the published definition with real app side effects.

The preview runtime requires `FAAS_WORKFLOWS_ENABLED=1` on apid and schedd and the
existing gateway executor configuration. The list response reports when runtime,
plan, app maintenance, tenant requirements, or missing deployment prevents
execution. Saving and validating drafts alone does not enable the runtime.
Full revision history is deferred.

## Simulate before publishing

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
produce warnings. Unknown step names or mocks for waits/control steps return 400.

Waits remain `would_wait`; dependent actions are blocked. Simulation does not
advance time, deliver callbacks/events, call condition checkers, inject failures
or model retries. Exception handlers remain blocked until the source outcome
is known, and are skipped with `route_not_taken` after a successful mock or
skipped source. Input/guard evaluation errors appear as trace errors and issues;
no failure-handler context is fabricated. An action with `on_timeout` cannot
mock the exact reserved output `{"timeout":true}`: this would represent a timeout
outcome and returns 400. Other output shapes remain ordinary successful mocks.

`definition_valid` reports structural, plan and binding validation independently
of sample-data issues. Invalid definitions return 200 with issues and an empty
trace. `complete` means every root resolved or skipped under your supplied mocks;
it does not verify real handler behavior, trigger admission or live availability.
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
See [ADR-569](adr/569-automation-simulation.md).

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
    "action": {
      "run": "send_receipt",
      "input": {
        "recipient": "{{input.item}}",
        "position": "{{input.index}}",
        "invoice_id": "{{input.input.invoice_id}}"
      },
      "timeout": "30s",
      "retry": { "max_attempts": 3, "backoff": "exponential" }
    }
  }
}
```

Items run sequentially. Gregale snapshots the array and prepares every mapped
input before executing the first item. Retried and recovered items reuse those
inputs, and completed items stay complete. Without an action `input`, the handler
receives the item itself. Explicit mappings read `input.item`, the zero-based
`input.index`, and `input.input` for the original run input. They may also read
outputs of the parent's direct dependencies. Data containing template delimiters
stays literal; it is never evaluated again.

`items` is a reference without braces, such as `input.recipients` or
`steps.lookup.output.recipients`. A referenced step must be in the parent's
`depends_on`. A missing or non-array source fails before any action runs.
The action accepts exactly one `run`, `path` or managed `outbound` target and may
specify input, method, timeout and retry. A parent guard can skip the whole batch.
Nested loops, body guards/dependencies, waits, joins and exception routes are
not supported in this first version.

The parent's output is an array of successful item outputs in input order;
downstream steps depend on `send` and read `{{steps.send.output}}`. An empty list
succeeds with `[]`. Successful non-JSON handler responses become JSON strings;
empty responses become null. Processing stops on an item failure, retains the
completed prefix on the parent, and skips remaining items. Downstream actions
requiring the batch do not run after that failure.

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
first if needed. See [ADR-566](adr/566-bounded-workflow-iteration.md).

## External service actions

An `outbound` step calls an existing managed integration bound to the automation's
app. Use its canonical UUID and an allowed fixed relative path. Gregale injects
the integration's sealed credential inside outboundd. For example:

```json
{
  "name": "update-crm",
  "outbound": {
    "integration_id": "00000000-0000-0000-0000-000000000001",
    "method": "POST",
    "path": "/v1/contacts",
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
Permissions and revocation apply at execution time. This version accepts no
custom headers, query parameters, dynamic paths, or arbitrary external URLs.

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
[ADR-568](adr/568-verified-webhook-automation-starts.md).

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
