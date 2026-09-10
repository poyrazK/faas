# `gregale.dev` gatewayd cut-over runbook

> **PR-E note (2026-08-09 — historical archive):** This runbook
> documents the legacy `cmd/gatewayd/` daemon's production cert
> rotation. The cited unit is `faas-gatewayd.service` (deleted in
> PR-A); the current public ingress is `faas-gatewayd-public.service`
> (TLS terminates upstream at Caddy + Cloudflare per ADR-070
> revision 2026-08-04). Retained for diff archaeology and pre-PR-A
> operator audit. Do not follow this runbook on a current
> deployment.

> Hostnames using `*.apps.gregale.dev` below are part of that
> historical procedure. The current customer hostname contract is
> `*.gregale.dev`.

One-time operator procedure for minting the production wildcard cert
`*.gregale.dev` on a reference control-plane node via DNS-01 against the Hetzner DNS API, and
for replacing the placeholder `apps.example.com` configuration the box
has been running with since M0. Follow the pre-flight checks, then the
numbered procedure; the validation matrix at the bottom proves the
cut-over landed end-to-end, not just that gatewayd started.

This runbook assumes the §11 P0 blockers from
[`gatewayd-tls-cutover.md`](gatewayd-tls-cutover.md) are already in
place — secret-file perm (0400/0440 root:faas), certs dir owner
(`faas:faas` 0700), and the Hetzner DNS API token loader in
`cmd/gatewayd/secrets.go::loadSecretFile`. If `git log --oneline --
deploy/ansible/roles/gatewayd_service/` doesn't show the P0 commits,
stop and finish that work first.

## Pre-flight

```sh
# Confirm the new code is on the box.
cd /opt/onebox-faas
git log --oneline -5 -- cmd/gatewayd pkg/gateway

# Confirm the Hetzner DNS API token exists with the correct perm.
stat -c '%a %U:%G' /etc/faas/secrets/hetzner-dns.token
# expect: 440 root:faas
test -s /etc/faas/secrets/hetzner-dns.token

# Confirm the certs dir is faas-owned (P0.2).
stat -c '%a %U:%G' /var/lib/faas/certs
# expect: 700 faas:faas

# Confirm the apex NS is delegated to Hetzner (one-time at the registrar).
dig +short NS gregale.dev
# expect: ns1.first-ns.de, ns2.first-ns.de, ... (Hetzner's nameservers)
```

If any of these checks fail, stop. Fix the precondition and re-run.

## Procedure

### 1. Provision `gregale.dev` in Hetzner DNS

Once-only, at the registrar: delegate the apex NS to Hetzner (their
DNS API manages the zone from there). Then in the Hetzner DNS console
(or via the `deploy/scripts/hetzner-zone-setup.sh` helper):

```sh
deploy/scripts/hetzner-zone-setup.sh \
  --zone gregale.dev \
  --apps-domain apps.gregale.dev \
  --edge-host edge.gregale.dev \
  --host-ip "$REFERENCE_NODE_PUBLIC_IP"
```

This creates the Hetzner zone, the `A apps.gregale.dev -> reference node` record
that the wildcard cert rides on, and the `CNAME edge.gregale.dev ->
apps.gregale.dev` record that customer custom-domains point at
(per spec §11).

### 2. Scope the Hetzner DNS API token to `gregale.dev`

Generate a fresh token in the Hetzner DNS console, scoped to the
`gregale.dev` zone only. Write it to the path gatewayd already reads:

```sh
umask 077
install -m 0440 -o root -g faas /dev/stdin \
  /etc/faas/secrets/hetzner-dns.token <<<"$HETZNER_DNS_TOKEN"
```

The `loadSecretFile` perm check in `cmd/gatewayd/secrets.go` accepts
0400/0440/0600/0640 and refuses to start otherwise — a wider perm
fails the daemon at boot.

### 3. Copy the example TOML and fill the production fields

```sh
cp /etc/faas/gatewayd.toml.example /etc/faas/gatewayd.toml
chown root:faas /etc/faas/gatewayd.toml
chmod 0640 /etc/faas/gatewayd.toml
$EDITOR /etc/faas/gatewayd.toml
```

The fields that need real values:

```toml
apps_domain              = "apps.gregale.dev"
wildcard_cert_domain     = "apps.gregale.dev"
hetzner_zone             = "gregale.dev"
contact_email            = "ops@gregale.dev"   # for Let's Encrypt expiry warnings
```

Leave `disabled = false`. The example file ships with `example.com`
comments — overwrite them.

### 4. Restart gatewayd and watch the DNS-01 mint

```sh
systemctl restart faas-gatewayd
journalctl -fu faas-gatewayd | grep -iE 'cert|acme|hetzner'
```

You should see, in order:

1. `gatewayd: TLS enabled; certmagic manager starting`
2. `hetzner dns: zone lookup "gregale.dev" -> <zone-id>`
3. `certmagic: presenting challenge _acme-challenge.apps.gregale.dev`
4. `hetzner dns: create record _acme-challenge TXT <token>`
5. `certmagic: cert obtained for *.gregale.dev`
6. `gatewayd: listening on :443 (TLS) and :80 (ACME)`

