# TLS cut-over drill — 2026-07-21 (M8 acceptance, ADR-024)

> ⚠️ **PENDING — operator execution required.** This drill template
> was scaffolded as part of the gatewayd Tier-1 PR
> (`worktree-tier1-gatewayd-fix`, Finding 5) but the actual EX44 run
> has not happened yet. The §14 milestone gate is the EX44 execution,
> not this document — issue #252 stays open until the row population
> below is filled in by an operator against a live box. Do NOT mark
> the issue closed from a code-only PR.

## Acceptance bar

> "gatewayd terminates TLS via CertMagic (DNS-01 wildcard +
> on-demand HTTP-01 gated by `custom_domains` allowlist); on-call has a
> runbook; cert-mint abuse vector closes" — ADR-024, spec §11, spec §14 M8.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | 2026-07-21T__:__:__Z |
| Operator | <name> |
| Box | <EX44 id / public IP> |
| Wildcard domain | `*.apps.<zone>` (e.g. `*.apps.gregale.dev`) |
| Cert issuer | Let's Encrypt (prod) |
| Cert `notBefore` | <ISO-8601> |
| Cert `notAfter` | <ISO-8601> |
| Days until expiry | __ (bar = ≥ 60) |
| Verdict | **PASS** / **FAIL** |

## Validation matrix

(Each row is one check from `docs/ops/gatewayd-tls-cutover.md` step 6.
Paste the raw command output under "Result".)

| # | Check | Expected | Result |
|---|-------|----------|--------|
| 1 | Cert subject + issuer (TLS 1.3, `apps.gregale.dev`) | `subject=CN = *.apps.gregale.dev`, `issuer=O = Let's Encrypt` | |
| 2 | TLS 1.2 rejected | `handshake failure` | |
| 3 | Customer HTTPS | `200` after wake | |
| 4 | :80 → :443 redirect | `308` | |
| 5 | ACME path reachable on :80 | `404` (certmagic handler responds) | |
| 6 | Cert-mint abuse vector (attacker SNI) | `handshake failure` (allowlist denies) | |
| 7 | apid status unaffected (loopback) | `200`, SLO JSON unchanged | |
| 8 | Cert expiry | `notAfter=` ≥ 60 days out | |
| 9 | TLS metric scrape (`gateway_tls_*` surfaces on :9090/metrics) | `gateway_tls_cert_expiry_seconds` ≥ 1d, `gateway_tls_cert_expiry_by_host_seconds{hostname,kind}` ≥ 1 row (per-host gauge from Finding 2), `gateway_tls_cert_expiry_refresher_walk_complete_total{result="complete"}` ≥ 1, `gateway_tls_on_demand_denied_total{reason=...}` present (any value, including 0) | |

## journalctl excerpt (first 60 s after `systemctl restart faas-gatewayd`)

```
<paste the first ~60 lines of `journalctl -u faas-gatewayd --since '-60s'`
here; expect to see "public listening (TLS) addr=:443" and "ACME
listening addr=:80" within the window, plus the wildcard mint INFO>
```

## DNS records created (idempotent script output)

```
$ sudo bash deploy/scripts/hetzner-zone-setup.sh \
      --zone example.com \
      --apps-domain apps.gregale.dev \
      --edge-host edge.gregale.dev \
      --host-ip <EX44_IP>

<paste the full script output here; expect three lines:
   A     apps.gregale.dev  -> <IP>
   CNAME edge.gregale.dev  -> apps.gregale.dev.
   TXT   _faas-verify      -> "faas-domain-ok=1">
```

## Rollback tested

| Check | Result |
|---|---|
| `sed -i 's/^disabled = false/disabled = true/' /etc/faas/gatewayd.toml && systemctl restart faas-gatewayd` brings plain `:8080` back | YES / NO |
| Certs left in `/var/lib/faas/certs/` are inert after rollback | YES / NO |
| Re-flip (`disabled = false`) re-enables TLS without manual cert regen | YES / NO |

## Notes / deviations from the runbook

<Anything the operator had to do differently from
`docs/ops/gatewayd-tls-cutover.md` — extra steps, missing dependencies,
unexpected errors. Empty if the runbook was followed verbatim.>

## Follow-ups committed

<PR numbers or follow-up task IDs for anything the cut-over uncovered.
ADR-024's H3 (Prometheus cert-expiry + on-demand-denied metrics) is
closed at cut-over time — row 9 above is the live evidence. H4
(file-watch secret reload) remains the only open ADR-024 follow-up.>
