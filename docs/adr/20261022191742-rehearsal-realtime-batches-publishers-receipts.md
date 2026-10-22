# ADR-20261022191742 · Rehearsal batches, publisher publishes, receipt reads and entities

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** [ADR-20261021180000](20261021180000-rehearsal-realtime-capture.md), [ADR-20261022082721](20261022082721-rehearsal-captured-reads-inbox-async-invoke.md)
- **Decision:** The run-local platform API (`api.svc.gregale`) serves the remaining realtime and event calls a guest SDK makes:
  - It captures realtime batch publishes and publisher-credential publishes.
  - It answers a captured event's recipient reads.
  - It refuses durable entity calls with an explicit `501`.
- **Why:** Each of these was blocked inside a contained rehearsal. The Node publisher client and the event receipt helpers therefore failed for reasons unrelated to the change under test.

## Decision

### Batch publishes

`POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish-batch` is validated as apid and the history store validate it:

- 1 to 32 messages, each at most 4096 decoded bytes, 64 KiB in total;
- a trimmed batch ID of at most 128 bytes;
- TTLs from 0 to 86400 seconds, and `expected_sequence` only on the batch.

It is captured as one retained publish (kind `realtime_publish`):

| Part | Value |
|---|---|
| Message | `{delivery: retained, messages: [...], expected_sequence?}`, with each message's effective TTL |
| Idempotency key | The batch ID, so the same ID with other messages is `409`, as in production |

The answer is `200` with the batch ID, `durable: true` and one result per message, none with a sequence.

### Publisher-credential publishes

`POST /v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish` is validated as apid validates it:

- exactly one `Idempotency-Key`;
- a required event type that metadata must not contradict;
- at most 4096 decoded bytes and a TTL of at most 86400 seconds.

It is captured as a retained publish. Its message carries `publisher: true`, the event type, and the metadata that would be retained.

The request names no app, so the endpoint alone resolves its target in the run's account. The target is named the same way as for an app-key publish: a member's endpoint by workload and creation order, any other endpoint by app and ID. An unknown endpoint is `401`, as apid answers a credential for an endpoint it cannot match.

The answer is `200` with `sequence: "0"`. The field is required, and `0` is never a history position, because a captured publish is not retained.

The credential itself is not verified. Rehearsal calls carry no credential the run can check, as for every captured call.

### Event recipient reads

A captured event is accepted and never routed, so it has no recipients:

| Request | Answer |
|---|---|
| `GET /v1/events/receipt/replays`, `GET /v1/events/receipt/attempts` | Validated like apid (identity, `limit`, `after`), then the public `404 Event receipt not found` |
| `GET /v1/events/recipient-blocker`, `POST /v1/events/recipient-blockers` | `status: unavailable` with reason `recipient_unavailable` for a captured event, or `receipt_unavailable` for another. A different `expected_accepted_at` is `acceptance_replaced`. |
| `GET /v1/events/receipt` | Now honours `expected_accepted_at` (`409 event_acceptance_replaced`), `limit`, and refuses any `after` cursor, since no page follows a recipient-less receipt |

### Durable entities

`POST /v1/apps/{slug}/entities/invoke`, `POST .../entities/retry` and `GET .../entities/inspect` are refused with `501 not_implemented`, "Durable entities unavailable in a rehearsal".

An entity's value and version come from running its handler against production entity state. A capture would have to invent them, and performing the call would read or change production state.

## Consequences

- A batch over 64 KiB of base64 text marks the platform evidence incomplete, as any oversized capture does.
- `ScenarioRealtimeEndpointTarget` accepts an empty slug to resolve by endpoint alone. Only the publisher route passes one.
- No migration: the new captures reuse the `realtime_publish` kind.
