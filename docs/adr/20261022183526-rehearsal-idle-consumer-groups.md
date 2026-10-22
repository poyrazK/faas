# ADR-20261022183526 · Contained workers see idle consumer groups

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** [ADR-20261021180000](20261021180000-rehearsal-realtime-capture.md)
- **Decision:** The run-local platform API (`api.svc.gregale`) answers a contained worker's consumer group calls as an idle group would. It never claims production messages and captures nothing.
- **Why:** An app that consumes a realtime consumer group, through the Node or Python worker helpers, failed inside a contained rehearsal: every group call was blocked. Serving the real group is not an option. A claim from a rehearsal would take production messages away from production workers.

## Context

Captured realtime publishes never reach retained channel history (ADR-20261021180000). So from inside the run, a group has nothing to claim: it is caught up, and that is the faithful answer. The production group's backlog belongs to production workers.

The SDK helpers poll: they read the group status, send heartbeats, claim with a long-poll wait, and settle what they claimed. These calls are polls, not business intents, so they are answered rather than captured.

## Decision

Both surfaces are served:

- the app-key routes `/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}`;
- the worker-credential routes `/v1/realtime/workers/groups/{group_id}`.

The group is resolved first, as apid loads it before the body:

- **By name:** the endpoint must belong to the addressed app or run workload, as for a captured publish, and the group must exist on that channel.
- **By ID:** the group must belong to the run's account.

Anything else is the public not-found. A call that changes state on a disabled endpoint is `400`, as in production.

| Call | Answer |
|---|---|
| `GET` the group | `200` with the group's configuration: lease, attempts, filter, ordering, dead-letter policy and trigger. Every counter and sequence is zero. |
| `POST .../claims` | Validated like apid, held for `wait_seconds` (at most 20), then `204`: nothing to claim. |
| `POST .../claims/batch` | Validated, held, then `200` with `claims: []` and every blocking flag false. |
| `POST .../claims/settle` | Validated, then `409 realtime_group_lease_lost`. The run never received a claim, so no token can complete one. |
| `POST .../claims/settle/batch` | Validated, then `200` with every item `lease_lost`. |
| `POST .../heartbeats` | Validated, then `200` with the worker live. Its ID is stable per group and worker. |

A group bound to a realtime trigger refuses claims and settlements with `409 realtime_group_bound_to_trigger`, as in production.

Answers carry `X-Gregale-Rehearsal: idle`. Nothing is captured, so these calls never appear in run evidence or affect a comparison.

## Consequences

- A worker keeps running inside the rehearsal and simply finds no work. To rehearse the handler, drive it from the recipe, as for queue consumers.
- The worker credential is not checked: rehearsal calls carry no credential the run can verify. A worker-credential call that omits `worker` is answered as worker `credential`.
- apid's history-preview gate and the consumer-group plan gate are not checked; the group exists, so both held when it was created.
- The memory store has no consumer groups, so it resolves none.
- The other endpoints of the realtime surface stay unserved: dead letters, replay, pause, drain and resume.
