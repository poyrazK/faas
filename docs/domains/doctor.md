# Domain doctor

`gregale domains doctor <domain>` runs the 5-check Render-style
doctor on a custom domain binding. It exists to close the
custom-domain activation drop-off — Render-style competitors
treat DNS verification + automatic TLS + a doctor report as core
product behavior, and Gregale had only 1.5 of 5 checks visible
to the customer before ADR-120.

## What it checks

The doctor runs five probes against the domain in parallel:

| Check | What it verifies | Failure means |
|---|---|---|
| `dns_record_found` | Apex has A or AAAA records | The apex is unconfigured — DNS isn't pointing anywhere |
| `points_to_gregale` | Apex or CNAME matches `apps.gregale.dev` | The customer is pointing at the wrong host |
| `tls_certificate` | Cert state is `issued` (or `pending` for new bindings) | Cert engine hasn't minted, or port-443 returns a CDN cert |
| `caa_permits` | No `0 issue ";"` blocking CA issuance (RFC 8659) | CAA policy forbids the issuing CA |
| `ipv6_conflict` | Apex AAAA doesn't mismatch the CNAME target | A stray AAAA at the apex is splitting traffic to the wrong host |

Each check returns one of:

- `ok` — check passed
- `fail` — check failed; the `remediation` field has the exact record to change
- `pending` — check couldn't run (DNS lookup timeout, cert engine has not yet issued, etc.)
- `na` — check not applicable for this domain

A domain is `healthy` only when every applicable check is `ok` or `na`.

## CLI

```
$ gregale domains doctor api.example.com
Domain:      api.example.com
AppID:       abc123abc123abc123abc123abc12345
Status:      1 of 5 checks failing
Observed at: 2026-08-18T14:23:11Z

✓ dns_record_found       A and AAAA records present
✓ points_to_gregale      CNAME → apps.gregale.dev
✗ tls_certificate        pending (cert engine has not yet issued)
✓ caa_permits            no CAA published (allowed by default)
✓ ipv6_conflict          no stray AAAA at apex

Fix:
  - Wait for the cert engine — it retries every 30s and usually
    resolves in <2 minutes once DNS is green. If it stays pending
    past 10 minutes, run `gregale domains show api.example.com`
    for cert_not_after + SANs.
```

The CLI exits 0 iff the report's `healthy` is true. Scripts can
branch on the exit code.

### `--json`

```
$ gregale domains doctor api.example.com --json
{
  "domain": "api.example.com",
  "app_id": "abc123abc123abc123abc123abc12345",
  "observed_at": "2026-08-18T14:23:11Z",
  "healthy": true,
  "checks": [
    {
      "name": "dns_record_found",
      "status": "ok",
      "detail": "A and AAAA records present",
      "observed": "1.2.3.4",
      "checked_at": "2026-08-18T14:23:11Z"
    },
    ...
  ]
}
```

Stable `name` tokens (`dns_record` / `points_to_gregale` /
`tls_certificate` / `caa_permits` / `ipv6_conflict`) let scripts
filter without parsing the human `detail` field.

## How the data is sourced

The doctor reads a row from the `domain_doctor_observations`
table (ADR-120). The `dns_poller` writes a fresh row every 30s
for every domain on the account — both the legacy `custom_domains`
surface and the go-forward ADR-100 `tenant_surfaces` /
`tenant_hostnames` surface. The handler reads the latest row per
domain; if the row is older than `FAAS_DOMAIN_DOCTOR_TTL_SECONDS`
(default 300), the handler triggers a synchronous re-probe with
a 5s budget and returns the refreshed report with
`stale: true` set.

The cert state in the observation comes from the `tenant_surfaces`
row when available (the cert engine is the SOLE writer of
`tenant_surfaces.cert_state`; the doctor never writes it). For
legacy `custom_domains` rows with no surface, the doctor dials
port-443 live to read the cert SANs.

For a previously verified legacy domain, a definite
`points_to_gregale=fail` result revokes `verified_at`, stores
`cert_status=dns_drifted`, and emits one `domain.drifted` audit event per
drift episode. The existing 30-second doctor cadence is stricter than the
daily recheck requirement, so the dashboard warning normally appears well
within 24 hours. Publishing the TXT challenge again restores verification.

## Operator controls

| Env var | Default | Effect |
|---|---|---|
| `FAAS_DOMAIN_DOCTOR_ENABLED` | off | Enables the dns_poller's doctor pass. Off by default for safe rollout; turn on after the migration lands on every region. |
| `FAAS_DOMAIN_DOCTOR_TTL_SECONDS` | 300 | The age at which the handler treats the cached row as stale and re-probes synchronously. |

## SDK

```go
report, err := client.DomainDoctor(ctx, "api.example.com")
if err != nil { return err }
for _, c := range report.Checks {
    if c.Status == "fail" {
        log.Printf("doctor failure: %s — %s (fix: %s)",
            c.Name, c.Detail, c.Remediation)
    }
}
```

Node SDK:

```ts
const report = await client.domains.domainDoctor({ domain: 'api.example.com' })
report.checks
  .filter((c) => c.status === 'fail')
  .forEach((c) => console.log(`${c.name}: ${c.remediation}`))
```

Python SDK:

```python
report = client.domain_doctor.sync(domain='api.example.com')
for c in report.checks:
    if c.status == 'fail':
        print(f"{c.name}: {c.remediation}")
```

## Related

- `gregale domains verify <domain>` — single-shot POST that re-runs the DNS walk + cert dial and surfaces the result as a `CustomDomainResponse`. Idempotent.
- `gregale domains show <domain>` — durable row + the live cert chain (NotAfter, SANs).
- [ADR-120](../adr/120-domain-doctor.md) — the design + rejected alternatives.
- [Implementation spec](../faas_implementation_spec.md) §7 (Custom Domains) — the wider domain-binding surface.
