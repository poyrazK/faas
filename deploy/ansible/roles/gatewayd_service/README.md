# gatewayd_service ansible role

Installs the systemd unit + example config for `gatewayd` (the edge proxy
and the only public listener on the box). Mirrors the `vmmd_service` role.

## What gets installed

| Path                                  | Mode    | Owner       | Notes |
|---------------------------------------|---------|-------------|-------|
| `/etc/systemd/system/faas-gatewayd.service` | 0644 | root:root   | systemd unit |
| `/etc/faas/gatewayd.toml.example`     | 0640    | root:faas   | config template |
| `/etc/faas/`                          | 0750    | root:faas   | config dir |
| `/etc/faas/secrets/`                  | 0750    | root:faas   | gatewayd-only secrets (Hetzner DNS token) |
| `/var/lib/faas/certs/`                | 0700    | faas:faas   | CertMagic storage (owned by the daemon's user so renewals can write) |

## What this role does NOT do

- **does not provision `/etc/faas/secrets/hetzner-dns.token`** — the operator
  pastes the Hetzner DNS API token with `install -m 0400 -o root -g faas`.
  The daemon refuses to start if the file is group/other-readable or has
  any exec/setuid/setgid bits (see `cmd/gatewayd/secrets.go`).
- **does not copy `gatewayd.toml.example` to `gatewayd.toml`** — that copy
  is a one-line override the operator runs by hand so the role never
  silently ships a config with the wrong apps_domain.
- **does not enable or start the daemon** — production runs the role, then
  `systemctl enable --now faas-gatewayd` once the config + token are in
  place and the operator has validated `curl -fsSL https://<slug>.apps.DOMAIN/`
  round-trips through CertMagic.

## Production enablement checklist

1. `ansible-playbook deploy/ansible/site.yml --tags gatewayd_service` (this role).
2. `cp /etc/faas/gatewayd.toml.example /etc/faas/gatewayd.toml && $EDITOR /etc/faas/gatewayd.toml`
   — set `apps_domain`, `wildcard_cert_domain`, `hetzner_zone`.
3. `install -m 0400 -o root -g faas /dev/stdin /etc/faas/secrets/hetzner-dns.token <<<"$HETZNER_DNS_TOKEN"`
4. `systemctl enable --now faas-gatewayd`.
5. `journalctl -u faas-gatewayd -f` — wait for "public listening (TLS) addr=:443".
6. `curl -fsSL https://<slug>.apps.DOMAIN/` — expect 200 over TLS 1.3.

## Fail-fast contracts

The role does NOT change these — the daemon enforces them at startup:

- empty/missing token → `gatewayd: Hetzner DNS token: …` exit 1.
- token file mode 0660 / 0604 / etc → `ErrInsecureSecretPerms` exit 1.
- TOML `[tls]` table partial → `ErrTLSMisconfigured` exit 1.
- TOML `[tls]` enabled without allowlist injection → unreachable: the
  gatewayd binary hardcodes `NewPGAllowlist` so the allowlist is always
  present in production.