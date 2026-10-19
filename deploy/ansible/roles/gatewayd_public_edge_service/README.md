# gatewayd_public_edge_service

The app edge on a compute node (ADR-1343): a Cloudflare Tunnel connector
(`faas-cloudflared`) in front of gatewayd-public in compute edge mode
(`faas-gatewayd-public-edge`). The edge serves `*.<apps domain>` and sends
requests to this node's gatewayd-internal, or to the compute pool while that
one is unhealthy. Public object reads, uploads and the OTLP endpoint go to the
control plane's private gatewayd-public listener.

The role runs in the compute play of `bootstrap.yml`. It is off unless the
manifest sets `public_edge.compute_nodes: true`. Turning it off again stops
and removes both units, their drop-ins and the firewall rules.

## Operator steps per cell

1. In Cloudflare, create one remotely managed tunnel for the cell. Give it
   the public hostname `*.<apps domain>` with service
   `http://127.0.0.1:8090`, and a catch-all rule that returns 404. Every
   compute node runs a replica of this same tunnel.
2. On each compute node, stage the tunnel token as a root-only file:

   ```sh
   install -d -m 0700 /etc/faas/secrets/cloudflared
   install -m 0600 /dev/stdin /etc/faas/secrets/cloudflared/tunnel-token   # paste the token, then Ctrl-D
   systemctl start faas-cloudflared.service
   ```

   The connector reads the token through systemd `LoadCredential=`, so the
   token is never on its command line or in its environment. Until the file
   exists the connector stays stopped (`ConditionPathExists=`) and no traffic
   arrives. The edge itself still listens on loopback.
3. Point `*.<apps domain>` at the tunnel only after the pilot hostname
   passes (ADR-1343 rollout step 6).

## Checks

- `systemctl status faas-gatewayd-public-edge faas-cloudflared`
- `curl -fsS http://127.0.0.1:9092/readyz` (the edge's readiness)
- `curl -fsS http://127.0.0.1:20241/ready` (the connector holds a tunnel
  connection)

The connector binary is pinned by version and SHA-256 in `defaults/main.yml`.
To upgrade it, change both values together and take them from the
cloudflared release notes.
