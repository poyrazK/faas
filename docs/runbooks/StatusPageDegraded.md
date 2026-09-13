# Runbook · Gregale public status delayed or degraded

> **Customer endpoints:** `GET /v1/status`, `GET /v1/status/incidents/{public_id}`
>
> **Compatibility endpoint:** `GET /status/slo.json`
>
> **Alert:** `FaasPublicStatusEvaluationStalled`
> **Storage:** `status_incidents`, `status_incident_updates`, `status_observation_buckets`

## Symptoms

- `/v1/status` reports `data_status=stale` or `unavailable`.
- The public page shows “Updates delayed” or “Status temporarily unavailable.”
- `FaasPublicStatusEvaluationStalled` is firing because no complete rollup has
  been written for 15 minutes.
- A public capability is degraded or in outage and needs an operator narrative.

The site intentionally retains the last snapshot during refresh failures. Do
not interpret an operational component in a stale snapshot as fresh evidence.

## Triage

1. Check the public and compatibility responses:

   ```sh
   curl -fsS https://api.gregale.dev/v1/status | jq '{overall_status,data_status,updated_at}'
   curl -fsS https://api.gregale.dev/status/slo.json | jq .
   ```

2. Inspect evaluator freshness and outcomes:

   ```promql
   time() - apid_status_rollup_last_success_timestamp_seconds
   rate(apid_status_evaluations_total[15m])
   ```

3. Check `apid` logs for `status: scheduled evaluation failed` or
   `status: record rollup bucket failed`. Verify Prometheus is reachable from
   `apid`, the `ALERTS{alertstate="firing"}` query returns labeled vectors, and
   Postgres accepts writes to `status_observation_buckets`.

4. Confirm five rows exist for the current UTC five-minute bucket and that a
   retry does not create duplicates. Check clock synchronization if bucket
   timestamps are not divisible by 300 seconds.

5. List operator events and inspect their public pages:

   ```sh
   gregalectl status incident list --active
   gregalectl status maintenance list --active
   ```

## Publish an incident

Use public capabilities, not daemon names. Messages and titles are plain text;
Markdown and HTML are displayed literally.

```sh
gregalectl status incident create \
  --title "Elevated API errors" \
  --impact partial_outage \
  --components api_console,app_execution \
  --message "We are investigating elevated error rates."

gregalectl status incident update \
  --id PUBLIC_UUID --state identified \
  --message "The database connection pool is saturated."

gregalectl status incident update \
  --id PUBLIC_UUID --state monitoring \
  --message "Capacity was added and error rates have recovered."

gregalectl status incident resolve \
  --id PUBLIC_UUID --message "The service has remained healthy."
```

An incident may move between investigating, identified, and monitoring before
resolution. Resolution is terminal; create a new incident if impact returns.

## Schedule maintenance

```sh
gregalectl status maintenance schedule \
  --title "Network edge maintenance" \
  --components networking \
  --start 2026-09-12T22:00:00Z \
  --end 2026-09-12T23:00:00Z \
  --message "Traffic may briefly reconnect during the window."

gregalectl status maintenance start --id PUBLIC_UUID --message "Maintenance has started."
gregalectl status maintenance complete --id PUBLIC_UUID --message "Maintenance completed successfully."
```

Scheduled maintenance may instead be cancelled. Completed and cancelled events
are terminal. The overview only advertises maintenance within the next 30 days.

## Recovery

- Restore Prometheus connectivity or the failing Postgres write path. A fresh
  evaluator run should update the last-success gauge and clear the alert.
- Publish or update an incident when customers are affected; telemetry never
  invents an operator-authored major outage.
- Do not delete public events or edit timeline rows. Correct an inaccurate
  statement with a new update so the audit trail remains intact.
- After recovery, verify `data_status=fresh`, the UTC bucket contains all five
  capabilities, the public permalink works, and `/status/slo.json` remains
  valid JSON.

## Follow-up

- Record the public event UUID in the incident review.
- Review missing telemetry coverage and evaluator failures separately from the
  customer-impact root cause.
- Confirm audit rows include actor, action, prior/new state, and affected
  capabilities, and review `apid_status_incident_mutations_total` for rejects.
