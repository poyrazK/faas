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
and fresh VM telemetry; legacy single-box
installs use their existing local notification path. In-process reload without
restart is not yet supported.

`gregale secrets list` reports delivery for each key:

- `pending` means the current version has not yet reached a successfully
  started runtime.
- `delivered` means that exact version was staged into the runtime identified
  by the returned wake and instance IDs.
- `failed` means a runtime start attempted that version and failed. A later
  successful wake changes it to `delivered`.

Delivery is version-fenced. If a rotation races with a wake, completion of the
older wake cannot mark the newer value delivered. The API exposes only the
opaque version, status, timestamps, and runtime correlation IDs; it never
places plaintext or ciphertext in delivery metadata or audit events.
