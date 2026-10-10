# Route regression webhooks

App webhooks can notify your systems when Gregale's route regression detector
finds that a route has regressed or later recovered. Subscribe to either event
or both:

```sh
gregale webhooks add --app checkout-api \
  --target-url https://ops.example.com/gregale/regressions \
  --secret "$WEBHOOK_SECRET" \
  --event debug.regression.detected \
  --event debug.regression.resolved
```

`debug.regression.detected` is enqueued when an observation first becomes
active, including when a previously resolved regression is detected again.
`debug.regression.resolved` is enqueued when the observation is resolved by
the detector or from the debugger lifecycle controls. Routine detector refreshes
do not generate more deliveries. Each transition and its webhook rows commit
atomically; the existing app-webhook dispatcher signs, retries, and exposes
dead deliveries for replay.

The event payload is metadata-only:

```json
{
  "app_id": "...",
  "deployment_id": "...",
  "route": "POST /checkout",
  "p95_ms": 183,
  "p95_base_ms": 41,
  "affected_count": 823,
  "regression_factor": 4.46,
  "first_detected_at": "2026-09-27T10:41:13Z",
  "last_detected_at": "2026-09-27T10:46:13Z",
  "state": "active",
  "transition_id": "2026-09-27T10:41:13Z",
  "resolved_at": null,
  "suspected_dependency": {
    "type": "app_dependency",
    "kind": "postgresql",
    "name": "SELECT orders",
    "p95_base_ms": 82,
    "p95_ms": 191,
    "regression_factor": 2.33
  }
}
```

`suspected_dependency` names the database, cache, HTTP, RPC or messaging
dependency whose p95 regressed most between the previous and the regressed
deployment on that route. It is present when the app's spans reach the
debugger (for example with [request tracing](tracing.md)) and a dependency
crossed the regression thresholds; otherwise it is `null`. The name is a
bounded identity such as `SELECT orders` or an HTTP host, never a query value,
key, URL path or credential.

The detector does not include request bodies, user identifiers, IP addresses,
or trace/span contents in these webhook payloads. Delivery is at least once;
verify the signature and deduplicate using the delivery ID in the signed
envelope/headers. `transition_id` identifies one route/deployment lifecycle
transition in the event data.
