# ADR-1322: Realtime channels as function triggers

- **Status:** proposed (owner decision, 2026-10-13: build it with the recommended defaults)
- **Date:** 2026-10-13
- **Decision:** Add a `realtime` trigger kind. It binds an app to one managed realtime consumer group (ADR-1030), and schedd delivers each message on that group's channel as one invocation of the app. The group stays the source of truth for ordering, filters, retries, dead letters and replay.
- **Why:** Consumer groups make customers run always-on workers that claim and settle messages. Gregale is both the realtime layer and the function runtime, so it can run that worker itself and let the function scale to zero. Competitors route channel messages to an external function service; here it is one platform and one bill.

## Context

The trigger primitive (ADR-100) already has what a delivery loop needs: a dispatch tick in schedd, per-kind pollers, a per-record ledger (`trigger_records`), retries, audit kinds, `schedd_esm_*` metrics, and concurrent push lanes for the in-platform queue (ADR-231). The `queue` poller already reads Postgres through schedd's own pool rather than an external broker.

The realtime release added consumer groups with leases, fencing tokens, per-key ordering, metadata filters, deduplication, retry policies, circuit breakers, dead-letter retention, export, replay jobs and an inspector. Neither side is complete alone: triggers have no realtime source, and groups have no managed consumer.

Two facts constrain the design:

- `POST /v1/invocations:dispatch_batch` invokes a batch's records **one at a time**, each as its own invocation with a 30 s limit (`GatewaySynthDispatchTimeout`) and a 5 min batch limit. Every trigger kind today delivers one record per invocation.
- The default group lease is 30 s, shorter than one wake plus a 30 s invocation.

## Decision

### Resource

A trigger with `kind: realtime` and `config: {endpoint_id, channel, group, max_concurrency}`. The group must already exist (customers configure ordering, filters, retry policy and retention on it as today). Validation at create time:

- The endpoint belongs to the trigger's account. It may belong to another app in that account, so a multi-function project (ADR-985) can consume a channel from its own endpoint.
- The group's `lease_seconds` is at least `RealtimeTriggerMinLeaseSeconds` (120 s: one 30 s invocation plus wake and queueing margin).
- The group is not already bound. One group binds to at most one trigger.
- `batch_window_ms`, `filter_criteria`, `max_attempts`, `retry_policy` and `broker_poison_strategy` are rejected for this kind. The group's own settings apply, so a customer cannot configure two conflicting retry budgets.

Deleting the trigger unbinds the group and leaves its messages in place.

### Delivery

One message per invocation, the same as every other trigger kind. `batch_size_max` defaults to 1 and only sizes the claim. Invoking several messages in one call would change the gateway contract for every trigger kind, so it is a separate follow-up decision.

The function receives one JSON record:

```json
{"endpoint_id": "…", "channel": "orders", "group": "billing", "sequence": "42",
 "message_id": "…", "event": "created", "version": 1, "deleted": false,
 "data_base64": "…", "binary": false, "metadata": {"event_type": "order.created"},
 "created_at": "…", "attempt": 1, "fencing_token": "…"}
```

Outcomes map onto group settlement through the gateway's stable per-record codes:

| Dispatch result | Group settlement |
|---|---|
| succeeded | `complete` |
| `function_failed`, `function_state_<state>`, `invoke_timeout`, `response_malformed` | `retry` with that code as `failure_code` |
| `invoke_error`, `batch_timeout`, rate-limited, transport or claim failure | `release`: the handler never ran, so the attempt is returned |

Because failure codes are stable, a group retry policy's permanent codes (for example `function_failed`) dead-letter on the first failure. A handler returns an empty or JSON body, as for every trigger kind; a non-JSON body counts as `response_malformed`.

### Concurrency

Realtime triggers reuse the queue push-lane model. Each trigger runs up to `min(config.max_concurrency, plan MaxConcurrency, QueuePushDispatchSlotsPerNode)` lanes, and each lane carries one message. The lane allowance starts at one, grows after each lane that reaches the gateway, and halves after a gateway failure. `config.max_concurrency` defaults to 1.

Per-key ordering needs no new code. The group never leases a message while an earlier message with the same ordering key is leased or pending, so lanes serialize per key and run in parallel across keys.

