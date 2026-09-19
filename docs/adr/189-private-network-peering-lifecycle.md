# ADR-189 · Durable private-network peering lifecycle

- **Status:** accepted
- **Date:** 2026-09-19

## Context

ADR-188 established provider-neutral route planning, but the planner had no
durable customer intent. Without a persisted lifecycle, retries could create
duplicate requests, network deletion could strand route state, and operators
could not distinguish a requested peering from a converged one.

## Decision

Expose account-scoped peering intent at
`/v1/networks/{id}/peerings`. A request connects two distinct Gregale-owned
networks in the same region and rejects overlapping IPv4 CIDRs. State stores a
canonical network pair, preventing reverse-order duplicates. Creation returns
`202 Accepted` with `status=pending`; only a later provider-neutral reconciler
may promote it to `ready`, while `error` remains fail-closed with a detail
message. Peering rows block deletion of either referenced network until the
peering is explicitly removed.

The endpoint is dark-launched behind the existing private-network fabric flag.
Schedd's peering worker consumes the durable rows and applies the complete
desired route set through the existing vmmd app-netns attachment update. The
same route overlay is replayed by attachment reconciliation, so a base-CIDR
replay cannot silently withdraw a ready peering while Gregale keeps ownership
of policy and routing.

## Consequences

Customers get an idempotent, inspectable lifecycle and SDK coverage without a
DigitalOcean dependency. Pending and error peerings never imply reachability,
and the canonical pair plus account/region checks make retries safe. The
provider-neutral `PeeringReconciler` consumes these rows, applies a complete
desired route set through an explicit `PeeringRouteApplier`, and promotes rows
only after that apply succeeds. The privileged operation reuses the existing
vmmd attachment RPC and fans out across live nodes, avoiding a second
provider-specific route protocol.

## Rejected alternatives

- Calling a provider-native VPC peering API from the request handler, which
  would couple Gregale's contract to DigitalOcean and make readiness opaque.
- Treating a `202` response as ready, which would permit traffic before both
  network route domains converge.
- Cascading network deletion through peerings, which would leave the opposite
  network with an unexpected route-domain change.
