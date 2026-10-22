# ADR 1035: Consumer-group pause and resume

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Persist nullable `paused_at` on consumer groups. New groups start running. Expose
idempotent account and scoped-worker `POST .../pause` and `POST .../resume`
operations returning `paused` and nullable `paused_at`. Repeated pause preserves
the original timestamp; resume clears it. Account operations require app ownership,
admin scope and MFA. Scoped credentials require separate explicit `pause` or
`resume` grants, checked again under the group lock with current expiry, token,
revocation and permissions. Existing worker credentials gain neither grant.
Record admin operations in the audit trail and committed scoped operations in
existing credential usage metadata.

Serialize control with claims on the group row lock. Once pause commits, later
single claims return 204 and batch claims return 200 with an empty `claims` array
and `paused: true`. A claim transaction committed before pause can still return
or process its leases. Stop assignment and new lease issuance while paused.
Keep existing leases valid: renew, complete and retry continue under their own
permissions. Pause does not freeze lease clocks or retry delays. Claims still
materialize expired leases, including dead-letter webhook transitions, before
returning paused status; they do not claim that work again until resume.

Resume preserves assignment cursor, retry counters, snapshots and retry delays.
Retention continues during pause, so a long pause can require explicit gap
recovery. Administrative replay/resync/delete remain separate authorized actions.
Pause is not a cancellation mechanism for already issued work and does not imply
that every handler has stopped.

Expose status in generated Node/Python clients and the inspector. Add isolated
Node worker client `pause()`/`resume()` methods. Processing helpers treat paused
empty responses as normal polling and resume automatically on later claims;
existing handlers may finish. Default single-claim helpers still receive 204.

## Deployment and validation

Apply `20261012102700481_realtime_consumer_group_pause.sql` after consumer-group
and worker-credential migrations. Expand allowed worker permissions and usage
vocabulary to eleven operations. Downgrade refuses incompatible credential records
rather than silently stripping grants or usage history. Go/Node builds and static
specification checks are the validation scope. Runtime, migration and UI checks
remain deferred by request.
