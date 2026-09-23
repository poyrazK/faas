# ADR-210 · Runtime secret delivery uses a no-snapshot rolling restart

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Every app-secret set, rotate, or delete marks all warm and init
  snapshots for every app deployment stale. `gregale secrets set --restart`
  and `gregale secrets rotate --restart` enqueue a durable runtime-configuration
  restart: schedd cold-boots replacements with the current sealed-secret set,
  then withdraws stale VMs from routing, waits for route convergence and
  in-flight requests to drain, and destroys them without snapshotting. Service
  deployments are refreshed to their desired replica count while app
  concurrency stays at or below its configured ceiling plus one temporary
  slot. `gregale env push --restart` uses the same path.
- **Why:** Guest-init turns `/etc/faas/secrets.env` into process environment at
  boot. Replacing that file cannot change an already-running process, and the
  normal app restart captures process memory before restoring it. Using that
  path after rotation can therefore preserve exactly the credential the user
  asked to replace. A rolling handoff keeps the previous process serving until
  its replacement is ready, without ever making its memory a future snapshot.
- **Durability:** The apply-now request has its own
  `runtime_config_restart` notification channel backed by the notification
  outbox. apid returns `202` only after the request is queued. schedd
  acknowledges it only after every live scope has fresh serving capacity and
  all stale instances are retired; failures remain replayable. Candidate and
  predecessor states let a replay resume an interrupted handoff. The existing
  broad `app_changed` channel remains advisory.
- **Ownership:** apid writes customer intent and claims the app lifecycle;
  schedd remains the only writer of instance state; vmmd remains the only
  component that destroys or boots a VM. Snapshots remain a cache, never the
  source of truth.
- **Authorization:** Secrets remain scoped to one app and environment scope.
  The existing `secrets:write` plus MFA policy controls mutations, and vmmd
  resolves only that app's effective secret rows during boot. This ADR does not
  add cross-app reusable secrets or ACL bindings.
- **Semantics:** Without `--restart`, mutation is immediate in the control
  plane and visible on the next cold wake; a running process keeps its old
  environment. With `--restart`, fresh processes receive the new value before
  old processes stop serving. At most one concurrency slot above the app's
  configured ceiling is permitted, and ordinary node RAM/CPU admission still
  applies. In-process file
  delivery and reload hooks are deferred because environment variables cannot
  be changed safely in an existing process. Multi-node fleets require
  registered gateway acknowledgements and fresh VM telemetry; legacy
  single-box installs retain the local-notification compatibility path.
- **Failure posture:** If durable enqueue or the request-time snapshot
  invalidation fails, apid releases its restart claim and returns `503`. If
  replacement admission, gateway convergence, or request draining fails later,
  the outbox retries and stale VMs remain resident rather than being destroyed
  before a safe handoff. The app remains active while old capacity is serving.
- **Delivery evidence:** Every value mutation advances an opaque per-secret
  delivery version. Schedd records `pending`, `delivered`, or `failed` against
  the exact version it staged, using a compare-and-set so an older in-flight
  wake cannot acknowledge a concurrent rotation. Successful and failed
  attempts emit value-free audit events containing only app, secret names,
  wake/instance correlation, counts, timestamps, and a closed error code.
- **Amendment (issue #3360):** invalidating existing snapshots is not enough
  for the no-restart path. A live instance still holds the old environment,
  and its next idle park would capture a fresh snapshot of it that the next
  wake restores. Every runtime-config mutation therefore also stamps
  `app_runtime_config_changes.changed_at` (database clock, like
  `instances.started_at`). schedd's park path retires, instead of snapshotting,
  any instance that started before the stamp, and it drops a warm or init
  capture if the stamp moves while the capture runs. A failed stamp lookup
  also skips the capture: a missed snapshot costs one cold boot, while a stale
  one keeps a credential the customer replaced.