The full mint usually takes 30–90 s. If step 5 never lands, check
that the Hetzner DNS API token has `write` scope on the zone (read-only
lets step 2 work but blocks step 4).

### 5. Smoke test

```sh
# Status page serves over the real cert.
curl -vI https://apps.gregale.dev/status
# expect: HTTP/2 200, subjectAltName: DNS:gregale.dev DNS:*.gregale.dev,
#         issuer: Let's Encrypt (prod, not staging)

# Wildcard resolves for an app slug (after a deploy).
curl -vI https://my-app.apps.gregale.dev/
# expect: HTTP/2 200 or 502 (cold), but the TLS handshake is for the
#         wildcard SAN — proves the cert covers <slug>.apps.gregale.dev.

# On-demand HTTP-01 mints for a verified custom_domain row.
# (Requires a customer to have added the domain via `gregale domains add`
#  and verified the _faas-verify TXT record.)
gregale domains add my-app shop.acme.com
# Operator-side: `dig TXT _faas-verify.shop.acme.com` confirms verification,
# then gatewayd mints the cert on first hit.
curl -vI https://shop.acme.com/
# expect: HTTP/2 200 + a cert whose CN is shop.acme.com (not the wildcard).

# DNS-01 round-trip during a re-issue.
dig TXT _acme-challenge.apps.gregale.dev @dns.hetzner.com +short
# expect: one TXT record carrying the ACME token, present during the
#         renewal window, gone after. Proves the Hetzner solver wired.

# Note: the `@dns.hetzner.com` literal is Hetzner-specific. Spec §4
# ADR-007 lists the DNS provider as "provider-pluggable; the reference
# deploy uses Hetzner DNS" — substitute your provider's authoritative
# resolver (e.g. Route 53, Cloudflare, Gandi) when running this runbook
# against a non-Hetzner deploy. The TXT record itself will resolve
# globally via the NS delegation set up in step 1; the per-provider
# resolver is just a faster probe for the `_acme-challenge` row that
# the CertMagic solver just wrote.
```

### 6. Rollback

If the cert mint fails or the wildcard isn't trusted by a client,
flip back to plain HTTP in 30 seconds:

```sh
# Edit /etc/faas/gatewayd.toml:
#   [tls]
#   disabled = true
systemctl restart faas-gatewayd
# gatewayd now binds :8080 plain HTTP (the legacy e2e harness path).
# The cert file remains on disk; CertMagic will retry on the next
# `disabled = false` flip.
```

The smoke test from step 5 still works on `:8080` if you tunnel the
reference-node port over SSH, but no customer traffic — the public listener
on `:443` is off, so customers see a connection refused until you
flip back.

## Validation matrix

| Check                          | Expected                                                  | Where to look                                |
|--------------------------------|-----------------------------------------------------------|----------------------------------------------|
| Daemon up                      | `active (running)`                                        | `systemctl status faas-gatewayd`             |
| Cert file on disk              | `*.gregale.dev.crt`, owned by `faas:faas 0600`            | `ls -la /var/lib/faas/certs`                 |
| Cert chain trusted             | Let's Encrypt prod (not staging)                          | `openssl s_client -connect apps.gregale.dev:443 -servername apps.gregale.dev < /dev/null` |
| DNS-01 round-trip              | TXT record appears + disappears during renewal            | `watch dig TXT _acme-challenge.apps.gregale.dev @dns.hetzner.com +short` |
| App routing via wildcard       | `<slug>.apps.gregale.dev` resolves and serves a wake       | deploy a test app, `curl https://<slug>.apps.gregale.dev/` |
| Custom-domain HTTP-01 mint     | cert mints for the verified domain only                   | `gregale domains add` + `journalctl -u faas-gatewayd` |
| Status page served             | `https://apps.gregale.dev/status` returns the HTML        | `curl -fsS https://apps.gregale.dev/status`  |
| Alert rules scrape cert expiry | `gateway_tls_cert_expiry_by_host_seconds{hostname="*.gregale.dev",kind="wildcard"} > 30d` | Prometheus `/api/v1/query?query=gateway_tls_cert_expiry_by_host_seconds` |

Note: the runtime metric is `gateway_tls_cert_expiry_seconds` (aggregate
gauge) and `gateway_tls_cert_expiry_by_host_seconds` (per-host gauge
from `pkg/gateway/hostname_label_set.go`), both defined in
`pkg/gateway/metrics.go:397` and `:414`. The cert host surfaces as a
`hostname` label, so the data is already keyed by `*.gregale.dev` —
only the metric *name* starts with `gateway_` rather than `faas_`.
The Gregale rename pass intentionally does not touch this family in
the same PR; renaming would orphan dashboards + page rules that key
off `gateway_tls_*` (see `docs/adr/024-certmagic-cutover.md` §H3 and
the per-metric assertion in `pkg/gateway/metrics_test.go:339`). A
follow-up ops-decomposition PR can re-key the alert rules + Grafana
panels to a fully `gregale_` prefix once the rename is non-disruptive.

If any check fails, the cut-over is not complete — do not announce
the new domain to customers until the matrix is green.
