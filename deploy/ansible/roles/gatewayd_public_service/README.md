# gatewayd_public_service ansible role

Installs the systemd unit for `gatewayd-public`, the plain-HTTP
edge daemon introduced by the Tier A7 split (ADR-070). TLS
terminates at Caddy + Cloudflare upstream (`api.gregale.dev`); this
daemon forwards plaintext requests to `gatewayd-internal` over
`/run/faas/gatewayd-internal.sock`.

## What this role does

1. Drops `/etc/systemd/system/faas-gatewayd-public.service` and
   runs `systemctl daemon-reload`.
2. Renders `/etc/faas/tcpd.env`. Raw TCP ingress stays disabled unless
   `faas_tcpd_enabled: true` is set explicitly.

## What this role does NOT do

- Provision any certmagic storage (`/var/lib/faas/certs` is removed;
  TLS is upstream).
- Provision an ACME account directory (`/var/lib/faas/ca` is
  removed; no DNS-01 here).
- Provision `FAAS_HETZNER_DNS_TOKEN_PATH` (no DNS-01 here).
- Provision `FAAS_NODE_ID` / `FAAS_NODE_NAME` env vars (operator
  drop-in at `/etc/systemd/system/faas-gatewayd-public.service.d/`
  if ever needed).
- Enable the unit (`systemctl enable --now faas-gatewayd-public`).
- Run the daemon.
- Configure the upstream Caddy reverse-proxy to
  `http://127.0.0.1:8080` (Caddy is provisioned by the operator or
  by a separate role; see `docs/ops/gatewayd-caddy-upstream.md`).
- Open the raw-TCP listener range by itself. Set
  `faas_tcpd_allowed_cidrs` and the matching `faas_tcpd_enabled` under the
  nftables role; an enabled daemon with no allowed CIDRs remains unreachable.

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to gatewayd-public (env-only — no `config.go`).
- `99-faas-role.conf.j2` — wires the per-box role gate through to
  gatewayd-public via `FAAS_GATEWAYD_PUBLIC_ROLE` so
  `cmd/gatewayd-public/config.go::LoadConfig` picks the right
  `role.FromConfig` sentinel. Without this drop-in gatewayd-public falls
  back to `RoleSingleBox` on a multi-host fleet and the per-daemon role
  gate is unenforced.
- `tcpd.env.j2` — opt-in runtime contract for durable TCP listeners. The
  default public range is 40000–49999; TLS paths and limits are optional.
  `faas_tcpd_idle_timeout` bounds quiet sessions and
  `faas_tcpd_max_connections_per_account` bounds concurrent sessions per
  account on each gateway.

## Note on the public edge

gatewayd-public is the ONLY public listener on a node (Tier A7 split,
ADR-070). Cross-node HA is achieved by having N nodes each with one
gatewayd-public in front of their local gatewayd-internal set — NOT by
having multiple public listeners on one node. The role's per-box role
drop-in is what makes the multi-host handshake layer know which box it
sits on.

## See also

- `docs/adr/068-tier-a7-edge-split.md`
- `deploy/systemd/faas-gatewayd-public.service`
- `cmd/gatewayd-public/main.go`
