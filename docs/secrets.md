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

Use `--json` or `FAAS_JSON=1` in scripts. Successful `set`, `rotate`, and
`unset` commands print one JSON receipt to stdout; secret values are never
included. A `set` receipt includes the app, scope, updated key names, and
whether a restart was requested (plus its `wake_id` when returned). A `rotate`
receipt includes the app, scope, key, `rotated_at`, sealing `kid`, and any
restart or runtime-acknowledgement details. An `unset` receipt includes the app,
scope, key, and `deleted: true`. Any deferred-application guidance is returned
in a `warnings` array, so stdout remains parseable JSON.

```bash
gregale --json secrets set --app my-api STRIPE_SECRET_KEY="$STRIPE_SECRET_KEY"
printf '%s\n' "DATABASE_URL=$DATABASE_URL" | FAAS_JSON=1 gregale secrets rotate --app my-api --from-stdin
gregale --json secrets unset --app my-api OLD_API_KEY
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

For an opted-in running workload, `secrets unset` is delivered as a replacement
projection with the deleted key omitted, followed by the configured reload
signal. The application must remove the old credential from its own clients;
removing the key from the file cannot erase values already held in process
memory. Restart-only workloads keep their existing value until the process is
replaced by a deployment that no longer grants the key. A cold wake that still
references the deleted key fails closed; remove the grant before replacing
that workload.

Deletion also writes a durable, value-free revocation record containing the
authorized runtime roster captured in the same transaction. The record keeps
its target history after the secret and runtime are removed; it stores only
the app, scope, key name, opaque revision, timestamps, closed status/error
codes, and runtime correlation IDs—never secret material. A runtime is counted
as acknowledged only after its application reports that the deleted key is no
longer in use. `gregale secrets unset --wait-for-ack --timeout 2m` waits for
that proof and exits non-zero on timeout, application failure, or a target
that cannot live-reload. Without the wait flag, unset returns the revocation
ID immediately; query its progress with
`GET /v1/apps/{slug}/secret-revocations/{id}`. The API preserves its legacy
`204 No Content` response unless the caller sends
`Prefer: return=representation`. A blocked target needs a
restart/redeployment path, and removing the projected file alone cannot erase
a credential already held by process memory.

The main image and each long-running sidecar image may independently declare
the reload OCI label in the same deployment. Main receives only its explicit
`env_secrets` grants when companions are declared; legacy main-only deployments
without a grant map continue to receive their scoped secrets. A sidecar receives
only its own explicit `env_secrets` grants; init helpers and sidecars without the image
opt-in retain restart delivery. Each workload gets its own `FAAS_SECRETS_FILE`,
revision file, signal target, execution generation, status observation, and optional acknowledgement.
For sidecars, the platform stamps the workload name into the acknowledgement
endpoint URL so the self-attestation is recorded against that sidecar. Secret
projections are prepared before reload workers or workload processes start.
Process restarts preserve the current revision and use the current granted
values; revoked keys are removed from older environment layers. Projection
ownership is resolved against each workload's image, including named sidecar
users, and stays fixed for subsequent updates. Secret
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
`FAAS_SECRETS_SNAPSHOT_FILE` and successfully applying its `secrets` map to its
own clients, it can POST the non-sensitive `revision` from that same JSON
envelope and its own `FAAS_SECRETS_RELOAD_GENERATION` to
`FAAS_SECRETS_RELOAD_ACK_ENDPOINT`:

```json
{"revision":"<64 lowercase hex characters>","status":"applied","generation":"<FAAS_SECRETS_RELOAD_GENERATION>"}
```

If it cannot apply the new credentials, keep the same revision and generation
and set `status` to `failed`. Older guests without the generation variable may
send revision-only ACKs; strict binding adoption treats their coverage as unknown.
The platform accepts only those closed outcomes and does not accept arbitrary
error text or secret values. The response is `202` when recorded, `409` when
the revision is stale or the calling process has been replaced, and `503` when
the host is temporarily unavailable (retry the same acknowledgement). Reread
and apply the latest projection for a stale revision, keeping your original
execution generation; never borrow a replacement process's ID. The
mode-0400 snapshot contains `{revision,secrets}` in one immutable file, so one
read binds the values to their exact revision. Guest-init publishes a complete
generation before updating restart state or signalling the workload. A failed
publication preserves the previous generation. Retry a transient missing-file
lookup during old-generation cleanup; a missing or malformed advertised
snapshot must not fall back to separate files. The initial revision is empty
until the first successful host fetch and cannot be acknowledged.

`FAAS_SECRETS_FILE` and `FAAS_SECRETS_REVISION_FILE` retain their existing paths
and formats through platform-owned symlinks. When the snapshot environment
variable is absent on an older guest, the helper retains the separate-file
reader, including its before/after revision check. That older contract cannot
guarantee a consistent pair during publication; replace the guest to obtain
the atomic envelope. An acknowledgement is an application self-attestation,
not independent proof of its internal state.

The built-in `secret-reload-node` template is an executable Node.js + Postgres
reference for this contract. It uses the `SIGHUP` OCI label, reads a consistent
secret snapshot, tests a candidate database pool before swapping it in, retries
transient ACK failures, and sends only the opaque revision plus `applied` or
`failed` status. Start it with `gregale init --template secret-reload-node
--path secret-reload-node`; its README includes the first deploy and rotation
steps.

On initial startup the starter waits up to 30 seconds for a valid, nonempty
revision, polling with a 100-ms-to-two-second backoff. Only a valid secret map
with the platform's empty initial revision is pending; malformed or unreadable
projections still fail closed. It does not apply or ACK unversioned values.
The helper's `startupTimeoutMs` option configures this availability deadline;
database initialization and ACK transport are separate. The reload-handler
marker is written before waiting, but HTTP serving starts only after credentials
are applied and their ACK is accepted. Rotations and signal reloads remain
serialized behind startup. After seeing the first valid revision, all subsequent
reads use strict validation. SIGTERM/SIGINT cancel the wait and ACK work. Redeploy
the updated starter to obtain this behavior; no platform migration is required.

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

### Startup-safe reload notifications

Set `com.gregale.secret-reload-readiness="required"` alongside the existing
reload-signal OCI label to gate reload delivery on application handler readiness.
On each process start, after installing the selected handler, write exactly
`ready\n` to the existing file at `FAAS_SECRETS_RELOAD_READY_FILE`. Its path is
platform-owned, private to that process generation, and removed when it exits.
A prior process's marker cannot unlock a restart. The Node starter implements
this handshake before awaited database initialization. An absent marker variable
indicates an older guest; its prior signal contract still applies.

Guest-init keeps only the latest pending revision, retries failed sends with a
one-second-to-one-minute backoff, and completes queued observations after actual
delivery. A replacement process that starts with the current values avoids a
redundant signal and reports `projection=updated, signal=not_attempted` with no
error. This proves only startup environment delivery. The marker proves readiness
to receive a signal. Neither substitutes for the separate application ACK.

Images without the readiness label keep their startup-gate behavior and must
install their signal handler early; guest-init cannot infer completion of
arbitrary initialization. Upgrade the outcome validator and apply the new observation migration before
replacing guests, and redeploy an image with the readiness label to obtain the stronger
startup guarantee. Required markers that are never written leave delivery queued.

Each opted-in process receives `FAAS_SECRETS_RELOAD_GENERATION`, a platform-owned
32-character opaque execution ID. Echo it as `generation` in every application
ACK alongside `revision` and `status`. The Node starter does this automatically.
Guest-init registers the ID before exec and retires it after exit; an in-VM
restart registers a new ID even when secret values and versions have not changed.
Registration clears the previous live application ACK. A late ACK or retirement
from the old process cannot overwrite the replacement's evidence. The metadata
proxy forwards the caller's ID unchanged. An ACK is still a trusted application's
self-attestation, and host knowledge of process exit can lag during a transport
outage.

Strict binding promotion requires the active process ID to match the ACK's ID.
The adoption target exposes optional `process_generation` and
`application_ack_generation` fields; missing coverage is `unknown`. Legacy guests
can still report version-only ACKs while no process generation has been registered,
but those receipts cannot satisfy strict adoption. Apply the ADR-508 migration and
upgrade vmmd first, then redeploy applications with the updated helper and guest
together. Old helpers running in new guests receive `409` until they echo their own
execution ID. Historical completed revocation operations retain their completion
history; live receipts are invalidated on restart.
