# ADR-20261123163340: Operator build queue report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261122135018 (operator snapshot health report).

## Context

Builds run in builder microVMs (ADR-003) on slots each compute node
provisions:

- reserved slots in `faas-cp-build.slice`;
- an opportunistic one while tenant residency is low;
- with `builder_elastic_slots`, borrowed ones in `faas-borrowed.slice`
  that give their memory back to tenants (ADR-20261118154632).

Operators see three pieces today, none of which shows the queue itself:

- the fleet queue depth and oldest enqueue time
  (`faas_build_queue_depth`);
- builderd heartbeats (`/v1/admin/obs/builder-heartbeats`);
- per-target worker health (`/v1/admin/obs/runtime-builds`).

They cannot see any of these:

- which builds are waiting and for how long;
- which builds run on which node;
- how full each node's reserved, opportunistic and borrowed slots are;
- how long builds take;
- why they fail.

A customer asking why their deploy is stuck, or a capacity question about
builder slots, needs all of these in one place.

## Decision

One operator session read:

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/build-queue?window=24h` | `ObsBuildQueueReport` |

`window` is 1m to 168h and defaults to 24h.

It reads two new optional store queries, `state.BuildQueueReportReader`
(sqlc, no migration):

- `OperatorActiveBuilds` returns queued and running builds with their app,
  longest waiting first, up to `ObsBuildQueueActiveMax` (500). The read
  uses `builds_queued_count_idx` and `builds_running_started_idx`.
- `OperatorFinishedBuildsSince` returns builds that finished in the window,
  newest first, up to `ObsBuildQueueFinishedMax` (20,000). The read uses
  `builds_finished_at_idx`.

Slot use comes from builderd's existing gauges, read in one PromQL instant
query within `ObsBuildQueuePrometheusBudget` (2 s):

- `builderd_build_slots_limit`
- `builderd_build_slots_reserved`
- `builderd_build_slots_in_use`
- `builderd_build_slots_active{kind}`

Metrics discovery gives every builderd target a `node` label. builderd
labels a slot only reserved or borrowed, so the report derives
opportunistic slots as the reserved-slice builds beyond the reserved count.

If Prometheus is unconfigured or fails, `slots_state` says so, and each
node row keeps only the database's running count.

`pkg/apid/buildqueue` (ADR-982) reports:

- **Queue totals:**
  - queued builds;
  - deferred builds (`not_before` still ahead), counted apart from queued;
  - running builds;
  - the oldest wait;
  - builds waiting longer than `ObsBuildQueueWaitWarnSeconds` (5 min);
  - running builds past `BuildTimeoutSeconds`.
  - A deferred build waits from its `not_before`, as
    `faas_build_queue_oldest_enqueued_timestamp_seconds` counts it.
- **Window totals:**
  - succeeded, failed and cancelled builds;
  - nearest-rank p50 and p95 of build duration and of queue wait;
  - cache hits, misses and invalidations;
  - preempted builds.
- **Breakdowns:**
  - queue and outcomes by build kind;
  - failed builds by `failure_class`, with "unclassified" for none.
- **Per-node slots:** for each compute node, its running builds and slot
  use.
- **Lists:**
  - the 50 longest-waiting queued builds;
  - the 50 longest-running builds;
  - the 25 slowest finished builds;
  - the 25 most recent failures.

## Consequences

- **Console:** gets a Build queue page under On call.
- **Node attribution:** only running builds are attributed to a node. The
  `builds_builder_node_scope` trigger clears `builder_node_name` when a
  build leaves running, so finished builds carry no node, and failures are
  not broken down by node. A running build whose claim has not recorded a
  node yet is listed under "unrecorded".
- **Read cost:** no migration. Both reads are bounded and use existing
  indexes, and only operators read them, on demand.
- **Existing signals stay:** the queue depth gauge, its alerts and the
  runtime-builds health view are unchanged. This report explains them.
- **Visibility:** the route is operator-only and excluded from the public
  OpenAPI spec, like the other `/v1/admin/obs/*` routes.
