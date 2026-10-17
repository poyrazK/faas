# FaasComputeEdge* — app edge on compute nodes (ADR-1343)

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_compute_edge`. Each compute node runs the app edge when the manifest
sets `public_edge.compute_nodes`:
- **`faas-gatewayd-public-edge`:** gatewayd-public in compute edge mode, on
  `127.0.0.1:8090`, with its control port on 9092.
- **`faas-cloudflared`:** the Cloudflare Tunnel connector, with metrics on 20241.

Prometheus discovers both per node through apid HTTP-SD, as the
`gatewayd-public-edge` and `cloudflared` jobs. These alerts are silent on
fleets without the edge.

| Alert | Severity | Meaning |
|---|---|---|
| `FaasComputeEdgeUnavailable` | page | No edge answers, or no connector holds a tunnel connection, for 2 minutes. App requests through the tunnel fail. |
| `FaasComputeEdgeDown` | warn | One node's edge does not answer. |
| `FaasComputeEdgeConnectorDisconnected` | warn | One node's connector is down or holds no tunnel connection. |
| `FaasComputeEdgeLocalFallback` | warn | One node's edge keeps routing to the compute pool because its own gatewayd-internal is unhealthy. |
| `FaasComputeEdgeMisdirected` | warn | One node's edge keeps answering 421 for hosts outside the apps domain. |

Cloudflare spreads tunnel traffic across the connected connectors, so one
node down costs capacity, not availability. While a node's edge is down but
its connector is still connected, that connector receives requests it
cannot serve.

## Check

From your workstation:

```bash
make check-compute-edges CP_SSH=<user@control-plane>
```

This prints, for each node, whether its edge is ready and its connector is
connected.

On the node:

```bash
systemctl status faas-gatewayd-public-edge faas-cloudflared --no-pager
journalctl -u faas-gatewayd-public-edge -u faas-cloudflared --since -30m --no-pager | tail -60
curl -fsS http://127.0.0.1:9092/readyz
curl -fsS http://127.0.0.1:20241/ready
curl -fsS http://127.0.0.1:9092/metrics | grep gatewayd_public_edge_
```

## Act

- **Edge down on one node.** Restart the edge:
  `systemctl restart faas-gatewayd-public-edge`. If it keeps failing, stop
  that node's connector (`systemctl stop faas-cloudflared`) so Cloudflare
  stops sending it requests, then investigate. Common causes:
  - `FAAS_APPS_DOMAIN` missing from `99-faas-edge.conf`;
  - the compute database DSN in `/etc/faas/compute-db.env`;
  - the release missing the binary.
- **Connector disconnected.** Check `/etc/faas/secrets/cloudflared/tunnel-token`.
  The unit does not start without it. Also check outbound reachability to
  Cloudflare (TCP/UDP 7844) and the tunnel's state in the Cloudflare
  dashboard.
- **Unavailable (page).** Treat it as an app-traffic outage:
  - check whether one cause hits every node: tunnel deleted or token
    revoked, the release, the database;
  - if the control-plane edge still serves the apps domain, move
    `*.<apps domain>` DNS back to it while you fix the compute edges
    (ADR-1343 rollout).
- **Local fallback.** The node's gatewayd-internal is unhealthy. Requests
  still succeed through other nodes, with one extra hop. Fix gatewayd-internal
  on that node (`FaasComputeNode*` runbooks).
- **Misdirected.** The tunnel's ingress rules send hosts other than
  `*.<apps domain>` to the edge. The platform hosts and customer domains
  belong on the control-plane edge, so fix the ingress rule in Cloudflare.
