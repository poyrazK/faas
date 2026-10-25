# ADR-20261022004500: gatewayd-internal private control listener for apid

- **Status:** accepted (owner decision, 2026-10-22, Hunt 12 H12-9)
- **Date:** 2026-10-22
- **Amends:** ADR-093 (apid reaches gatewayd-internal's control listener on
  loopback), ADR-070 (control listener is loopback only)
- **Decision:** On a split control plane, apid calls a private control listener
  on an active compute node instead of the loopback control URL.
  - gatewayd-internal on a compute-only node serves a second control listener
    on `FAAS_GATEWAY_PRIVATE_CONTROL_LISTEN` (`0.0.0.0:9097`). It routes only
    what apid calls across hosts: `/v1/internal/apps/{slug}/…` (routes,
    streaming-cap, mcp-health, mcp-probe, response-check, service-endpoints)
    and the response-check capabilities path. `/metrics`, pprof, quota,
    services and the edge-rule reset stay on the loopback listener.
  - nftables admits the control-plane CIDRs to port 9097 through
    `faas_control_plane_allowed_ports`, as ADR-1343 does for port 9092. No
    other host reaches it.
  - apid with `FAAS_GATEWAYD_CONTROL_FROM_REGISTRY=1` resolves the control URL
    per request: the private host of each active node with a gateway target in
    `compute_nodes`, port 9097, taken in turn. Without it apid keeps
    `FAAS_GATEWAYD_CONTROL_URL` (loopback default), the single-box layout.
  - Ansible sets the apid variable on control-plane hosts and the listener on
    compute nodes with a private data-plane listener.

## Context

Production-us runs apid on gregale-cp and gatewayd-internal only on the
compute nodes. apid's `FAAS_GATEWAYD_CONTROL_URL` defaults to
`http://127.0.0.1:9090`, documented as "same-box is the only supported posture
today", so every call failed with `dial tcp 127.0.0.1:9090: connect:
connection refused`. That broke:

- MCP replica health and protocol qualification: `gregale mcp deploy` never
  promotes on prod (Hunt 11 H11-31; mcp-node and mcp-python on rc.288);
- route observations (ADR-093), route-aware streaming caps and deployment
  response checks (ADR-1728).

Capability discovery still advertised mcp-hosting and route-advisor.

## Consequences

- Any active node can answer: MCP probes and response checks forward to the
  target instance's own node, and streaming-cap rules are the same on every
  node. Route observations are per gateway, so one node answers with its own
  observations only; the Postgres collectors remain the complete source.
- The private listener trusts the network boundary, as the 9092 edge control
  listener does. It carries no customer secrets, and every route revalidates
  app, deployment and instance identity against Postgres.
- A node that is active but unhealthy fails the requests rotated to it; the
  caller's existing unavailable handling applies.
- Rollout order: compute nodes (listener and firewall) before the control
  plane's apid drop-in. Until then apid keeps the loopback behaviour.

## Alternatives

- Relay through the control plane's gatewayd-public over the existing edge
  hop: mixes control routes into the public listener's forwarding rules.
- Mark the features unavailable on split installs: leaves MCP hosting broken.
