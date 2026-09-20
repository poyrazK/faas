# ADR-190 · Daemon durability primitives

- **Status:** accepted
- **Date:** 2026-09-20
- **Decision:** Four cross-cutting primitives so a daemon that is alive but
  not working is restarted, a control-plane outage does not become a
  data-plane outage, and Postgres connections are spent on work rather than
  on parked listeners: (1) a default deadline on every unary gRPC client
  call, (2) per-loop liveness beats gated onto the systemd watchdog, (3) a
  last-known-good route tier in the gateway, (4) one LISTEN connection per
  daemon.
- **Why:** every production outage in September 2026 had one of two shapes.
  A daemon stayed up and ready while its only working goroutine was blocked
  (2026-09-03 schedd prime wedge: a `PauseAndSnapshot` with no deadline,
  10+ minutes, `/metrics` answering the whole time). Or a control-plane
  dependency failed and the failure leaked into the hot path (2026-09-12
  fsn-1: ~46 parked LISTEN connections plus per-daemon pool budgets exceeded
  `max_connections=100`, every daemon died with SQLSTATE 53300; gateway
  route lookups 404 whenever Postgres is unreachable and the host is not in
  the LRU). Each primitive below closes one of those mechanisms at the
  seam where it recurs, not at the call site where it last happened.
- **Consequences:** see per-decision sections. New metrics:
  `<daemon>_grpc_client_calls_without_deadline_total{method}`,
  `<daemon>_loop_stalled{loop}`, `<daemon>_loop_last_beat_age_seconds{loop}`,
  `gateway_route_lookup_stale_served_total`,
  `<daemon>_db_notify_hub_reconnects_total`,
  `<daemon>_db_notify_hub_dropped_total{channel}`. New alert
  `FaasDaemonLoopStalled` (runbook `docs/runbooks/FaasDaemonLoopStalled.md`).
  New env: `FAAS_GRPC_DEFAULT_DEADLINE`, `FAAS_GATEWAY_ROUTE_STALE_TTL`,
  `FAAS_DB_NOTIFY_HUB`. Every `Type=notify` unit gains `WatchdogSec=`. No
  migrations.
- **Rejected alternatives:** listed per decision.

## 1. Default deadline on unary gRPC client calls

`wire.DialContext` is the single dial path for every in-tree client. It now
installs a unary client interceptor (`pkg/wire/grpc_deadline.go`): if the
caller's context has no deadline, the call is bounded by
`FAAS_GRPC_DEFAULT_DEADLINE` (default 60 s, above every engine budget) and
counted on `grpc_client_calls_without_deadline_total{method}`. A caller's
own deadline is never shortened. Streams are not covered; bridge sessions
are 24 h by design and own their lifetimes.

A `forbidigo` rule forbids `grpc.NewClient` / `grpc.Dial*` outside
`pkg/wire` so the interceptor cannot be bypassed by a new client. The one
exception, builderd's dial-only readiness probe, carries `//nolint` with a
reason.

Rejected: a lint that requires `ctx.Deadline()` at every call site. It
cannot see a deadline that was set three frames up, so it either
false-positives everywhere or is disabled. The interceptor enforces the
property at runtime and reports the sites that still rely on it.

## 2. Liveness beats and the systemd watchdog

Readiness (`/readyz`, `daemon_ready`) answers "are my dependencies up".
Liveness answers "am I still doing work". `pkg/wire.Liveness` records a
last-beat timestamp per named loop with a budget; `wire.StartWatchdog`
registers a `runtime` loop beaten by a 1 s goroutine, samples every loop
onto the two gauges, and starts `daemonunit.WatchdogFromEnv`, which sends
`WATCHDOG=1` only while no loop is past its budget. Every `Type=notify`
unit declares `WatchdogSec` (schedd 180 s, vmmd 120 s, gateways 60 s,
imaged/builderd 300 s, others 90–120 s), so a stalled loop becomes a unit
restart under `Restart=on-failure`.

schedd beats `main` at the top of every `Loop.Run` select iteration with a
180 s budget: a synchronous Prime can legitimately hold that goroutine for
`ColdBootTimeout` + the memory-scaled snapshot budget (about 110 s for a
Scale instance). vmmd beats `sweep` from the parent-mount sweep with a
three-interval budget. Other daemons get the `runtime` loop only; adding a
real loop is one `Register` and one `Beat`.

`StartLimitBurst=5/60s` already bounds a permanently stalled daemon the
same way it bounds a crash loop. `pkg/fcvm` and vmmd's journal helper
already scrub `WATCHDOG_*` from child environments, so Firecracker never
inherits the contract.

Rejected: watchdog on readiness. A daemon whose database is down must not
be restarted for it; restarting does not fix Postgres and would turn every
dependency outage into a restart storm. Rejected: a `/livez` HTTP probe
polled by an external agent. systemd is already the supervisor and the
`Type=notify` channel already exists; a second agent is a second thing to
keep alive.

## 3. Last-known-good route tier

`PGBackend.Lookup` consults `RouteCache` (host → app_id) and then the
Router; a Router error was a 404. Invalidation on `app_routes_changed`,
LRU eviction at 10k, and every gateway restart therefore made an app
unreachable for the length of a Postgres outage. `pkg/gateway/stale_routes.go`
keeps a bounded copy of the last `App` each host resolved to; `Lookup`
serves it only when the Router **errors**, never when the Router reports
the host does not exist. A positive not-found clears the entry.
`FAAS_GATEWAY_ROUTE_STALE_TTL` (default 10 min, `0` disables) bounds how
long a route removed **during** an outage can still be served; that is the
accepted trade-off, and `gateway_route_lookup_stale_served_total` makes the
coasting visible.

Rejected: persisting the route table to disk. It would cover a gateway
restart during an outage, but adds a file the daemon must trust and a
staleness problem across deploys; the in-memory tier covers the invalidate
and evict cases that actually recur. Revisit if a restart-during-outage
incident occurs.

## 4. One LISTEN connection per daemon

`db.Subscribe` parks one pooled connection per call, and daemons subscribe
from many places. `pkg/db/notify_hub.go` multiplexes every
`SubscribeWithReconnect` on a pool onto one connection: subscribers keep
their channel shape; the hub owns `LISTEN`/`UNLISTEN` diffs, reconnect with
the existing 100 ms → 5 s backoff, and fan-out. Two contracts are kept:
the first subscriber acquires synchronously so an unreachable database
still fails at boot, and a channel added to a running hub blocks until its
`LISTEN` is applied (daemons subscribe and then drain durable tables). One
contract changes: an overflowing subscriber (1024-deep buffer) drops the
newest notification and counts it instead of applying backpressure to the
daemon's only listener; every consumer already treats LISTEN as a wake-up
over a table with a safety tick. `FAAS_DB_NOTIFY_HUB=0` restores the old
path. `DaemonMaxConnections` is left unchanged in this ADR; lowering
budgets follows production evidence from `pg_stat_activity`.

Rejected: a pooler (pgbouncer) alone. It hides idle connections from
Postgres but a parked LISTEN session is not idle to a pooler either; the
multiplexing has to happen where the subscriptions are. A pooler remains
a sensible follow-up for the request-path pools. Rejected: dropping the
"LISTEN active on return" guarantee for simplicity; `pkg/sched/loop.go`
subscribes first and drains second precisely to avoid the gap.
