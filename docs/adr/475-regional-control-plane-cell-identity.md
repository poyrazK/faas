# ADR-475: Regional control-plane cell identity

Date: 2026-10-03
Status: Accepted (implementation and qualification tracked below)

## Context

ADR-465 implements compute placement within one shared PostgreSQL control
plane. Independent regional control planes need separate databases before
they can survive each other's database failures. A hostname or DSN alone
does not identify that boundary: a copied environment file can connect an
entire region to another region's otherwise healthy database.

## Decision

A cell is one independently operated control plane and its compute fleet,
with an immutable UUID and canonical compute region stored in its database.
An optional `control_plane_cell: {id, region}` deployment-manifest section
requires every host to declare that region. The renderer supplies the
expected pair to every daemon through `FAAS_CONTROL_PLANE_CELL_ID` and
`FAAS_CONTROL_PLANE_CELL_REGION`.
The manifest Ansible generator supplies the same pair in host variables;
a shared bootstrap role installs identity drop-ins for the canonical daemon
set before service installation. Unbound inventories explicitly clear stale
cell values. The separate Goose SQL connection retains the daemon pool's
identity hooks.

An operator initializes a **fresh, migrated database**, before starting any
daemons, with `gregalectl cell init --manifest-file PATH` and an explicit
`DATABASE_URL`. Initialization is atomic and idempotent for the same pair;
it refuses a different identity and refuses existing accounts, apps, projects,
jobs, instances, or adopted compute nodes. It removes only the unused
migration-seeded `default-local` node. This command provisions platform
identity, never rewrites customer intent or partitions an existing fleet.

All shared database open paths, including read-only and direct session pools,
check cell identity on each new connection. A bound database refuses a
process without an expected identity. A configured process also verifies
identity on every pool checkout, including reused connections through a
transaction pooler. Failed checks return before a connection reaches a
caller. The extra read on configured cells is an intentional correctness
cost; ordinary unbound fleets retain their existing checkout path.

Database triggers reject unlabelled or foreign-region compute nodes and
explicit foreign-region app, project, and job intent in a bound cell.
The store maps that rejection to HTTP 422 `compute_region_outside_cell`,
distinct from an unknown registry entry or temporary capacity failure.
Omitted compute selection keeps its existing API meaning: any eligible
node in this cell. Blank intent is not silently rewritten. All registered
nodes in that database must belong to its region. Registry records can still
describe other regions, but cannot admit foreign workloads into the cell.
Identity cannot be updated, deleted, truncated, or downgraded while bound.

Fresh cell initialization uses an explicit operator database connection;
production daemon open paths never expose a bootstrap bypass. Existing
unbound databases and manifests remain supported. Operators must stop
unconfigured processes before initialization. Superuser DDL and deliberate
backup cloning are operator trust boundaries: clones retain the original
identity and must not run concurrently as a different cell.

## Consequences and remaining scope

Two fresh cells can run the existing daemons against independent databases
and regional manifests. This creates a database wiring and placement
boundary; it does not by itself prove regional fault isolation or residency.
Each cell also needs independent storage, credentials, networking, DNS, and
operational recovery. Global account/app identity, multi-target deployment,
geographic ingress, data replication, and native fleet recovery acceptance
remain separate work. Existing shared fleets require an explicit migration
design rather than rebinding their populated database.

## Validation

Use two private PostgreSQL databases to check independent identities, wrong
ordinary/direct DSNs, missing and partial configuration, reused and reopened
connections, empty-only initialization, concurrent initialization, immutable
identity, node/workload guards, and unchanged unbound behavior. Check the
manifest validator and every rendered daemon unit. These checks need Linux
and PostgreSQL but do not exercise VM lifecycle; ADR-465 native recovery
qualification remains outstanding.
