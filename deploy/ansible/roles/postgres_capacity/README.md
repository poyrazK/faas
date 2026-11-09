# PostgreSQL fleet capacity

This role derives direct PostgreSQL capacity from the complete
`[compute_nodes]` inventory. It accounts for one control plane, every active
compute node, overlapping compute generations during a rolling deployment,
operator headroom, and five superuser-reserved connections. A standalone node
join reserves one overlapping generation. `gregalectl deploy join-fleet`
overrides that value with the batch's effective worker count, so
`--max-parallel` and database admission use the same concurrency contract.

The production daemon pool maxima are 61 sessions on the control plane and 34
per compute node. A compute node also runs gatewayd-public when its cell puts
the app edge on compute nodes (`faas_edge_on_compute_nodes`, 3 more) and
s3-gatewayd when it puts the object gateway there
(`faas_object_gateway_on_compute_nodes`, 6 more). Both read
`/etc/faas/compute-db.env`, so the plan adds their budgets to every compute
node once any compute host sets the switch. The role keeps the steady fleet
below the 75% warning threshold and fits the declared rollout overlap. It
rounds the resulting postmaster ceiling to ten. With the standalone one-node
overlap, two compute nodes derive 180 connections, ten derive 540, and twelve
derive 640; with the edge and the object gateway on, two derive 210 and twelve
derive 780. A four-worker `join-fleet` batch at ten nodes derives 560 instead.

The role changes only `max_connections` and
`superuser_reserved_connections`, restarts PostgreSQL only when the running
postmaster differs from the target, and verifies the live settings before a
node join continues. The restart decision reads the server, not whether this
run edited the file, so a run that failed after writing the file still
restarts on the next run.
It is included by both `bootstrap.yml` and `node_join_control_plane.yml`. A RAM
guard rejects admission before modifying PostgreSQL when the control-plane
host is too small. The 12-node, one-overlap contract requires about 14.7 GB
and therefore fits a 16 GB database host; with the edge and the object gateway
on compute nodes it requires about 17.9 GB. Higher batch concurrency can require a
larger database host and is rejected before any new compute node is activated.

## Pooled fleets

`faas_pgbouncer_enabled` changes two terms in the derivation and nothing else.
A compute node's ordinary pools no longer reach the postmaster — they
terminate at pgbouncer and share its server pool, which is a constant rather
than a per-node cost. What still scales per node is the session-scoped residue
that cannot be pooled: `LISTEN` and session advisory locks on the direct DSN
(`faas_postgres_per_compute_direct_budget`, 5 database daemons ×
`db.directHubOnMaxConns`, plus imaged's ADR-570 nested traffic guard and
schedd's five prime locks: 16; the edge and the object gateway each add `db.directHubOnMaxConns` where they run).

| Nodes | Unpooled | Pooled |
|---|---|---|
| 2 | 180 conns / 3.9 GB | 240 conns / 5.3 GB |
| 12 | 640 conns / 14.7 GB | 450 conns / 10.2 GB |
| 50 | 2,360 conns / 55.0 GB | 1,260 conns / 29.2 GB |
| 100 | 4,620 conns / 107.9 GB | 2,330 conns / 54.3 GB |

The table leaves out the edge and the object gateway.

**Do not enable the pooler below five compute nodes.** pgbouncer's server pool
is a fixed 80 backends, so on a two-node fleet it costs more than the 36 it
saves after accounting for direct sessions. The crossover is at about 4½ nodes: `34n = 80 + 16n`.

Note what pooling does and does not fix. It takes a 100-node fleet from
impossible to a database host with at least 55,580 MB of RAM. The remaining
per-node cost includes five prime-lock sessions on schedd as well as notification
listeners and short session guards. Reducing that term requires changing the
session-scoped work, rather than adding a transaction pooler.

The per-daemon direct budget is hub-dependent: with `FAAS_DB_NOTIFY_HUB=0`
every subscriber parks its own `LISTEN` connection on the direct pool, so
`db.directMaxConns` falls back to the pre-hub per-daemon sizing. A fleet
running any daemon with the hub disabled needs materially more direct
connections than the table above; re-derive before enabling that mode on more
than one node.

ADR-570 reserves three direct connections for hub-enabled apid when a separate
`FAAS_DATABASE_URL_DIRECT` is configured: the notification hub, outer edge
mutation convergence lock and guarded policy transaction. imaged gets the same
three on every compute node: the hub, its deployment activation lock and
MarkDeploymentLive's guarded policy transaction. schedd reserves seven: its
hub, four background prime locks, one inline prime lock and one short session
operation. The worker concurrency comes from the same API constant as its
direct pool budget. Converge the updated PostgreSQL capacity plan before
rolling out this larger scheduler pool. The deployed pooler role targets
compute nodes, so the control plane keeps the ordinary direct
pool budget above. If the control plane is explicitly given a separate session
pool, include that sibling pool in `faas_postgres_control_plane_pool_budget`;
the default 61 (apid's request and background pools included) assumes the existing control-plane pool topology.

The per-daemon maxima come from `pkg/db.DaemonMaxConnections`, which applies
while the ADR-190 notify hub is active — the supported configuration. Setting
`FAAS_DB_NOTIFY_HUB=0` selects `DaemonMaxConnectionsNotifyHubDisabled`
instead, which keeps the larger pre-hub sizing because every subscriber then
parks its own connection. A fleet running any daemon with the hub disabled
therefore needs more connections than the figures above; re-derive the
ceiling before enabling that mode on more than one node.

`pkg/db.DaemonMinConnections` keeps a small warm floor of each listed
daemon's connections open while idle (schedd: 4 per process, so wakes reuse
backends that have already compiled the instance triggers;
gatewayd-internal: 2, so a route-cache miss reads its host policy on a warm
backend within the 250 ms ADR-570 budget). A floor never exceeds the
daemon's maximum: it raises the idle session count, not the ceiling this
role derives.
