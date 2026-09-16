# Secrets rotation

Gregale keeps a small, sealed set of provider secrets on each host. This
doc is the rotation runbook for the ones that have to change on a
recurring cadence (the others — the host age keypair, the apid
session secret — are generated once and only rotate under incident
response, not as scheduled maintenance).

## DNS provider credentials

Public TLS is terminated by the upstream Caddy/Cloudflare edge. Gregale's
supported `gatewayd-public` and `gatewayd-internal` units do not mint public
certificates or read a DNS API token, so there is no Gregale DNS-token rotation
step for a normal deployment. Rotate the credential in the upstream edge's
secret manager and follow that provider's validation procedure.

The `FAAS_TLS_DNS_PROVIDER` / `FAAS_TLS_DNS_TOKEN` variables and the
CertMagic runbooks under `docs/runbooks/FaasTLS*` are retained only for legacy
daemon or acceptance-harness deployments. Do not add them to the production
split-box units; if a legacy daemon is intentionally enabled, use the
provider-owned secret file and restart that legacy unit after rotation.

## Other secrets

### Repairing a missing database fingerprint

If a node already has a valid `/etc/faas/secrets/host.age` but its
`compute_nodes.host_certificate` or `cert_fingerprint` columns are empty,
use the non-destructive repair leaf:

```sh
sudo gregalectl secrets stamp \
  --host <compute_nodes.name> \
  --pg-dsn "$FAAS_PG_DSN"
```

`secrets stamp` reads the existing vmmd server certificate, derives the
canonical `sha256:` DER fingerprint, and updates only the two database audit
columns. It never regenerates or overwrites `host.age` or the certificate; do
not use `secrets init --force` for this repair.

- **`/etc/faas/secrets/host.age`** — sealed customer-secret box
  keypair (ADR-020, ADR-057). Rotated only under incident response
  or compliance cadence; the old keypair stays valid for 30 days
  post-rotation so daemons can re-decrypt in-flight envelopes
  during the overlap window. See
  [`host-age-rotation.md`](host-age-rotation.md) for the full
  runbook (`gregalectl host-age rotate --commit` →
  bounce daemons → `gregalectl host-age prune-previous` after 30
  days).
- **`apid session secret`** — generated at apid install time, lives in
  apid's TOML. Rotated only if leaked; invalidates every active
  customer session.
- **GitHub App webhook secret** — loaded into `githubd` and
  `gatewayd-internal` environment files at startup.
  Rotation cadence: same as the GitHub App's own private key (annual
  or under incident). Restart required.
- **Stripe API key (legacy compatibility provider)** — lives in meterd's env.
  Rotation cadence: on personnel change or under incident; restart required.
- **Polar Billing (`FAAS_POLAR_ACCESS_TOKEN`, `FAAS_POLAR_WEBHOOK_SECRET`)** —
  lives in `sealed.env` on every node (read by `apid` + `meterd` via
  systemd `EnvironmentFile=`). The TOML equivalent
  (`[billing.polar]` in `apid.toml` / `meterd.toml`) covers
  containerized deploys; the loader's `ApplyBillingEnvOverlay` makes
  **env win over TOML** when both are set
  (`pkg/billing/loader/config.go:157-172`). Rotation cadence: monthly
  under scheduled maintenance, immediately on personnel change. The
  full procedure — including the post-restart `gregale billing status`
  check and the "send a Polar test event from the dashboard" smoke
  test — lives in [`billing-provider-switch.md`](billing-provider-switch.md).
  `make verify-secrets` fails the playbook if Polar is selected (or the
  selector is unset) without `FAAS_POLAR_ACCESS_TOKEN`.
