# ADR-208 · Runtime secret delivery uses a no-snapshot restart

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Every app-secret set, rotate, or delete marks all warm and init
  snapshots for every app deployment stale. `gregale secrets set --restart`
  and `gregale secrets rotate --restart` enqueue a durable runtime-configuration
  restart: schedd destroys resident VMs without snapshotting them, invalidates
  snapshots again while holding the app lifecycle lock, and cold-boots one
  replacement with the current sealed-secret set. `gregale env push --restart`
  uses the same path.
- **Why:** Guest-init turns `/etc/faas/secrets.env` into process environment at
  boot. Replacing that file cannot change an already-running process, and the
  normal app restart captures process memory before restoring it. Using that
  path after rotation can therefore preserve exactly the credential the user
  asked to replace.
- **Durability:** The apply-now request has its own
  `runtime_config_restart` notification channel backed by the notification
  outbox. apid returns `202` only after the request is queued. schedd
  acknowledges it only after the replacement wake succeeds; failures remain
  replayable. The existing broad `app_changed` channel remains advisory.
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
  environment. With `--restart`, the app experiences a restart and the
  replacement receives the new value. In-process file delivery and reload
  hooks are deferred because environment variables cannot be changed safely in
  an existing process.
- **Failure posture:** If durable enqueue fails, apid releases its restart
  claim and returns `503`. If VM destruction, snapshot invalidation, or wake
  fails later, the outbox retries while the app remains cold rather than
  restoring an old credential.
