# Delivered secrets

Secrets are write-only to the API, sealed at rest, and delivered only to the
app they belong to. Gregale injects them into the guest at wake; plaintext
values are never returned, logged, or included in deployment receipts.

```bash
gregale secrets set --app my-api STRIPE_SECRET_KEY="$STRIPE_SECRET_KEY"
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL" --restart
gregale secrets rotate --app my-api DATABASE_URL="$NEW_DATABASE_URL" --restart --wait-for-ack --timeout 2m
gregale secrets list --app my-api
gregale secrets unset --app my-api STRIPE_SECRET_KEY
```

Grant one app secret to a single companion in the deployment's `companions`
declaration (legacy field name: `sidecars`):

```yaml
companions:
  - name: proxy
    image: registry.example.com/proxy@sha256:<digest>
    type: sidecar
    env_secrets:
      DATABASE_URL: secret:DATABASE_URL
```

The companion gets only the app secrets it declares; it does not inherit the
main workload's secret set. Each reference resolves in the deployment's scope and
must name the same secret as its environment key. Missing grants fail the
wake rather than silently creating an empty variable.
Legacy sidecar images without a baked workload manifest preserve their command
and non-sensitive `api_env` compatibility behavior, but do not inherit main
workload app secrets.

This is a per-workload delivery allowlist, not a security boundary between
hostile workloads in one VM. Workloads share the guest kernel and privileged
code may inspect shared guest resources; use separate deployments when code
must not be trusted with another workload's runtime state.

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

`secrets rotate --wait-for-ack` is the in-process counterpart: after rotating,
the CLI polls the complete active authorized-runtime roster until every
reload-enabled runtime self-attests that it applied the current version. It
exits non-zero on an application-reported failure, timeout, or if a target
without a current app-applied acknowledgement has disabled/unknown reload
support. Combine `--restart --wait-for-ack` for apps without live-reload
support: the CLI waits for the correlated restart to reach a running instance,
then waits until every active authorized runtime acknowledges the current
secret version. Missing or unsupported capability stays pending on this path;
it is never treated as success. If no runtime is active without `--restart`,
the command succeeds and the rotation will be delivered on the next cold wake.

Apps that can reload credentials in-process may opt in via an OCI image label:

```dockerfile
LABEL com.gregale.secret-reload-signal="SIGHUP"
```

The supported signals are `SIGHUP`, `SIGUSR1`, and `SIGUSR2`; the selected
signal must differ from the image's `STOPSIGNAL`. On rotation, guest-init polls
the deployment's current secret scope, atomically replaces a JSON map at the
path in `FAAS_SECRETS_FILE`, then forwards the configured signal to the opted-in
workload. The file is mode `0400`, owned by the workload user, and lives on its
`/tmp` tmpfs. The process environment itself cannot change after
`exec`, so the application must handle the signal, reread the file, and update
its own clients or connection pools. Refresh is checked every 10 seconds; use
`--restart` when the app cannot implement that contract or when a rolling
replacement is preferred.

The main image's reload opt-in still supports single-workload deployments only;
a main image declaring the reload label is rejected when that deployment has
sidecars. Independently, each long-running sidecar image may declare the same
OCI label to opt that workload into live refresh. It receives only its own
explicit `env_secrets` grants; init helpers and sidecars without the image
opt-in retain restart delivery. Each workload gets its own `FAAS_SECRETS_FILE`,
revision file, signal target, status observation, and optional acknowledgement.
For sidecars, the platform stamps the workload name into the acknowledgement
endpoint URL so the self-attestation is recorded against that sidecar. Secret
reload requests are resolved against the live deployment's scope and the
requesting workload's allowlist. The main workload uses its `env_secrets`
allowlist (legacy single-workload deployments without one retain their
all-secrets-in-scope behavior). The application is responsible
for confirming to itself that it successfully reloaded. `secrets list` reports
wake-time delivery and a complete roster of active runtimes currently
authorized for each key by deployment scope and `env_secrets`. Each target
shows whether reload support is enabled, explicitly disabled, or unknown for a
legacy deployment, plus whether that runtime has reported. A missing report is
unknown, not success. `runtime_reload_targets_complete` distinguishes this
complete roster from an older server that only returned reporters. The report
does not claim that the application applied the new credentials unless its
self-attestation is present.

An opted-in app may make that last step explicit. After rereading
`FAAS_SECRETS_FILE` and successfully applying the new credentials to its own
clients, it can POST the non-sensitive revision from
`FAAS_SECRETS_REVISION_FILE` to `FAAS_SECRETS_RELOAD_ACK_ENDPOINT`:

```json
{"revision":"<64 lowercase hex characters>","status":"applied"}
```

If it cannot apply the new credentials, use `{"revision":"…","status":"failed"}`.
The platform accepts only those closed outcomes and does not accept arbitrary
error text or secret values. The response is `202` when recorded, `409` when
the revision is stale (reread and apply the latest projection), and `503` when
the host is temporarily unavailable (retry the same acknowledgement). Read the
revision before and after reading the secrets file; if it changed, reread so
the values and revision describe the same rotation. An acknowledgement is an
application self-attestation, not independent proof of its internal state.

The built-in `secret-reload-node` template is an executable Node.js + Postgres
reference for this contract. It uses the `SIGHUP` OCI label, reads a consistent
secret snapshot, tests a candidate database pool before swapping it in, retries
transient ACK failures, and sends only the opaque revision plus `applied` or
`failed` status. Start it with `gregale init --template secret-reload-node
--path secret-reload-node`; its README includes the first deploy and rotation
steps.

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
stale. Text and JSON include every active authorized target, including those
with no report, and flag disabled/unknown support separately from pending
reports. Existing deployments whose opt-in metadata predates this roster are
marked unknown; redeploy with an image reload-signal label to make the
capability explicit.
A successful signal means only that guest-init's signal operation succeeded,
not that the app handled it. An explicit app acknowledgement is shown
separately from guest-init's signal result; missing acknowledgements are
unknown. The API exposes only opaque versions, status, timestamps, and runtime
correlation IDs; it never places plaintext or ciphertext in delivery metadata
or audit events.
