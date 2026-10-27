# ADR: The events flag pauses publishing and routing

Date: 2026-10-27
Status: Accepted
Amends: [ADR 20261023115741](20261023115741-every-capability-behind-a-platform-flag.md) (what the `events` capability's flag stops)
Related: [ADR-1700](1700-platform-flags.md) (platform flags), [ADR 20261025211616](20261025211616-event-routing-company-boundary.md)

## Context

A GA blocker from the 2026-10-25 event audit: switching the `events` flag off
is not a rollback.

- The flag only hides apid routes. No daemon reads it: schedd keeps routing
  every accepted receipt, and the deliveries it creates are invocations gated
  by `async-invocations`, for the subscriber's account.
- Several paths publish without it: in-guest publishing through vmmd,
  `POST /v1/platform-tenant-self/apps/{slug}/events:publish` (owned by
  `platform-tenants`), and `triggers:batch_create`, which creates event
  subscriptions without the `event-subscriptions` setting check a source
  deploy applies.
- A hidden publish route answers 404, which a producer may read as permanent
  and drop the events it holds.
- `GET /v1/events` is the dashboard and CLI live-update stream (ADR-011), not
  published events, but the catalog's `/v1/events` subtree made the events
  kill switch cut it.

## Decision

1. **Publishing is refused with 503 while events is off**, platform-wide or
   for the account. `POST /v1/events:publish`, `:publish-batch` and
   `/v1/apps/{slug}/events:publish` answer `503 events_paused` with
   `Retry-After: 30` instead of a missing route. The platform-tenant route
   and in-guest publishing refuse the same way; the guest proxy already turns
   any vmmd refusal into a retryable `503 event_publish_unavailable`. Nothing
   is accepted, so a producer resends the same events later. Other events
   routes keep the ADR 20261023115741 behaviour (hidden).
2. **Routing is held, nothing is lost.**
   - Platform-wide off (the flag's kill switch): schedd claims no receipts or
     recipients and runs no recoveries or backfills. Settled receipts keep
     pruning (their routing failures still reach the dead-letter queue) and
     routing health is still observed.
   - Off for an account: each recipient of a customer event whose app's
     account has events off waits one minute at a time without spending a
     routing attempt (progress `delivery_control_reason: events_paused`,
     waiting reason `retry_backoff`).
   - Delivery-age limits keep counting while routing is held, as for a paused
     subscription.
3. **Server-side publishers keep writing.** Object-storage notifications
   (`gregale.storage`), inbound webhook automation (`gregale.inbound.*`),
   commit relay (`gregale.commit.*`) and platform events (`gregale.api`) are
   written inside other capabilities' transactions; refusing them would fail
   uploads and webhook deliveries. They are accepted and wait for routing.
   Their routing stops only with the platform-wide switch: per account, those
   capabilities' own flags govern them.
4. **Deliveries already admitted finish.** An invocation created before the
   switch keeps its normal lifecycle (and its own capability flag).
5. **`triggers:batch_create` checks `manifest.event_triggers`** like a source
   deploy, so it cannot create event subscriptions for an account without
   them.
6. **The live-update stream is the dashboard's.** The catalog gives the exact
   path `/v1/events` to `dashboard`; the events subtree under it is unchanged.
7. **Consumers.** `events` lists schedd and vmmd as consumers; vmmd joins the
   known consumers and watches platform flags through the runtime-config
   watcher (apply receipts as `vmmd`). A flag change reaches every daemon
   within seconds; schedd and vmmd reuse an app's flag subject for 30 s.

## Consequences

- An operator can stop the event bus in an incident by turning the `events`
  kill switch off, and turn it back on without losing an accepted event.
- Because `events` is a preview capability (default off), an account without
  an allow rule now gets `503 events_paused` from in-guest and platform-tenant
  publishing, and its customer events routed through subscriptions created
  before this change wait until it is released.
- While the switch is off, server-side events accumulate as receipts and count
  against event storage; publishes are already refused, so only those paths
  add to it.

## Rejected alternatives

- **Accept and hold customer publishes.** Producers would need no retries, but
  an incident would keep filling event storage, and publishes would turn into
  429s once it is full.
- **Hide the publish routes (404).** A producer may treat a missing route as
  permanent and drop events.
- **Hold already-admitted event invocations.** It needs every invocation
  tagged as event-delivered and adds a check to the drain's hot path.