### Retries and dead letters

The group is authoritative. Its attempts, retry policy, circuit breaker and dead-letter queue govern delivery. Each claim is one delivery with its own ledger item (`<sequence>:<fencing token>`), so the trigger's retry, backoff and dead-letter machinery never acts on it. A successful delivery's ledger record ends `succeeded`; any other ends `superseded` when it is handed back to the group, and no `trigger_dead_letter` row is written. Customers therefore see one dead-letter queue, the group's, with its existing inspector, export and replay jobs.

### Binding exclusivity

While a group is bound, claim and settle requests from worker credentials and the account API return 409 `realtime_group_bound_to_trigger`. Status, metrics, dead letters, replay, export, pause, drain and resume stay available. Pause and drain stop dispatch exactly as they stop workers.

### Plans, preview and billing

- The kind requires the retained-realtime preview (`FAAS_REALTIME_RETAINED_PREVIEW_ENABLED`) and is available to Hobby, Pro and Scale, like the in-platform `queue` kind. It counts against the existing per-app trigger caps.
- Invocations bill like any other: plan RAM + 8 MB per running second (§4.7). There is no per-message fee, so the financial model is unchanged. Metering realtime usage itself remains a separate gap.
- A parked app wakes through the normal path. The first message after idle pays the restore or cold-boot cost. Publishing on a bound channel notifies `trigger_ready` from a database trigger, so dispatch starts at commit rather than on the next 1 s tick.

### Ownership

apid writes the trigger and the binding (customer intent). schedd claims and settles group messages through the state store, the same way the `queue` poller moves `invocations` rows. No daemon is added, and apid does not invoke functions.

### Storage and limits

- Widen the `triggers.kind` CHECK to admit `realtime`.
- Add `managed_realtime_consumer_groups.trigger_id uuid UNIQUE REFERENCES triggers(id) ON DELETE SET NULL`.
- Add `RealtimeTriggerMinLeaseSeconds = 120` to `pkg/api/limits.go`.
- A realtime trigger is created disabled, bound, then enabled, so no dispatcher polls a group the trigger cannot consume. A failed binding deletes the trigger again.
- Realtime lanes share the push-queue dispatch pool (`QueuePushDispatchSlotsPerNode`).

### Observability

`schedd_esm_*` metrics with `source="realtime"`. The poller implements `BrokerStats` from the group backlog (unassigned plus ready), so a `queue_lag` scaling target scales the app out ahead of a realtime backlog. The realtime inspector shows the bound trigger on the group.

## Rejected alternatives

- **Multi-message invocations now.** Cheaper per message, but it changes the gateway and handler contract for every trigger kind. Separate decision.
- **Hidden auto-created groups.** One less step for customers, but ordering, filters, retries and retention would need a second configuration surface on the trigger. Binding an explicit group reuses all of it.
- **realtimed pushes to functions.** realtimed would need invocation, retry and wake logic that schedd already has, and messages published while no node holds the channel would be missed.
- **Trigger-owned retries.** Two attempt budgets and two dead-letter queues for one message.

## Rollout

Four small PRs, each behind the retained-realtime preview:

1. Migration, limit, `api` kind and apid validation, binding, and the 409 on bound groups.
2. schedd `realtime` poller, push lanes, settlement mapping and Postgres-backed tests.
3. OpenAPI, Go/Node/Python SDK methods, manifest (`gregale.yaml` single-function and `functions:` projects) and CLI.
4. Inspector binding view, metrics and `docs/ops/realtime.md`.

## Consequences

- Customers get serverless consumers for realtime channels, with ordering, retries and replay they already know from consumer groups.
- Throughput per trigger is bounded by the plan's concurrency and one message per invocation. High-volume channels may need multi-message invocations later.
- A long-running handler must finish within the 30 s invocation limit, as for every other trigger.
- Realtime adds one ledger row per delivery, as other kinds add one per broker record. schedd reaps the ledger only with `FAAS_TRIGGER_LEDGER_RETENTION_ENABLED=1`: terminal rows past their explicit `result_retention_until`, or past the default ages (`TriggerRecordRetention` 7 days, `TriggerDeadLetterRetention` 30 days). Off, the default, ledger rows are kept as before.
