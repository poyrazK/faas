# Account release webhooks

Register one signed receiver for release events from every app owned by your
account, including apps you create later. This is useful for platforms that
need one deployment/rollout event stream for their own release dashboard,
approval workflow, or incident system. App-scoped webhooks remain available
when each app needs a separate target or signing key.

Account release receivers are available on the same plans as outbound app
webhooks. Each receiver consumes one slot from the existing **per-account
webhook quota**, which is shared with app-scoped subscriptions.

## Create a receiver

Choose a non-empty subset of `deployment.live`, `deployment.failed`,
`rollout.completed`, and `rollout.aborted`. Account receivers cannot subscribe
to all present and future events with an empty filter.

```sh
gregale webhooks account add \
  --target-url https://platform.example.com/gregale/releases \
  --event deployment.live --event deployment.failed \
  --event rollout.completed --event rollout.aborted \
  --delivery-format cloudevents
```

The CLI generates a signing secret if you omit `--secret` and displays it
**once**. Store it in your receiver's secret manager. For automation, pass an
existing secret via `--from-stdin` to keep it out of shell history. The API
equivalent is `POST /v1/account/release-webhooks` with `target_url`,
`webhook_secret`, `event_filter`, and optional `retry_policy`,
`delivery_format`, and `enabled` fields. The response contains only
`webhook_secret_sealed_masked: "***"`; the secret is sealed at rest and is
never readable through the API.

The default delivery format is Gregale JSON. `cloudevents` sends CloudEvents
1.0 structured mode (`application/cloudevents+json`), retaining the same
signed headers. Every delivery records the source `app_id` even though the
subscription itself has no app ID. If both an app receiver and an account
receiver match an event, each gets an independent delivery ID and retry state.

## Verify each delivery

Read the raw request body, `X-Faas-Webhook-Timestamp`, and
`X-Faas-Delivery-Id`. Compute HMAC-SHA256 with the stored secret over the
exact bytes `<unix_timestamp>.<delivery_id>.<raw_body>`, then compare its hex
digest in constant time with the hex part of
`X-Faas-Webhook-Signature: sha256=<hex>`. Reject timestamps outside your
replay window and deduplicate by delivery ID. Do not parse and re-serialize
the body before checking the signature.

## Operate and recover

```sh
gregale webhooks account list
gregale webhooks account info <webhook-id>
gregale webhooks account update --disable <webhook-id>
gregale webhooks account deliveries --page-size 50 <webhook-id>
gregale webhooks account retry <webhook-id> <dead-delivery-id>
gregale webhooks account rotate-secret --from-stdin <webhook-id>
gregale webhooks account rm <webhook-id>
```

The delivery list is cursor-paginated and spans all source apps; each row has
its `app_id`, event, attempt count, status, and last response/error details.
Only `dead` deliveries can be manually retried. The normal dispatcher uses
the same retry and dead-letter policy as app webhooks. Rotate the receiver's
secret in your receiver and Gregale together; the rotate response is masked.
Deleting a receiver removes its delivery history, so export anything you need
for audit before deletion.

The same operations are available under
`/v1/account/release-webhooks[/{id}]`, with `/rotate-secret`, `/deliveries`,
and `/deliveries/{did}/retry` subroutes. These are active-account routes; IDs
from another account or app-scoped webhooks are not exposed through them.
