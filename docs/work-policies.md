# Application work policies

A work policy coordinates invocations that share an application key. For
example, edits to one document can run serially and replace older pending
indexing requests while edits to different documents run independently.

Declare a policy in `gregale.yaml`:

```yaml
work_policies:
  - name: document-index
    max_running_per_key: 1
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
  --work-policy document-index --work-key '"d1"'
```

The `work.key` API field and CLI `--work-key` accept a bounded JSON string,
number, or boolean. Types are distinct: `"1"` and `1` use different lanes.
An event subscription can derive the key from its CloudEvents payload:

```yaml
event_triggers:
  - source: documents
    type: document.edited
    work_policy: document-index
    work_key: data.document_id
```

The event selector is a dot path through JSON objects; the selected value
must be a scalar. A missing or non-scalar key prevents that delivery from
being enqueued and surfaces as an event fanout error. Explicit async
invocations and event subscriptions using the same app, policy name, and
typed key share a lane.

`max_running_per_key` currently supports `1`. `pending_updates` can be `all`
(the default, retaining FIFO pending work) or `keep_latest` (superseding
older **pending** work in the same lane). `debounce_ms` delays eligibility
from admission; `expires_after_ms` expires work that has not begun before its
deadline. A policy update increments its revision for new work; existing
invocations retain their admitted settings. A policy bound to an event
subscription cannot be deleted until the binding is removed.

Dispatch and ledger updates are fenced by claim attempt. A worker that loses
ownership can still have contacted an external service. Protect external
side effects with an application idempotency key, version predicate, or
external fencing mechanism. Delivery remains at least once.

This release applies keyed policies to explicit async invocations and
internal event subscriptions. Queue and broker producers, delayed tasks,
independent app tasks, and tenant fairness caps still use their existing
execution behavior.
