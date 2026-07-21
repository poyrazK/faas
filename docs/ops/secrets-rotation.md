# Secrets rotation

One-box FaaS keeps a small, sealed set of secrets on the host. This
doc is the rotation runbook for the ones that have to change on a
recurring cadence (the others — the host age keypair, the apid
session secret — are generated once and only rotate under incident
response, not as scheduled maintenance).

## Hetzner DNS API token

CertMagic uses the Hetzner DNS API to write `_acme-challenge` TXT
records for the wildcard `*.apps.DOMAIN` cert and on-demand
HTTP-01-challenged custom-domain certs. The token is read once at
gatewayd startup from `/etc/faas/secrets/hetzner-dns.token` and held
in process memory for the daemon's lifetime; **token rotation today
requires a `systemctl restart faas-gatewayd`** because the
`loadSecretFile` seam (cmd/gatewayd/secrets.go) is single-shot. A
file-watch reload is a follow-up.

- **Owner:** platform team rotation list, PagerDuty schedule
  `faas-platform-oncall`.
- **Cadence:** 90 days.
- **Storage:** `/etc/faas/secrets/hetzner-dns.token`, mode `0440`,
  owner `root:faas`. The perm check in `cmd/gatewayd/secrets.go`
  refuses to start the daemon if the file is group/other-writable or
  has any exec/setuid/setgid bits.
- **Source of truth:** Hetzner Cloud Console → Project → Security →
  API tokens. The token must have `read` + `write` on the DNS zone
  the wildcard cert is minted under; `read` alone breaks on-demand
  cert issuance.

### Procedure

1. Generate a new token in the Hetzner Cloud Console. Label it
   `gatewayd-prod-YYYY-MM-DD` so the rotation history is auditable.
2. Install it on the EX44:

   ```sh
   sudo install -m 0440 -o root -g faas /dev/stdin \
       /etc/faas/secrets/hetzner-dns.token <<<"$NEW_HETZNER_DNS_TOKEN"
   ```

3. Verify the perm:

   ```sh
   stat -c '%a %U:%G' /etc/faas/secrets/hetzner-dns.token
   # expect: 440 root:faas
   ```

4. Restart gatewayd:

   ```sh
   sudo systemctl restart faas-gatewayd
   sudo journalctl -u faas-gatewayd -f
   # expect: "public listening (TLS) addr=:443" within ~5 s
   ```

5. Revoke the old token in the Hetzner Cloud Console. Revoking before
   restart means a window where the daemon holds a token with no
   write authority; the safest order is install → restart → verify the
   new wildcard mint succeeded → revoke the old one.

6. Record the rotation in `docs/drills/YYYY-MM-DD-hetzner-token-rotation.md`
   (use `docs/drills/2026-07-21-tls-cutover.md` as the format template).
   Include the date, the new token label, the journalctl excerpt from
   step 4, and a `curl -fsSL https://<slug>.apps.DOMAIN/ | head -1`
   output proving customer traffic still serves after the restart.

### Rollback

If the new token breaks DNS-01 (e.g. the token was scoped to the wrong
zone), reinstall the previous token and restart:

```sh
sudo install -m 0440 -o root -g faas /dev/stdin \
    /etc/faas/secrets/hetzner-dns.token <<<"$OLD_HETZNER_DNS_TOKEN"
sudo systemctl restart faas-gatewayd
```

CertMagic leaves any issued certs in `/var/lib/faas/certs/`; they're
inert until the next renewal tick. No customer impact unless the
rollback lands inside a renew window.

### Alerting

A missed rotation surfaces as a token-expiry alert from Hetzner
(recommended) or as customer-facing cert-renewal failures when
CertMagic can no longer write `_acme-challenge` records. The
`gateway_tls_on_demand_denied_total` Prometheus counter (follow-up
metric, ADR-024 H3) is the canary for partial-token failures — a
non-zero value with no matching allowlist change indicates the
token has lost write authority.

## Other secrets

- **`/etc/faas/secrets/host-age.key`** — sealed customer-secret box
  keypair (ADR-020). Rotated only under incident response; the old
  keypair stays valid for 30 days post-rotation so customers can
  re-decrypt in-flight secrets.
- **`apid session secret`** — generated at apid install time, lives in
  apid's TOML. Rotated only if leaked; invalidates every active
  customer session.
- **GitHub App webhook secret** — loaded into gatewayd's env at
  startup (`loadGithubWebhookSecret` in cmd/gatewayd/main.go).
  Rotation cadence: same as the GitHub App's own private key (annual
  or under incident). Restart required.
- **Stripe API key** — lives in meterd's env. Rotation cadence: on
  personnel change or under incident; restart required.
