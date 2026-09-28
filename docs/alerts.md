# Alerts

Alerts turn platform signals into signed webhook deliveries. Configure them per account or app and keep the receiver idempotent.

## Configure an alert

```bash
gregale alerts preset list
# Choose one enable command; action defaults to webhook.
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable availability --app APP_ID --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
# Safe-release actions are available when a preset should trigger a rollout response.
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable availability --app APP_ID --action rollback --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
gregale alerts list --app APP_ID
gregale alerts rm --app APP_ID ALERT_ID
```

Useful presets include availability, latency, error rate, out-of-memory, certificate expiry, quota, and spend. Use the preset as a starting point; narrow the threshold and notification window in the generated configuration when needed.

For login abuse, `login_target_pressure` is an opt-in security preset for apps that have enabled `observe_targets` on a pre-auth POST route. It sends a webhook when the aggregate target-threshold signal exceeds five events in 15 minutes. The alert includes `observations_path` for the API and `dashboard_path` for the read-only pre-auth protection view. It never includes the login target digest. This preset supports webhook notifications only.

Deliveries include an event id, timestamp, alert state, and signature. Verify the signature before processing, deduplicate by event id, and return a 2xx response quickly. Retryable failures are retried with backoff; a permanently failing endpoint is paused so it cannot amplify an incident.

For dashboards and SLOs, use the app metrics endpoint and correlate alert event ids with deployment ids. Never put credentials in an alert URL.
