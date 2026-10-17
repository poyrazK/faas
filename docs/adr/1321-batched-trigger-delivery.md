# ADR-1321: Batched trigger delivery

- **Status:** accepted (owner direction, 2026-10-15: build several messages per invocation as the next realtime item).
- **Date:** 2026-10-15
- **Decision:** A trigger may opt into `delivery: batch`. The gateway then invokes the function once per dispatch batch with every claimed record, instead of once per record. The function reports partial failure with the `batchItemFailures` response ADR-100 already defines. The default stays `delivery: record`, so no existing trigger changes behaviour.
- **Why:** Busy channels and broker topics pay one invocation, one admission and often one wake per message. Customers buy compute (plan RAM + 8 MB per running second); handling a batch in one invocation costs them less for the same work and cuts wake load on the fleet. ADR-100 intended batch delivery ("stolen verbatim from AWS Lambda") but the gateway shipped per-record invocation.

## Context

- schedd already claims up to `batch_size_max` records per dispatch, bounded to 6 MB, and posts them in one `POST /v1/invocations:dispatch_batch` envelope (ADR-100). Realtime trigger lanes (ADR-1322) do the same per lane.
- The gateway walks the envelope and invokes the function once per record, sequentially, each with its own 30 s (or app request-budget) timeout, then reads `batchItemFailures` from each single-record response.
- The in-platform `queue` kind gives every record a durable invocation identity (ID, attempt, replay generation) that the invocation ledger tracks one-to-one. Those records cannot share one invocation.

## Decision

### Configuration

- New trigger field `delivery`: `record` (default) or `batch`. Stored in `triggers.delivery` with a CHECK constraint.
- Allowed for `kafka`, `nats`, `redis_streams`, `sqs_compat` and `realtime`. Rejected for `cron` (no records) and `queue` (durable per-record invocations), with a 422 `trigger_invalid_config` problem naming the kind.
- `batch_size_max` keeps its meaning: it is the most records one invocation receives. Existing batch limits and the 6 MB envelope cap apply unchanged. Realtime triggers, which ADR-1322 limited to one message, may set it up to the consumer group's claim limit (16) with batch delivery; it defaults to 10.
- A trigger created with batch delivery is created disabled, switched, then enabled, so no record is ever delivered in the per-record shape.
- API (`POST`/`PATCH /v1/triggers`), CLI (`gregale triggers add --delivery batch`), and `gregale.yaml` triggers accept the field.

### Invocation

The gateway sends one `POST /_triggers/{source}/{trigger_id}` with `Content-Type: application/json`, header `X-Gregale-Batch-Size: N`, and body:

```json
{"records": [
  {"item_identifier": "…", "data": {…}, "data_base64": "…", "headers": {…}, "metadata": {…}}
]}
```

`data` carries a payload that is valid JSON as-is; any other payload arrives as `data_base64` instead, so the body stays close to the records' own size. Order follows the claim order (for realtime, the group's sequence order). A record whose payload cannot be decoded is dead-lettered and left out of the invocation.

### Results

- Error, timeout or a non-2xx status: every record is retried with the same code as today (`invoke_error`, `invoke_timeout`, `function_state_*`).
- 2xx: records listed in `batchItemFailures` are retried with `function_failed`; all others succeed. Unknown identifiers are ignored. A malformed `batchItemFailures` retries the whole batch (`response_malformed`).
- One invocation span (`gregale.invocation`, attribute `gregale.batch.size`) covers the batch.

### Timeouts

One invocation gets the same per-invocation limit a single record gets today (30 s, or the app's request budget per ADR-1180). Customers choose `batch_size_max` so a batch finishes inside it.

## Rejected alternatives

- **Make batch the default.** Changes the handler contract for every existing trigger.
- **Batch the `queue` kind.** Breaks the one-invocation-per-record ledger and its replay semantics; it needs its own design.
- **Parallel per-record invocations.** Lowers latency but keeps one invocation and one admission per record, so it saves the customer nothing.

## Consequences

- A function with `delivery: batch` must accept the batch envelope and report partial failures by item identifier. The Node (`@gregale/sdk-node/triggers/runtime`) and Python (`faas_sdk.trigger_batch`) SDKs parse the envelope, run a handler per record and build `batchItemFailures`; the shape stays plain JSON for other languages.
- Fewer, longer invocations for busy triggers; per-record retry and dead-letter accounting is unchanged.

## Rollout

apid admits `delivery: batch` only with
`FAAS_TRIGGER_BATCH_DELIVERY_PREVIEW_ENABLED=1`; off, the default, create,
update and manifest deploys that ask for it get 422, and every trigger keeps
record delivery. Turning the flag off again stops new batch triggers but leaves
existing ones delivering batches; switch them to `delivery: record` first. The
flag stays off until batch delivery has been tested end to end.
