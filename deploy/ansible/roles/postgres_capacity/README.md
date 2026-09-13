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

PgBouncer transaction pooling is not used because the daemons intentionally
hold PostgreSQL `LISTEN` sessions. Larger fleets should split notification
connections onto a direct DSN before routing ordinary query pools through
transaction-mode PgBouncer.
