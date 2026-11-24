# Build stored workflows in Gregale

A stored workflow runs steps against an app, with a manual, scheduled, or event start.
The app must have a live default deployment. Stored workflows use the existing durable
workflow executor, with retries, dependencies, waits, and run history.

Create and manage definitions through the stored workflow authoring API or generated
SDKs. Save a draft with a trigger and steps, including input mapping, event filters,
waits, and failure handlers as needed. Validate reports errors without executing
or saving steps; publishing makes the saved draft available to future runs.
The app dashboard also offers starter forms and an advanced JSON editor, described below.

## Start from the app dashboard

Open **Build and manage stored workflows** in the app's Workflows section, or visit
`/dashboard/apps/APP_SLUG/workflows/stored`. Choose one of the same four starters
available through `gregale workflow stored init`. Selection does not save a draft
or start a run. Deploy the starter's handler routes and configure its required
integrations or event sources separately.

1. Choose a unique stored workflow name. Scheduled reports offer form fields for cron,
   timezone, collect/deliver paths, overlap and missed-start recovery. Other
   definitions use JSON; keep its name matched to the stored workflow name. Validate
   the configured definition before saving.
2. Review the sample input and mock outcomes, then simulate. The trace shows step
   decisions without calling handlers or connected services.
3. Save the draft, then choose **Save test data** to retain the sample, mock
   outcomes, and named test cases. Reopening the stored workflow restores that saved
   data, including for custom names. Draft saves alone keep test edits in the editor.
4. Review the scenario JSON and choose **Check and publish**. The server checks
   the saved draft and issues the receipt required by the app's publishing policy.
   Unsaved changes, stale revisions, failing scenarios or missing required
   coverage stop publication. Explicitly choose manifest takeover only when
   API-managed publication should replace the deployed definition's ownership.
5. After publication, confirm real side effects and choose **Run published
   stored workflow**. Only the sample's `input` is submitted. Mock results never
   control real execution. Repeating the same form submission reuses its admitted
   run through the existing idempotency mechanism while the displayed draft
   revision remains current. If it changed, inspect run history before reloading
   and starting another run.

The page shows recent health, queue observations, run states, pinned deployments,
and step attempts. Run input/output bodies and raw errors remain behind explicit
API/CLI inspection. Run links are scoped to the selected app and stored workflow.
A failed history read is reported as unavailable instead of an empty history.

Scheduled starters remain disabled after publication until **Enable automatic
starts** is selected. Manual runs remain distinct from that setting. The console
cannot enable a disabled workflow runtime or satisfy tenant admission
requirements. The existing permissions, publication policies and execution
gates still apply.

The `stored-workflows` capability controls access to this authoring surface. Turning
it off does not pause published schedules, accepted events, or existing runs.
Pause automatic starts and drain or cancel runs before access is removed. The
separate `workflows` capability holds all workflow execution for the account,
including workflows declared in a deployment manifest.

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

### Configure a verified webhook stored workflow

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
This replaces ordinary app delivery or the displayed stored workflow binding for future
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
enabled stored workflow can perform real actions immediately on matching deliveries.
Pausing the stored workflow makes future verified events receive ignored receipts.
**Remove webhook binding** has its own confirmation and revision check, restores
ordinary delivery for future events, and does not cancel accepted work. Bindings
target a published stored workflow name, so later publications affect future events;
already accepted definitions and filters remain captured.

Enter a **Provider event ID** to inspect acceptance, ignored reason and routing
status. A resulting run link appears only after verifying its app and stored workflow
identity. Receipts from another stored workflow are not shown. Read failures are reported
as unavailable or not found, rather than as successful acceptance. Provider retries
reuse the original receipt and accepted work; receipt lookup does not retain retry
counts or delivery history. Real payloads and signing material stay out of this view.
Native execution qualification remains separate from this dashboard acceptance.

## Start from a template

Create a local starter without logging in:

```sh
gregale workflow stored init --list
gregale workflow stored init --template scheduled-report --path daily-report
```

The destination must be a new directory with an existing parent. The command
refuses existing directories and creates private files: `stored_workflow.yaml`,
`sample-input.json`, `mock-outputs.json`, `mock-attempts.json`, `scenarios.yaml`,
and `README.md`.
It does not save or publish a stored workflow.

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
gregale workflow stored validate --app APP_SLUG --file stored_workflow.yaml
gregale workflow stored simulate --app APP_SLUG --file stored_workflow.yaml \
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

## Author stored workflows with the CLI

The CLI accepts the same definition as YAML or JSON. Validate it before saving a
draft, then pass the version returned by each write to the next command:

