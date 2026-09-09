# gatewayd TLS cut-over runbook

> **PR-E note (2026-08-09 — historical archive):** This runbook
> documents the legacy `cmd/gatewayd/` daemon's TLS cut-over. The
> cited unit is `faas-gatewayd.service` (deleted in PR-A); the
> current public ingress is `faas-gatewayd-public.service` (TLS
> terminates upstream at Caddy + Cloudflare per ADR-070 revision
> 2026-08-04). Retained for diff archaeology and pre-PR-A operator
> audit. Do not follow this runbook on a current deployment.
>
> Hostnames using `*.apps.gregale.dev` below are part of that
> historical procedure. The current customer hostname contract is
> `*.gregale.dev`.

Step-by-step operator procedure for flipping a reference node from plain `:8080`
(`[tls].disabled = true`) to TLS on `:443` + ACME on `:80` (`disabled =
false`). Followed by the validation matrix that proves the cut-over is
real, not just the daemon started.

This is the D1 deliverable from ADR-024. The P0 blockers (secrets perm,
certs dir owner) must already be applied to the ansible role before
this runbook is attempted — `git log --oneline -- deploy/ansible/roles/gatewayd_service/`
should show the `fix(gateway): accept 0440 / 0640 secret perms` and
`fix(gateway): certs dir owned by faas:faas 0700` commits on the
branch you deploy.

## Pre-flight

```sh
# Confirm ansible is current with the P0 fixes.
cd /opt/onebox-faas
git log --oneline -5 -- deploy/ansible/roles/gatewayd_service/

# Confirm the Hetzner DNS API token exists with the correct perm.
stat -c '%a %U:%G' /etc/faas/secrets/hetzner-dns.token
# expect: 440 root:faas
test -s /etc/faas/secrets/hetzner-dns.token

# Confirm the certs dir is faas-owned (P0.2).
stat -c '%a %U:%G' /var/lib/faas/certs
# expect: 700 faas:faas

# Confirm the apex NS is delegated to Hetzner (one-time at the registrar).
dig +short NS example.com
# expect: ns1.first-ns.de, ns2.first-ns.de, ... (Hetzner's nameservers)
```

If any of these checks fail, stop. Fix the precondition and re-run.

## Procedure

### 1. Apply the ansible role (idempotent; safe to re-run)

```sh
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-control-plane
```

The role now creates `/var/lib/faas/certs` as `faas:faas 0700` (P0.2
fix). The role does **not** install the token or copy `gatewayd.toml`
— those are operator steps below.

### 2. Copy and edit the gatewayd TOML

```sh
sudo cp /etc/faas/gatewayd.toml.example /etc/faas/gatewayd.toml
sudo $EDITOR /etc/faas/gatewayd.toml
```

Set at minimum:

```toml
apps_domain          = "apps.gregale.dev"
contact_email        = "ops@example.com"   # monitored inbox for expiry warnings

[tls]
disabled             = false                # <-- the flip
wildcard_cert_domain = "apps.gregale.dev"
hetzner_zone         = "example.com"
storage_dir          = "/var/lib/faas/certs"
```

Leave `use_staging_ca = false` for production.

### 3. Install the Hetzner DNS API token

If the file isn't there yet (P0.1 requires `0440` — `0600` is also
accepted but `0440` matches the runbook):

```sh
sudo install -m 0440 -o root -g faas /dev/stdin \
    /etc/faas/secrets/hetzner-dns.token <<<"$HETZNER_DNS_TOKEN"
```

If the file already exists from a prior secret-store bootstrap but with
the wrong perm (`0600` is fine, but `0660` / `0604` would have crashed
the daemon):

```sh
sudo chmod 0440 /etc/faas/secrets/hetzner-dns.token
sudo chown root:faas /etc/faas/secrets/hetzner-dns.token
```

### 4. Bootstrap the DNS zone

```sh
sudo bash deploy/scripts/hetzner-zone-setup.sh \
    --zone example.com \
    --apps-domain apps.gregale.dev \
    --edge-host edge.gregale.dev \
    --host-ip $(curl -fsSL https://api.ipify.org)
```

The script is idempotent: re-running it does not duplicate records.
First run on a brand-new zone needs `--create-zone` (or
`HETZNER_CREATE_ZONE=1`); the apex NS delegation at the registrar is
out of scope for the script and must be done in the registrar UI.

Validate the records propagated before continuing:

```sh
dig +short apps.gregale.dev      # expect: <HOST_IP>
dig +short edge.gregale.dev      # expect: apps.gregale.dev.
```

### 5. Restart gatewayd

```sh
sudo systemctl restart faas-gatewayd
sudo journalctl -u faas-gatewayd -f
```

Expected output (within ~60 s):

```
gatewayd public listening (TLS) addr=:443
gatewayd ACME listening addr=:80
gatewayd control listening addr=127.0.0.1:9090
```

The wildcard mint takes 30-60 s; the journalctl excerpt above appears
once the cert is issued. The wildcard is obtained lazily on the first
inbound request via certmagic's OnDemand path (which short-circuits
eager ManageSync when an OnDemand config is present). If DNS-01 fails
on the first request — typically a Hetzner token perm scope issue —
certmagic returns a 5xx and you see the error in
`journalctl -u faas-gatewayd`; the next inbound request re-triggers
the mint.

### 6. Validation matrix

