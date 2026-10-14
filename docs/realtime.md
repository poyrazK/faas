# Managed realtime

Gregale manages client connections and channel delivery. Your backend publishes
messages to a managed realtime endpoint; web and mobile clients subscribe through
WebSocket or HTTPS Server-Sent Events. This supports job progress, live dashboards,
chat events, and notifications without operating a separate connection server.

Configure the endpoint and authentication before distributing client credentials.
Keep account credentials on your backend. Scoped publisher credentials can limit
channels, event types, payload sizes, message TTL, and publishing rate. Worker
credentials authorize only their configured consumer group and operations.

Retained channels support recovery after reconnecting. Consumer groups add leased
claims, retry policies, dead-letter recovery, and worker health reporting. A worker
must settle a claim with its current fencing token; use that token to protect
customer-side writes from a stale worker.

See the [Node SDK](../sdk/node/README.md), [Python SDK](../sdk/python/README.md),
and [customer transaction and fencing examples](realtime-customer-examples.md).
The [realtime operations guide](ops/realtime.md) documents configuration,
preview gates, limits, recovery behavior, and rollout procedures.

## Principal access controls preview

When the operator enables the access-control preview, an endpoint can deny a
principal access to specific channels or revoke its existing sessions. OIDC
endpoints use the authenticated principal; subscription-ticket authentication
requires the ticket preview to be enabled as well. Access controls do not permit
clients to choose another principal or override their credential's permissions.

Access-control capacity is bounded. If the endpoint reaches its limit, review
existing deny entries before adding more. See the
[access-control operations guide](ops/realtime.md#principal-access-controls-preview)
for the request contract and recovery procedure.

## Function triggers

A realtime trigger runs one of your functions for each message on a channel, so
you do not operate always-on workers (ADR-1237). It consumes an existing consumer
group: create the group with `lease_seconds` of at least 120, then create a
trigger with `kind: realtime` and `{"endpoint_id", "channel", "group"}` config.
The group's ordering, filters, retry policy and dead-letter queue apply, and a
parked app wakes for the first message.

Each message is one invocation. A 2xx response completes it; a failed or
timed-out handler retries under the group's retry policy; a platform failure
returns the message without spending an attempt. While a trigger consumes a group,
direct claims return 409 `realtime_group_bound_to_trigger`; deleting the trigger
returns the group to your workers.

Declare it in `gregale.yaml` to create it on deploy, in a single-app manifest or
for one function of a [multi-function project](functions.md):

```yaml
triggers:
  - kind: realtime
    app: notify                   # the app slug, or a function name
    slug: orders
    config:
      endpoint_id: 6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11
      channel: orders
      group: billing
      max_concurrency: 4          # optional; defaults to 1
```

Deploy matches the app's realtime triggers to the file by `slug` and rolls the
change back if the deploy fails. See the
[function-trigger operations guide](ops/realtime.md#realtime-function-triggers).
