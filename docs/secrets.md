# Delivered secrets

Secrets are write-only to the API, sealed at rest, and delivered only to the
app they belong to. Gregale injects them into the guest at wake; plaintext
values are never returned, logged, or included in deployment receipts.

```bash
gregale secrets set --app my-api STRIPE_SECRET_KEY="$STRIPE_SECRET_KEY"
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL" --restart
gregale secrets list --app my-api
gregale secrets unset --app my-api STRIPE_SECRET_KEY
```

Use stdin or an environment variable when setting a value in automation, and
grant CI only `secrets:write` plus the scopes it needs to deploy. Secret names
follow the same uppercase key contract as [environment variables](env.md).
Rotate a value by writing the same key; the operation is audited without
recording the value. Every mutation invalidates snapshots that already exist;
the `--restart` path additionally prevents the live process with the old value
from being captured into a replacement snapshot.

By default, a running process keeps its current environment and the new value
arrives on the next cold wake. Add `--restart` to `secrets set` or `secrets
rotate` to apply immediately. Gregale durably queues a rolling configuration
refresh: it cold-boots replacements with the current environment, routes new
requests to them, waits for route convergence and in-flight requests to drain,
then destroys the old processes without snapshotting their old environment.
The scheduler keeps app concurrency at or below its configured ceiling plus
one temporary slot; node RAM and CPU limits still apply, so a refresh can
remain pending until capacity is available. In a multi-node fleet, the drain
waits for registered gateways to acknowledge the route update
and fresh VM telemetry; legacy single-box installs use their existing local
notification path.

Apps that can reload credentials in-process may opt in via an OCI image label:

```dockerfile
LABEL com.gregale.secret-reload-signal="SIGHUP"
```

The supported signals are `SIGHUP`, `SIGUSR1`, and `SIGUSR2`; the selected
signal must differ from the image's `STOPSIGNAL`. On rotation, guest-init polls
the deployment's current secret scope, atomically replaces a JSON map at the
path in `FAAS_SECRETS_FILE`, then forwards the configured signal to the main
application. The file is mode `0400`, owned by the app user, and lives on the
guest's `/tmp` tmpfs. The process environment itself cannot change after
`exec`, so the application must handle the signal, reread the file, and update
its own clients or connection pools. Refresh is checked every 10 seconds; use
`--restart` when the app cannot implement that contract or when a rolling
replacement is preferred.

This opt-in currently supports single-workload deployments only. A deployment
with sidecars is rejected when the image declares the reload label, preserving
the existing boundary that sidecars do not receive the main workload's
secrets. Secret reload requests are resolved against the live deployment's
scope and `env_secrets` allowlist (legacy deployments without an allowlist keep
their existing all-secrets-in-scope behavior). The application is responsible
for confirming to itself that it successfully reloaded. `secrets list` reports
both wake-time delivery and guest-init's latest live-refresh observation, but
does not claim that the application applied the new credentials.

`gregale secrets list` reports delivery for each key:

- `pending` means the current version has not yet reached a successfully
  started runtime.
- `delivered` means that exact version was staged into the runtime identified
  by the returned wake and instance IDs.
- `failed` means a runtime start attempted that version and failed. A later
  successful wake changes it to `delivered`.

Delivery and live-refresh observations are version-fenced. If a rotation
races with a wake or refresh report, the older result cannot mark the newer
value delivered or reloaded. The CLI labels live-refresh outcomes as runtime
file updated/unchanged/failed and whether the signal was sent, queued, or
failed; a reported version different from the current version is shown as
stale. A successful signal means only that guest-init's signal operation
succeeded, not that the app handled it. These are latest-per-secret
observations from one reporting runtime, not a fleet-wide health guarantee;
the API includes that runtime's ID. The API exposes only opaque versions,
status, timestamps, and runtime correlation IDs; it never places plaintext or
ciphertext in delivery metadata or audit events.
