# ADR-024 · CertMagic TLS production cut-over + test closure

- **Status:** accepted (legacy daemon only — revised 2026-08-04)
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-07-21 (revised 2026-08-04)
- **Revised 2026-08-04:** The CertMagic + Hetzner DNS-01 plumbing
  described in this ADR applies to the **legacy `cmd/gatewayd/`
  daemon** only, and to the Tier A7 split's `cmd/gatewayd-public/`
  *before* PR #633. PR #633 (tier-a7 recovery) stripped certmagic +
  Hetzner from `gatewayd-public`: the production deployment
  terminates TLS upstream at Caddy + Cloudflare (`api.gregale.dev`),
  not in the daemon. The cert-magic surface remains in tree for the
  legacy daemon's migration window; PR-C sweeps it together with
  `pkg/gateway/{tls_wire,tls,dns01_hetzner,allowlist,certsync,
  cert_expiry}`.
- **Hostname note (issue #1727):** any `*.apps.gregale.dev` values
  preserved in this legacy-daemon ADR are historical examples. New
  customer URLs use `*.gregale.dev`.
- **Decision:** Ship gatewayd's TLS termination via the already-merged
  CertMagic plumbing (`pkg/gateway/tls*.go`, `dns01_hetzner.go`,
  `allowlist.go`, `acme.go`, `cmd/gatewayd/{main,config,secrets}.go`, the
  systemd unit, the ansible role). No in-house TLS layer is added. The
  reference-node production flip is operator-side only — `Disabled=true → false`
  in `/etc/faas/gatewayd.toml` plus the Hetzner DNS zone bootstrap
  described in `docs/ops/gatewayd-tls-cutover.md`.
- **Why:** Spec §4.1 makes gatewayd the only public listener on the node
  and the only path that terminates TLS for customer traffic. Spec §11
  ship-blocking requires (a) wildcard `*.apps.gregale.dev` via DNS-01 against
  a zone the operator controls and (b) on-demand HTTP-01 certs for
  customer custom_domains gated by the `custom_domains` allowlist so an
  attacker who reaches `:80` cannot mint a cert for an unrelated
  hostname. The plumbing has been merged across multiple M8 PRs but the
  reference node still runs `[tls].disabled = true` (plain `:8080`) — the flip is
  the load-bearing closure of the §11 checklist for gatewayd TLS.
  CertMagic is Caddy's battle-tested core as a library; owning our own
  wake-blocking edge (ADR-007 inline in spec §3) means owning our own TLS
  termination on top of it, not replacing it.
- **Consequences:**
  - `pkg/gateway.NewCertMagicConfig` gains a `DNSProviderFactory` test
    seam (D2.1). Production passes `nil`; tests pass a closure pointing
    at an `httptest.NewServer` Hetzner stub. The closure type is
    explicit in the signature — no hidden package-level var.
  - `pkg/gateway/tls_wire_test.go` (D2.2) pins the wire shape against a
    stubbed Hetzner: 11 unit tests covering happy-path bundle build,
    ManageSync failure tolerance, staging-vs-prod CA selection,
    half-config rejection, allowlist-missing rejection, storage-dir
    creation, **the §11 cert-mint abuse-vector denial**, and the :80
    ACME mux round-trip.
  - `cmd/gatewayd/secrets.go` `allowedSecretPerm` widens to accept
    `0o400 / 0o440 / 0o600 / 0o640` (P0.1). The production token file
    is `0o440 root:faas` because the daemon runs as `faas:faas` and
    needs group-read; the original `0o600` mask refused every file the
    operator was told to provision, which would have blocked the cut-over
    with a misleading "stat secret" error.
  - `deploy/ansible/roles/gatewayd_service/tasks/main.yml` changes
    `/var/lib/faas/certs` from `root:faas 0o700` to `faas:faas 0o700`
    (P0.2). CertMagic runs inside the daemon process; its renew writes
    hit EACCES against a dir owned by `root` that doesn't grant group
    access at the requested mode. The systemd unit already runs as
    `User=faas Group=faas` so the daemon owns the dir outright; the
    `ProtectSystem=strict + ReadWritePaths=/var/lib/faas/certs` carve
    in the unit keeps the read-only `/var` semantics intact.
  - `pkg/gateway/tls_wire.go` switches the issuer construction from a
    struct literal (`&certmagic.ACMEIssuer{...}`) to
    `certmagic.NewACMEIssuer(magic, template)`. The literal form leaves
    `am.config = nil`, which segfaults certmagic inside
    `httphandlers.go:138 → account.go:49` (mutex on a nil config
    pointer) the moment any request lands on `:80/.well-known/...`.
    `NewACMEIssuer` wires the back-pointer and defaults CA / TestCA /
    Email from `DefaultACME` when the template leaves them blank.
  - `pkg/gateway/tls.go::TLSConfig.Validate` reorders its checks so a
    config with the primary fields populated but `OnDemandHTTP01Allowlist
    = nil` returns `ErrTLSAllowlistMissing` (the §11 ship-blocking
    sentinel) instead of the misleading `ErrTLSMisconfigured`. Operators
    who forgot the allowlist now see the right diagnostic.
  - `certmagic.NewCache` requires a non-nil `GetConfigForCert`
    (certmagic v0.25 cache.go:130 panics). The wire uses the standard
    pointer-to-pointer trick: `cache.GetConfigForCert` closes over
    `&magic` and dereferences at call time, after `magic =
    certmagic.New(...)` has been assigned.
  - `pkg/gateway/tls_wire.go::(*TLSBundle).Close` is added as the
    symmetric shutdown seam (D4). certmagic v0.25 has no public Stop
    API; the Close is a marker today, but a future certmagic upgrade
    can wire real shutdown without touching `cmd/gatewayd/main.go`.
    `cmd/gatewayd/main.go:411-419` calls it from the SIGTERM branch.
  - `pkg/gateway/tls_wire_metal_test.go` (D2.5, `//go:build metal`) adds
    `TestMetalCertMagic_StagingE2E` (wildcard mint) and
    `TestMetalCertMagic_OnDemandStaging` (HTTP-01 on-demand mint), both
    against `acme-staging-v02.api.letsencrypt.org` via the real
    Hetzner DNS API. Operator opt-in via `FAAS_RUN_TLS_METAL=1`;
    automatic skip when `HETZNER_DNS_API_TOKEN` or `FAAS_SKIP_METAL_TESTS`
    is set. Wire into `make test-metal` via the existing target — these
    are the metal acceptance evidence for the cut-over.
  - `docs/ops/gatewayd-tls-cutover.md` + `docs/drills/2026-07-21-tls-cutover.md`
    (D1) give the on-call a copy-pasteable runbook: copy the example
    TOML, install the Hetzner DNS token, run the zone-bootstrap script,
    flip `[tls].disabled = false`, validate the handshake, record
    evidence. The rollback is one sed line.
  - `docs/ops/secrets-rotation.md` (D5.H4) commits us to a 90-day
    Hetzner DNS token rotation cadence with a documented owner.
    Rotation today requires `systemctl restart faas-gatewayd` because
    `loadSecretFile` (cmd/gatewayd/secrets.go) is single-shot at
    startup; a file-watch reload is a follow-up.
  - `docs/STATUS.md:197-200` stops claiming `caddyserver/certmagic not
    yet in go.mod` — that stale parenthetical is replaced with a pointer
    to this ADR and the runbook.
- **Rejected alternatives:**
  - **Stock Caddy as a sidecar** in front of gatewayd (ADR-007 inline in
    spec §3). Rejected: the wake-blocking edge logic is owned by
    gatewayd (routing cache, rate limiter, wake gate, proxy); putting a
    separate TLS terminator in front means two listeners, two caches,
    and the wake gate gets a TCP hop. Inline CertMagic inside gatewayd
    keeps the single-public-listener invariant (spec §4.1).
  - **nginx + lua** for TLS termination. Rejected: unmaintainable at
    this scale, no first-class ACME library equivalent to CertMagic,
    and the wake-blocking edge would still need a reverse-proxy hop.
  - **Hand-rolled ACME client** (e.g. `eggsampler/acme`). Rejected:
    re-implementing ACME-issuance + DNS-01 + storage + renew scheduling
    is several person-months of work and the CertMagic dependency is
    already in `go.sum` with no API churn since v0.21. The hand-rolled
    Hetzner DNS provider (in `dns01_hetzner.go`) is the only bespoke
    piece; libdns-hetzner exists but pulls modules whose `go.mod` pins
    an older Go than our toolchain.
  - **Deferring the cut-over** to M9+ on the basis that the daemon runs
    fine on plain `:8080`. Rejected: spec §11 ship-blocking includes
    the cert-mint abuse vector (which the unit suite now closes) and
    the §4.1 single-public-listener invariant assumes TLS is on the
    node, not deferred indefinitely. The reference-node's networking posture
    without TLS is a customer-trust regression.

## H3 closed — 2026-07-26

ADR-024 declared H3 (TLS observability) as a known follow-up:
`gateway_tls_cert_expiry_seconds` + `gateway_tls_on_demand_denied_total`
on the §12 dashboard, with alert rules to prevent a missed renewal from
turning into a customer-facing outage.

H3 closes in this PR (the cert-expiry + on-demand-denial PR):

- **`gateway_tls_cert_expiry_seconds`** (gauge, `pkg/gateway/metrics.go`):
  smallest remaining lifetime across cached leaf certs in
  `cfg.StorageDir`. Ticked every 5 minutes by
  `pkg/gateway.StartCertExpiryRefresher` (new file `cert_expiry.go`).
  Pre-mint state is "no series" — Prometheus drops NaN series and
  the page rule's `<` expression handles a missing series correctly,
  so no false page fires during the boot window before the first mint.
- **`gateway_tls_on_demand_denied_total{reason}`** (counter):
  wire-incremented from `pkg/gateway/tls_wire.go`'s on-demand
  DecisionFunc on the `allowlist` reason. The `dns01` and `token`
  series are pre-instantiated at 0 so the dashboard panel surfaces from
  boot; their eventual wire-incrementation is the ADR-024 H3.b
  follow-up (certmagic's silentZap indirection makes a clean bridge
  non-trivial and out of scope for the cut-over PR).
- **Three alert rules** in `deploy/ansible/roles/prometheus/files/faas.rules.yml`:
  `FaasTLSCertExpiryPage` (<14d, page), `FaasTLSCertExpiryWarn`
  (14-30d, warn), `FaasTLSOnDemandDeniedHigh` (>1/min, warn). Validated
  with `promtool check rules` (17 rules total in the `faas_slo` group
  after this PR).
- **Cut-over drill evidence**: `docs/drills/2026-07-21-tls-cutover.md`
  row 9 — operators curl `127.0.0.1:9090/metrics` and grep
  `gateway_tls_` to confirm both metrics surface.
- **Operator runbook pointer**: `docs/ops/gatewayd-tls-cutover.md` has
  been updated to remove the "follow-up" entry for these metrics and
  add a "Cut-over evidence — metric scrape (ADR-024 H3)" section.

H4 (file-watch secret reload for `loadSecretFile`) remains open.
