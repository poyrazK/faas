# Alerts

Alerts turn platform signals into signed webhook deliveries. Configure them per account or app and keep the receiver idempotent.

## Configure an alert

```bash
gregale alerts preset list
gregale alerts preset enable availability --app APP_ID --webhook-url https://example.com/hooks/gregale --webhook-secret "$ALERT_SECRET"
gregale alerts list --app APP_ID
gregale alerts rm --app APP_ID ALERT_ID
```

Useful presets include availability, latency, error rate, out-of-memory, certificate expiry, quota, and spend. Use the preset as a starting point; narrow the threshold and notification window in the generated configuration when needed.

Deliveries include an event id, timestamp, alert state, and signature. Verify the signature before processing, deduplicate by event id, and return a 2xx response quickly. Retryable failures are retried with backoff; a permanently failing endpoint is paused so it cannot amplify an incident.

For dashboards and SLOs, use the app metrics endpoint and correlate alert event ids with deployment ids. Never put credentials in an alert URL.
