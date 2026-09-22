# PostgreSQL fleet capacity

This role derives direct PostgreSQL capacity from the complete
`[compute_nodes]` inventory. It accounts for one control plane, every active
compute node, overlapping compute generations during a rolling deployment,
operator headroom, and five superuser-reserved connections. A standalone node
join reserves one overlapping generation. `gregalectl deploy join-fleet`
overrides that value with the batch's effective worker count, so
`--max-parallel` and database admission use the same concurrency contract.

The production daemon pool maxima are 40 sessions on the control plane and 34
per compute node. The role keeps the steady fleet below the 75% warning
threshold and fits the declared rollout overlap. It rounds the resulting
postmaster ceiling to ten. With the standalone one-node overlap, two compute
nodes derive 160 connections, ten derive 520, and twelve derive 610. A
four-worker `join-fleet` batch at ten nodes derives 540 instead.

The role changes only `max_connections` and
`superuser_reserved_connections`, restarts PostgreSQL only when either file
setting changes, and verifies the live settings before a node join continues.
It is included by both `bootstrap.yml` and `node_join_control_plane.yml`. A RAM
guard rejects admission before modifying PostgreSQL when the control-plane
host is too small. The 4 GB beta host supports the current two-node standalone
join shape; the 12-node, one-overlap contract requires about 14.3 GB and
therefore fits a 16 GB database host. Higher batch concurrency can require a
larger database host and is rejected before any new compute node is activated.

## Pooled fleets

`faas_pgbouncer_enabled` changes two terms in the derivation and nothing else.
A compute node's ordinary pools no longer reach the postmaster — they
terminate at pgbouncer and share its server pool, which is a constant rather
than a per-node cost. What still scales per node is the session-scoped residue
that cannot be pooled: `LISTEN` and session advisory locks on the direct DSN
(`faas_postgres_per_compute_direct_budget`, 5 database daemons ×
`db.directHubOnMaxConns`).

| Nodes | Unpooled | Pooled |
|---|---|---|
| 2 | 160 conns / 3.4 GB | 200 conns / 4.4 GB |
| 12 | 610 conns / 14.0 GB | 330 conns / 7.4 GB |
| 50 | 2,330 conns / 54.3 GB | 840 conns / 19.4 GB |
| 100 | 4,600 conns / 107.5 GB | 1,500 conns / 34.8 GB |

**Do not enable the pooler below four compute nodes.** pgbouncer's server pool
is a fixed 80 backends, so on a two-node fleet it costs more than the 68 it
saves. The crossover is at 3⅓ nodes: `34n = 80 + 10n`.

Note what pooling does and does not fix. It takes a 100-node fleet from
impossible to a 48 GB database host, but the remaining 1,500 is dominated by
the per-node `LISTEN` term, which is irreducible while every compute daemon
subscribes to notifications. Cutting it further means fewer subscribing
daemons per node, not a better pooler.

The per-daemon direct budget is hub-dependent: with `FAAS_DB_NOTIFY_HUB=0`
every subscriber parks its own `LISTEN` connection on the direct pool, so
`db.directMaxConns` falls back to the pre-hub per-daemon sizing. A fleet
running any daemon with the hub disabled needs materially more direct
connections than the table above; re-derive before enabling that mode on more
than one node.

The per-daemon maxima come from `pkg/db.DaemonMaxConnections`, which applies
while the ADR-190 notify hub is active — the supported configuration. Setting
`FAAS_DB_NOTIFY_HUB=0` selects `DaemonMaxConnectionsNotifyHubDisabled`
instead, which keeps the larger pre-hub sizing because every subscriber then
parks its own connection. A fleet running any daemon with the hub disabled
therefore needs more connections than the figures above; re-derive the
ceiling before enabling that mode on more than one node.
