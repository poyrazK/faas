# Application work policies

A work policy coordinates invocations that share an application key. For
example, edits to one document can run serially and replace older pending
indexing requests while edits to different documents run independently.

Declare a policy in `gregale.yaml`:

```yaml
work_policies:
  - name: document-index
    max_running_per_key: 1
    max_running_per_fairness_key: 2
    pending_updates: keep_latest
    debounce_ms: 3000
    expires_after_ms: 600000
```

`gregale.toml` supports the same fields under `[[work_policies]]`. Policies
are scoped to one app. In a multi-app manifest, add `app: my-app` to each
declaration. The API also offers `GET /v1/apps/{slug}/work-policies` and
`PUT` or `DELETE /v1/apps/{slug}/work-policies/{name}`. An app can have up to
64 named policies.

Use the policy with an async invocation:

```bash
gregale invoke my-app --async --payload '{"document_id":"d1"}' \
  --work-policy document-index --work-key '"d1"' \
  --work-fairness-key '"tenant-42"'
```

The `work.key` API field and CLI `--work-key` accept a bounded JSON string,
number, or boolean. Types are distinct: `"1"` and `1` use different lanes.
Queue sends and application inbox messages accept the same `work` field. Use
`gregale queue send` or `gregale send` with `--work-policy`, `--work-key`, and
optional `--work-fairness-key` to coordinate those messages with other work in
the same lane. Unnamed messages use the keyed invocation dispatcher and are
delivered individually. Named messages require an enabled push queue consumer;
the queue poller shares lane and fairness reservations with the dispatcher
while preserving the consumer's batch and retry behavior.

`POST /v1/apps/{slug}/delayed-tasks` also accepts `work` with the same
`policy`, `key`, and optional `fairness_key` fields. Its scheduled time remains
the earliest dispatch time; a pending expiry can occur before that time if
the policy TTL is shorter. Policy-tagged delayed tasks use the keyed
invocation dispatcher even when the app has a delayed-task queue trigger;
they are delivered individually rather than in a trigger batch.
An event subscription can derive the key from its CloudEvents payload:

```yaml
event_triggers:
  - source: documents
    type: document.edited
    work_policy: document-index
    work_key: data.document_id
    work_fairness_key: data.tenant_id
```

A matching completion event can cancel pending work in that lane without
invoking the application handler:

```yaml
event_triggers:
  - source: orders
    type: order.completed
    work_policy: reminders
    work_key: data.order_id
    work_action: cancel_pending
```

An event captures its subscription work selector, action, and effective
policy settings when it is published. Updating or deleting the binding or
policy later affects new events; accepted events keep their original routing
and policy revision. Receipts accepted before policy snapshots use the live
policy for compatibility.

The same operation is available at
`POST /v1/apps/{slug}/work-policies/{name}/cancel-pending` with a JSON `key`.
It returns the number of pending rows cancelled. A replay of the same event
uses its original cancellation receipt, so it cannot cancel work admitted
afterward. For API callers, reuse an `Idempotency-Key` when retrying an
uncertain response. Cancellation is a point-in-time operation: an older
producer retry may enqueue work later. Use an application version check or
watermark when that race matters.

The event selector is a dot path through JSON objects; the selected value
must be a scalar. A missing or non-scalar key prevents that delivery from
being enqueued and surfaces as an event fanout error. Explicit async
invocations and event subscriptions using the same app, policy name, and
typed key share a lane.

`max_running_per_key` currently supports `1`. A
`max_running_per_fairness_key` of 1 to 1000 limits running work across
different work keys in the same fairness group. For example, distinct
documents can run concurrently while one tenant has at most two active
documents. If `fairness_key` is omitted, it defaults to `key`, which leaves
different work keys in separate groups. A saturated group is skipped by the
due-work scan, allowing other groups to reach the dispatcher.

`pending_updates` can be `all`
(the default, retaining FIFO pending work) or `keep_latest` (superseding
older **pending** work in the same lane). `debounce_ms` delays eligibility
from admission; `expires_after_ms` expires work that has not begun before its
deadline. A policy update increments its revision for new work; existing
invocations retain their admitted settings. A policy bound to an event
subscription or broker trigger cannot be deleted until the binding is removed.

External broker triggers can use the same policy and lane. In a source-ref
`gregale.yaml` deployment, bind the trigger to scalar fields in its JSON
message payload:

```yaml
triggers:
  - kind: kafka
    app: my-app
    slug: document-edits
    config:
      brokers: [broker.example:9092]
      topic: document-edits
      group: indexers
    work_policy: document-index
    work_key: document_id
    work_fairness_key: tenant_id
```

For an API-created trigger, create it with `enabled: false`, then PUT
`/v1/triggers/{id}/work-binding` with
`{"policy_name":"document-index","key":"document_id","fairness_key":"tenant_id"}`,
then resume it. GET and DELETE on that binding path inspect or remove it.
The initial binding requires a disabled trigger with no record receipts,
because older receipts may use delivery handles that change on redelivery.
An existing binding may be updated; already admitted records keep their
captured policy revision and lane. The batch-create trigger endpoint expects
the named policy to exist before it applies a trigger declaration.

Kafka uses topic, partition, and offset as a durable record identity; SQS
uses `MessageId`; NATS and Redis Streams use stream sequence or entry ID.
AMQP publishers must provide a unique `message_id`. A delivery without a
stable identity or scalar work key goes to the trigger dead-letter ledger.
Gregale acknowledges a terminal broker redelivery without invoking the app
again. Superseding or cancelling a **pending** broker record leaves its
running predecessor untouched.

Dispatch and ledger updates are fenced by claim attempt. A worker that loses
ownership can still have contacted an external service. Protect external
side effects with an application idempotency key, version predicate, or
external fencing mechanism. Delivery remains at least once.

Keyed policies apply to explicit async invocations, delayed tasks, internal
event subscriptions, queue or inbox sends, and external broker triggers.
Deployment-attached app tasks have a separate command lifecycle and do not
yet join this shared work ledger.