| # | Check | Command | Expected |
|---|-------|---------|----------|
| 1 | Cert subject + issuer | `openssl s_client -connect apps.gregale.dev:443 -servername apps.gregale.dev </dev/null 2>&1 \| grep -E 'subject=\|issuer='` | `subject=CN = *.apps.gregale.dev`, `issuer=O = Let's Encrypt` |
| 2 | TLS protocol | `openssl s_client -connect apps.gregale.dev:443 -tls1_2 </dev/null 2>&1 \| grep 'handshake failure'` | `handshake failure` (we pinned TLS 1.3) |
| 3 | Customer HTTPS | `curl -fsSL -o /dev/null -w '%{http_code}\n' https://apps.gregale.dev/` | `200` (after wake gate) |
| 4 | :80 → :443 redirect | `curl -fsSL -o /dev/null -w '%{http_code}\n' http://apps.gregale.dev/` | `308` |
| 5 | ACME path reachable | `curl -fsSL -o /dev/null -w '%{http_code}\n' http://apps.gregale.dev/.well-known/acme-challenge/probe` | `404` (certmagic's handler responds; only the token matters) |
| 6 | Cert-mint abuse vector | `openssl s_client -connect apps.gregale.dev:443 -servername attacker.example.com </dev/null 2>&1 \| grep -E 'handshake failure\|verify return code'` | `handshake failure` (allowlist denies → no cert minted) |
| 7 | apid status unaffected | `curl -fsSL http://127.0.0.1:8081/status/slo.json \| jq .` | `200`, SLO JSON unchanged |
| 8 | Cert expiry | `openssl s_client -connect apps.gregale.dev:443 -servername apps.gregale.dev </dev/null 2>&1 \| openssl x509 -noout -dates` | `notAfter=` ≥ 60 days out |

### 7. Record evidence

Copy `docs/drills/2026-07-21-tls-cutover.md` to today's date, fill in
the validation matrix outputs, attach the journalctl excerpt from
step 5. Commit it under `docs/drills/`. The drill template is the
spec §14 M8 evidence for the cut-over.

## Rollback

If anything fails post-cut-over:

```sh
sudo sed -i 's/^disabled = false/disabled = true/' /etc/faas/gatewayd.toml
sudo systemctl restart faas-gatewayd
```

Plain `:8080` returns. CertMagic leaves issued certs in
`/var/lib/faas/certs/` — they're inert until `[tls].disabled = false`
is set again. DNS records stay; the wildcard A and edge CNAME are
harmless when no cert is being minted.

If the daemon is wedged in a crash loop after a partial config change,
revert the TOML from the last-known-good revision (the runbook
recommends keeping a `gatewayd.toml.bak` next to the active file):

```sh
sudo cp /etc/faas/gatewayd.toml.bak /etc/faas/gatewayd.toml
sudo systemctl restart faas-gatewayd
```

## Common failure modes

- **`gatewayd: Hetzner DNS token: ...`** — token file missing or wrong
  perm. Re-check step 3. The `loadSecretFile` perm check refuses
  `0660` (group-writable) and any mode with other-readable bits.

- **`gatewayd: certmagic: gateway: TLS config partial`** — TOML is
  missing one of the four primary fields (wildcard_cert_domain,
  hetzner_zone, storage_dir, contact_email). The error message lists
  which one (see `pkg/gateway/tls.go::TLSConfig.Validate`).

- **Wildcard mint hangs for >90 s** — likely DNS-01 propagation delay
  on the Hetzner zone. The daemon does not block startup; the wildcard
  is obtained lazily on the first inbound request via certmagic's
  OnDemand path. If the first request returns a 5xx, hit
  `https://apps.gregale.dev/` again and watch the journal — each
  request re-triggers the mint attempt.

- **Cert-mint abuse vector test fails** (validation #6 returns a
  cert) — the allowlist isn't wired. Check
  `cmd/gatewayd/main.go:188-199` for the `NewPGAllowlist` injection;
  the `pgStore.DomainByName` lookup must return `state.ErrNotFound`
  for hostnames not in the `custom_domains` table.

## Follow-ups (not in this runbook)

- Hetzner DNS API token rotation cadence: 90 days, see
  `docs/ops/secrets-rotation.md`.
- A follow-up PR adds a file-watch reload to `loadSecretFile` so the
  90-day rotation doesn't require a `systemctl restart`.

## Cut-over evidence — metric scrape (ADR-024 H3)

The TLS observability gap closed before cut-over (see ADR-024 "H3
closed" footer). Operators can confirm both metrics surface on the box:

```sh
curl -fsSL http://127.0.0.1:9090/metrics | grep gateway_tls_
# expect: gateway_tls_cert_expiry_seconds <number>
#         gateway_tls_on_demand_denied_total{reason="allowlist"} 0
#         gateway_tls_on_demand_denied_total{reason="dns01"} 0
#         gateway_tls_on_demand_denied_total{reason="token"} 0
```

A `tls_cert_expiry_seconds` value above 86,400 seconds (1 day) is the
healthy steady state once a cert has been minted. Pre-mint the gauge is
unset (Prometheus reports no series), which the alert rule's
`< 14 * 86400` comparator handles correctly (Prometheus drops
missing series from the rule evaluation, so the comparison
returns false). The `dns01` and
`token` series are pre-instantiated at 0 by design — a frozen-zero there
is the H3.b follow-up signal.
