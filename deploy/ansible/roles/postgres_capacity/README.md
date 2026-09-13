# PostgreSQL public-beta capacity

This role reserves enough direct PostgreSQL connections for the public-beta
topology: one control plane, two active compute nodes, and one overlapping
compute generation during a rolling deployment.

The production daemon pool maxima total 108 sessions at steady state and 142
during rollout overlap. PostgreSQL runs with 160 total connections and five
superuser-reserved connections, leaving 13 ordinary slots during that worst
case and keeping the steady fleet below the 75% warning threshold.

The role changes only `max_connections` and
`superuser_reserved_connections`, restarts PostgreSQL only when either file
setting changes, and verifies the live settings before a node join continues.
It is included by both `bootstrap.yml` and `node_join_control_plane.yml`.

PgBouncer transaction pooling is not used for this two-node gate because the
daemons intentionally hold PostgreSQL `LISTEN` sessions. A future larger fleet
must split notification connections onto a direct DSN before routing query
pools through transaction-mode PgBouncer.
