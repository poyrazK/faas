# Operator runtime configuration

The operator console at `/operations/configuration` is the control-plane
surface for settings that used to require an SSH session and an edit to
`/etc/faas/*.env` or `apid.toml`.

## State model

Each catalogued key has a durable row in `runtime_config_entries`:

- `desired_value` is what the operator requested.
- `effective_value` is what the daemon acknowledged as live.
- `version` is an optimistic-concurrency number.
- `status` is `pending`, `applied`, `failed`, or `blocked`.

Every write also appends `runtime_config_revisions` and emits the
`runtime_config_changed` notification. Notifications are only wake-ups; apid
re-reads the table at boot, after LISTEN is established, after reconnect, and
on a five-second repair interval. A dropped notification therefore cannot
silently leave a daemon stale. Version-ordered application also prevents an
older concurrent PATCH from overwriting a newer value already live in a
process.

## Apply modes

`hot` settings are validated, swapped into the process snapshot, acknowledged,
and are immediately visible to new requests without restarting apid or
interrupting in-flight requests. Feature gates and the domain doctor TTL use
this path. The tenant-surfaces and HSTS gates are also consumed by the edge
daemons through the shared runtime-config watcher; data placement is consumed
by both schedd and meterd. The environment value is only a bootstrap fallback;
once a durable operator value has been applied, a restart cannot replace it
with the environment value.

`graceful`, `rolling`, and `break_glass` settings are never reported as live
just because the database write succeeded. The PATCH returns `202` with a
durable row in `runtime_config_operations`; a deployment/daemon controller
must claim that row, apply the change, and call the state-layer terminal
method. Successful completion updates both the operation and the matching
`runtime_config_entries` row in one database transaction. Failure/block reasons
remain visible to the operator. In the current deployment, no controller is
enabled for these modes, so mutable requests are immediately marked
`blocked` with `controller_unavailable` instead of remaining pending forever.
These modes are not part of the hot, zero-downtime guarantee until their
controller is enabled for the deployment. The API exposes
`controller_enabled` so an operator can distinguish an actionable queue from
an intentionally blocked setting.

Bootstrap secrets, listener addresses, billing provider selection, and daemon
role are deployment-managed and are intentionally not editable in the web
console. This prevents a UI action from creating a partial topology or a
credential outage. A runtime flag is still only as safe as the consumers that
subscribe to it; adding a new daemon consumer requires a versioned apply path
and an acknowledgement before the catalog is expanded.

## API

- `GET /v1/admin/config` — catalog plus desired/effective state.
- `PATCH /v1/admin/config/{key}` — hot apply or queue an asynchronous apply.
- `GET /v1/admin/config-operations/{id}` — poll an asynchronous apply.
- `GET /v1/admin/config/{key}/revisions` — inspect append-only history.
- `POST /v1/admin/config/{key}/rollback` — apply an older hot revision as a
  new revision; the request requires a reason and supports optimistic
  `expected_version` protection.

All writes require admin scope, MFA, a reason, and an optional expected
version. Sensitive values are redacted in list, operation, and revision
responses.
