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
- `rollout_state` is `stable`, `canary`, `promoting`, `paused`, or
  `rolled_back`.
- `auto_promote` opts a daemon canary into the fixed progression ladder;
  safety rollback remains active for every canary.

Every write also appends `runtime_config_revisions` and emits the
`runtime_config_changed` notification. Notifications are only wake-ups; apid
re-reads the table at boot, after LISTEN is established, after reconnect, and
on a five-second repair interval. A dropped notification therefore cannot
silently leave a daemon stale. Version-ordered application also prevents an
older concurrent PATCH from overwriting a newer value already live in a
process.

For daemon-level convergence, each watcher may also write a row to
`runtime_config_acks`. The row is keyed by setting, consumer, and node and
records the version plus `applied` or `failed` status. This separates the
control-plane acknowledgement from an edge daemon's observation and gives
the operations UI a durable basis for reporting partial fleet convergence.
Missing acknowledgement rows mean that a consumer has not observed that
version yet; they are not treated as successful application.

Daemon-scoped values with `rollout_percent` between 1 and 99 are treated as
canaries. The runtime-config safety controller waits for the observation
window, then checks current-version acknowledgements and the fleet gateway
signals (`gateway_requests_total` and
`gateway_request_duration_seconds`). A failed acknowledgement or a breach of
the default gate (at least 20 requests, at most 5% 5xx, and at most 2s p95)
automatically restores the newest older 100% revision. If no stable revision
exists, the canary is marked paused rather than guessing a fallback. Missing
Prometheus data never causes a mutation.

## Scope and canary targeting

The admin API accepts a `scope` and optional `scope_id` on PATCH, rollback, and
history reads. `global` is the fleet default; `control_plane` currently targets
the `apid` singleton; `daemon` targets a consumer name such as
`gatewayd-internal`; and `node` targets an exact `FAAS_NODE_NAME`/hostname.
Daemon watchers resolve values in this order:

1. matching node override;
2. matching daemon override;
3. global value.

Each row also carries `rollout_percent` (0–100). Percentage rollout is limited
to daemon-scoped rows in this slice. Matching daemons are selected by a stable
hash of the setting, target scope, and daemon identity, so widening a canary
does not reshuffle nodes. A daemon outside the canary keeps the lower-precedence
value and does not write an acknowledgement for the skipped override.

Use an exact node override (`rollout_percent: 100`) for a one-node emergency
test. Use a daemon override with 1–100% for a gradual fleet rollout. The
control-plane row is marked effective when it is persisted; the `acks` array is
the source of truth for whether each selected daemon has actually applied it.

Set `auto_promote: true` when creating a daemon canary to let the safety
controller advance it through `1% → 5% → 25% → 50% → 100%`. Each step waits
for the acknowledgement and health gates plus the observation window. The
default is false, so existing and manually managed canaries remain operator-
controlled. An operator edit wins through optimistic versioning and stops the
automatic ladder.

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

To manually promote a daemon canary, PATCH the same target with
`rollout_percent: 100`. The API evaluates the current canary health first and
rejects promotion when the gate is not met. A later regression is handled by
the background safety controller and is recorded as
`operator.runtime_config_auto_rollback` in the audit stream. Automatic step-up
uses `operator.runtime_config_auto_promote` for each successful transition.
