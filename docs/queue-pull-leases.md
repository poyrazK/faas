# Pull leases for worker queues

Worker and job apps never receive pushed messages: they have no HTTP listener
and are not gateway targets. They consume a queue by leasing its messages
(ADR-1152). Function apps keep using push bindings.

## Set up

Give the app a **pull** binding for the queue, either with
`gregale queue setup` (which picks pull for worker and job apps) or directly:

```bash
curl -X POST "$GREGALE_API/v1/apps/my-worker/queue-bindings" \
  -H "Authorization: Bearer $KEY" \
  -d '{"name":"jobs","queue_name":"jobs","mode":"pull","workload_class":"worker","max_concurrency":4}'
```

`max_concurrency` caps how many messages can be leased at once across all of
the app's workers. Pair the binding with a `queue_depth` scaling target to
scale the worker pool on the backlog.

## Consume

```bash
# Lease up to 10 messages, hidden from other consumers for 60 seconds.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"max_messages":10,"visibility_timeout_seconds":60}'

# Done: complete them.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases:ack" \
  -H "Authorization: Bearer $TOKEN" -d '{"receipts":["<receipt>"]}'

# Failed: retry under the binding's retry policy (or dead-letter when the
# attempt budget is spent). delay_seconds can push the retry later.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases:nack" \
  -H "Authorization: Bearer $TOKEN" -d '{"receipts":["<receipt>"],"error":"upstream timeout"}'

# Still working: keep the message hidden for another 5 minutes.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases:extend" \
  -H "Authorization: Bearer $TOKEN" -d '{"receipts":["<receipt>"],"visibility_timeout_seconds":300}'
```

An empty `messages` list means nothing is ready; poll again with back-off.
Each message is delivered at least once. A lease that is not acked, nacked or
extended before its visibility timeout returns to the queue and is redelivered
with a new attempt; the old receipt then reports `lease_expired`, so make
handlers idempotent. The ack, nack and extend calls report each receipt
separately in `succeeded` and `failed`.

## Authenticate

- **From inside the worker (recommended):** request a workload-identity token
  for audience `gregale:queues` from the guest's loopback token endpoint. No
  secret is stored in the image, and the platform records which instance
  holds each lease, so a worker that is mid-message is not the one stopped
  when the pool scales in.
- **From anywhere else:** an API key with the `queues:consume` scope
  (`deploy:write` and `admin` keys also work).

## Limits

A call leases at most 10 messages and takes at most 100 receipts. The default
visibility timeout is 30 seconds; the maximum is the plan's async invocation
deadline. Leasing works on production queues; stage environment queues use
their own delivery path.