```sh
gregale workflow stored list --app billing
gregale workflow stored get --app billing --name paid-invoice
gregale workflow stored health --app billing --name paid-invoice
gregale workflow stored pause --app billing --name paid-invoice --expected-version VERSION
gregale workflow stored resume --app billing --name paid-invoice --expected-version VERSION
gregale workflow stored get --app billing --name paid-invoice --definition-out paid-invoice.json
gregale workflow stored get --app billing --name paid-invoice --definition-out published.json --published
gregale workflow stored revisions list --app billing --name paid-invoice
gregale workflow stored revisions show --app billing --name paid-invoice --revision 42
gregale workflow stored validate --app billing --file paid-invoice.yaml
gregale workflow stored apply --app billing --file paid-invoice.yaml --expected-version 0
gregale workflow stored publish --app billing --name paid-invoice --expected-version VERSION
gregale workflow stored restore --app billing --name paid-invoice --revision 42 --expected-version VERSION
gregale workflow stored delete --app billing --name paid-invoice --expected-version VERSION --yes
```

`VERSION` is the `version` returned by the previous write (or shown by
`get`/`list`). Versions come from one counter shared by every stored workflow in
the app, and are never reused after a delete, so a new draft saved with
`--expected-version 0` usually returns a version greater than 1. Publish with
the version that `apply` returned, not `1`.

List and get show the current version, publication version, ownership, and enabled
state without returning definition contents. Definition export is explicit: use
`--definition-out` to write the draft as JSON to a new file, or combine it with
`--published` to export the published definition. Exported files use mode `0600`,
and the command will not overwrite an existing path. Machine-readable get/list
output remains metadata-only.

Use `--expected-version 0` only when creating a new draft. For an existing
stored workflow, use its current version from the most recent response or API read. A
version conflict means another writer changed the draft; reload it before
retrying. Publishing is a separate step. If the name is currently owned by the
app's YAML manifest, pass `--take-over-manifest` only when you intend the saved
stored workflow to take ownership.

Each revision is identified by the version at which it was published. Listings
report it as `revision` (and, for compatibility, `version`); pass it to
`revisions show --revision` and `restore --revision`. Revision listing defaults to 50 entries and accepts `--limit` (1..100) and
`--offset` for paging. Revision details show the timestamp, definition hash, and
whether the snapshot was migrated from legacy publication history. Definitions
stay out of normal terminal and JSON output; `revisions show --definition-out`
exports one to a new mode-`0600` JSON file. `workflow stored restore` requires the
current `--expected-version` and saves the selected snapshot as a new draft. Use
`0` if the stored workflow was deleted. The restore command does not publish; validate
and publish the new draft separately.

Delete a stored workflow with its current version and explicit `--yes` confirmation.
If the published stored workflow took over a YAML-owned name, include
`--restore-manifest` to allow the current YAML definition to own that name again:

```sh
gregale workflow stored delete --app billing --name paid-invoice --expected-version 48 --yes --restore-manifest
```

Pause automatic starts to stop future scheduled/event admissions. Existing runs
and already accepted events continue. Republishing preserves pause. Resume to
start new automatic admissions again; schedules skip missed minutes and re-arm
at the next evaluation. Manual sample runs remain possible while paused and
execute the published definition with real app side effects.

Use `workflow stored pause` and `workflow stored resume` for a published stored workflow.
Both require the current `--expected-version`; a successful change advances the
stored workflow version but does not publish a definition or add a revision. Read the
stored workflow again before retrying after a version conflict.

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
action.

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
by Gregale.

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

## External service actions

An `outbound` step calls an existing managed integration bound to the stored workflow's
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

## Start from a Stripe webhook

Create a signed Stripe inbound endpoint and publish a stored workflow on
its app. Bind the endpoint to that published name:

