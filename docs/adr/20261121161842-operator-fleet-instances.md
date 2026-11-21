# ADR-20261121161842: Operator fleet instance list and instance lookup

Status: Accepted

Extends ADR-20261115175927 (operator console routes).

## Context

The operations console showed running instances only one node at a time,
inside the node detail. An operator asking "what is running right now, and for
whom" had to open every node, and could not link to one instance: the console
had no read of an instance by id. The wake that started an instance already
has a timeline route (`GET /v1/admin/obs/wakes/{wake_id}`), and the app detail
already lists the app's recent invocations, but nothing tied an invocation to
the instance that handled it.

## Decision

Two operator session reads, registered beside the node routes:

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/instances?state=running\|resident&node=&app_id=&account_id=&limit=` | `ObsInstanceListResponse`: resident instances across every compute node, most recently used first |
| `GET /v1/admin/obs/instances/{id}` | `ObsInstanceRow` for one instance in any state |

The list reads `state.ListResidentInstancesOnNodeID` once per compute node, so
it uses the resident partial index and never the instance history. `state`
defaults to `running`; `resident` adds every other state that holds a VM.
`node` is a node name; `app_id` and `account_id` filter after the read. `limit`
defaults to `ObsInstanceListDefault` (200) and is capped at
`ObsInstanceListMax` (1000); `truncated` says more matched. A node whose read
fails is named in `unavailable_nodes` and the rest still answer.

`ObsInvocationRow` gains `instance_id` (omitempty), so the console can show
which of an app's recent invocations an instance handled.

## Consequences

- The console gets a fleet-wide Running instances page and an instance page
  that combines this lookup with the wake timeline and the app detail.
- No migration and no new store method. The cost is one indexed read per
  compute node per request.
- Both routes are operator-only and excluded from the public OpenAPI spec like
  the other `/v1/admin/obs/*` routes.
