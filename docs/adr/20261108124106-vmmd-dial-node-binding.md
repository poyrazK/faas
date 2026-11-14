# ADR-20261108124106: vmmd dials name the node they mean

- Status: accepted
- Date: 2026-11-08
- Amends: ADR-20261107180000 (edge security round 8, R8-12),
  ADR-052 (cross-box mTLS), ADR-056 (node registry verifier)

## Context

R8-12 (ADR-20261107180000, left open): every compute node's vmmd leaf
carries the shared `vmmd.faas` SAN, and its CN is the node's
`compute_nodes.name`. schedd, gatewayd-internal and the raw TCP/UDP ingress
dial vmmd at `compute_nodes.target_url` and accept any leaf the internal CA
issued whose CN is *some* registered node (`PGNodeVerifier`). Every compute
node logs in as `faas_compute` and can write `compute_nodes`, so a
compromised node can point another node's `target_url` at itself. schedd
would then send it that node's VM-control calls (create, snapshot, destroy,
egress policy, logs) and the gateways its tenants' HTTP and raw-stream
traffic, and the handshake would pass because the attacker's own leaf names
a registered node.

The node never holds the CA key (`gregalectl pki` runs on the control
plane), so it cannot get a leaf with another node's CN.

## Decision

Every TLS dial to a compute node's vmmd requires the server leaf's CN to
equal that node's `compute_nodes.name` (`wire.ExpectPeerCN`), on top of the
existing chain, SAN, EKU and registry checks:

- schedd: the VMM router (names from the active-node list and
  `RefreshNode` on `compute_node_changed`), the heartbeat probe and the
  instance-stats poller;
- schedd's database scheduler: `<id>.faas`, the CN
  `gregalectl pki issue-bundle --box-role database --cn <id>` issues, with
  connections keyed by node and target so two nodes sharing a target never
  share a connection;
- gatewayd-internal's forwarder and gatewayd-public's raw TCP/UDP ingress:
  `NodeClientCache.WithNodeBinding`, which resolves the node's name and dials
  with it;
- pgproxyd: `<node_id>.faas` from schedd's wake reply (`DatabaseRuntime`
  already carries `node_id` beside `vmmd_target`, so no proto change), with
  one connection per node and target.

A dial with no name to bind fails rather than falling back to the registry
check. Unix-socket targets (single box, the local data socket) carry no TLS
and are unchanged.

A rewritten `target_url` now stops traffic to the victim node (its dials
fail) instead of redirecting it. The compute-node compromise runbook
restores the rows.

## Consequences

- Before release, every active node's vmmd leaf CN must equal its
  `compute_nodes.name`. pkg/renderer issues compute-only vmmd leaves with
  CN `<name>.faas` and vmmd registers the same `.faas` name, so rendered
  fleets match. A node whose vmmd still presents a leaf with CN `vmmd.faas`
  under another name (a leaf issued before node identities, accepted today
  only through a legacy `vmmd.faas` registry row) loses its dials. Check
  each node with
  `openssl x509 -noout -subject -in /etc/faas/tls/vmmd/server.crt` against
  `SELECT name FROM compute_nodes WHERE active`, and reissue mismatched
  leaves with the renderer first.
- The registry writes themselves stay open to `faas_compute` (heartbeats and
  self-registration use them). Moving them behind schedd is a larger change
  and no longer needed to stop the redirect.
- R9-1 (no runbook for the fleet-shared credentials a compute node holds)
  was closed by #6804; this change adds the gateway fleet key, the
  file-sourced origin key and the registry rows to it.