```http
PUT /v1/apps/billing/inbound-webhooks/ENDPOINT_ID/workflow-binding
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
to a manual, scheduled or event stored workflow. The original definition is preserved
in the accepted run. No frontend configuration changes are included.

Gregale verifies the exact Stripe body before storing a receipt and durable
workflow start intent. The workflow input is a CloudEvents envelope whose
`data` is the complete Stripe event. For example, the Stripe object ID maps with
`{{input.data.data.object.id}}`. The source is
`gregale.inbound.stripe.ENDPOINT_UUID`, the type is Stripe's event type, and
`id` is its provider event ID. The event publishing API reserves this source;
customers cannot use it to forge verified ingress. Signing secrets are never
included in the input.

The complete workflow input envelope, including the provider body and
server-added metadata, must fit within 1 MiB. A matching webhook that exceeds
this limit receives HTTP 413 with code `event_envelope_too_large`; reduce the
provider payload and retry. The check conservatively accounts for exponent-form
JSON numbers expanding when stored. Paused targets and nonmatching event types
can still be recorded as ignored without creating a workflow input.

Provider retries return the original receipt and do not start another run.
Different content for the same endpoint/event ID conflicts. Retries still use
the original receipt after binding removal; retained events accepted through
ordinary app delivery before binding keep that app delivery. Deduplication
lasts for the plan's [settled-receipt window](events.md#settled-receipt-retention). Side effects
inside steps still require the normal action idempotency contract.

Paused/unpublished targets and nonmatching event types receive an `ignored`
receipt. Content filters are evaluated before a run starts: a mismatch reports
`routing_status: filtered` and starts no run. Pausing or editing a definition
affects future events; already accepted definitions and filters stay captured.
When the workflows capability is disabled, captured workflow recipients wait
without consuming routing attempts. A captured definition that exceeds a
downgraded plan's limits also waits for an eligible plan. Delivery age limits
and receipt retention still apply while recipients wait.
The workflow runtime, account/app eligibility and live deployment must be
available for new acceptance. Integration bindings remain validated. A
runtime/configuration outage returns 503 without accepting new work.

Inspect `GET /v1/apps/billing/inbound-webhooks/ENDPOINT_ID/workflow-receipts/STRIPE_EVENT_ID`
for the receipt, ignored reason, routing status and admitted `run_id`. Use the
existing workflow run/step APIs for execution history. Quota pressure uses the
existing bounded event fanout retry and failure replay APIs; look up failures
with the receipt's `event_source` and `provider_event_id`.

## Start from a generic signed webhook

Use a generic inbound endpoint when the sender is not Stripe. Create it with
`provider: "generic"` and a random secret containing at least 32 bytes, then
bind it to a published stored workflow using the same workflow-binding API above.
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
[settled-receipt window](events.md#settled-receipt-retention).

An accepted stored workflow run receives a CloudEvents envelope with source
`gregale.inbound.generic.ENDPOINT_ID`, the signed event ID and type, and the
complete submitted JSON as `data`. The durable receipt and existing event
fanout handle retries and filters. Without a stored workflow binding, Gregale
delivers the verified JSON to the configured app path and includes the verified
event metadata in `x-gregale-webhook-*` request headers.

Apply migration `20261006062359228_generic_inbound_webhook.sql` before creating
generic endpoints. The down migration is forward-only; remove generic endpoints
before rolling back application binaries.

## Start on an event

An `event` trigger starts a run for each matching event. `source` and
`event_type` accept a `*` wildcard at either edge, and `filter` matches the
event body:

```json
{ "type": "event", "source": "gregale.storage", "event_type": "object.created" }
```

Sources under `gregale.` are platform events, such as bucket notifications,
and work on every plan that allows workflows. Any other source, including a
wildcard such as `*`, matches your own custom events, which need custom events
enabled for your account. Without them, publishing or enabling the stored
workflow is refused with `stored_workflow_events_unavailable` instead of
accepting a workflow that would never start. Use a `gregale.` source, a
schedule, a manual start or an inbound webhook binding instead.

## Preview scheduled fire times

Inspect a deployed schedule before relying on its cadence or catch-up behavior:

```sh
gregale workflow schedule simulate --app billing --workflow nightly
gregale --json workflows schedules preview --app billing --workflow nightly --count 8
gregale workflow schedule simulate --app billing --workflow nightly \
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
Node and Python SDKs expose the same two operations.

## Review and restore published revisions

Every successful publish appends an immutable revision containing the definition,
publication version, recording time, account, and API key when key authentication
was used. Saving drafts and pausing or resuming automatic starts do not create
history entries. The response includes `definition_hash`, the SHA-256 hash of the
canonical JSON encoding of that revision's definition.

List revisions newest first, with up to 100 entries per page:

```http
GET /v1/apps/billing/workflows/stored/paid-invoice/revisions?limit=50&offset=0
```

Read one immutable definition with
`GET /v1/apps/{slug}/workflows/stored/{name}/revisions/{version}`. Restore it as a new
draft using the current stored workflow version from the latest read:

```http
POST /v1/apps/billing/workflows/stored/paid-invoice/revisions/42/restore
Idempotency-Key: restore-paid-invoice-42
Content-Type: application/json

{"expected_version": 47}
```

Restoring does not publish the definition or alter runs and accepted events.
Validate and publish the restored draft separately; a YAML-owned name still
requires explicit takeover at publish time. If the stored workflow was deleted, use
`expected_version: 0` to restore its draft. Draft updates remain protected by
optimistic version checks.

When history is introduced, Gregale seeds the latest available publication for
each existing API-owned stored workflow as a `legacy_snapshot`. Its `recorded_at` is
the migration time, and earlier definitions and the original publication time
cannot be recovered. Future publishes are recorded individually. Go, Node, and
Python clients expose list, get, and restore methods. No dashboard editor is
included. Export the history before rolling this migration back; its down
migration removes the retained revisions.

## Next steps

- [Test stored workflows](stored-workflow-testing.md): test cases, verification suites, scenarios, simulation and publishing checks.
- [Run and monitor stored workflows](stored-workflow-operations.md): find and cancel runs, health, and alerts.
- [Recover stored workflow runs](stored-workflow-recovery.md): resume failed runs, recover missed and paused work, and recovery policies.

## Moved sections

Testing, monitoring and recovery now have their own pages:
[Test stored workflows](stored-workflow-testing.md),
[Run and monitor stored workflows](stored-workflow-operations.md) and
[Recover stored workflow runs](stored-workflow-recovery.md). The two
headings below stay because errors from earlier CLI and API releases link to
them.

### Promote and use a named baseline

Moved to [Test stored workflows](stored-workflow-testing.md#promote-and-use-a-named-baseline).

### Retire an unused baseline

Moved to [Test stored workflows](stored-workflow-testing.md#retire-an-unused-baseline).
