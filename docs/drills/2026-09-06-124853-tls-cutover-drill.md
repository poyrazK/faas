# TLS cutover drill — 2026-09-06 (M8 acceptance, issue #252)

> Current topology: Caddy/Cloudflare terminates customer TLS and forwards to gatewayd-public.
> This record was produced by `make tls-cutover-drill` in `dry-run` mode.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | 2026-09-06 |
| Started | 2026-09-06T12:48:53Z |
| Finished | 2026-09-06T12:48:53Z |
| Mode | dry-run |
| Operator | poyrazk |
| Box | Mac.home |
| Public endpoint | https://api.gregale.dev |
| DNS provider | cloudflare |
| DNS tool | cloudflare-api |
| State file | /var/lib/faas/tls-cutover.state |
| Run ID | 20260906T124853Z |
| Verdict | **PASS** |

## Five-step validation matrix

| # | Step | Expected | Result |
|---:|---|---|---|
| 1 | Pre-flight inputs and topology | endpoint, provider, and operator inputs are present | PASS |
| 2 | Validate Caddy and DNS control path | configuration validates and the configured DNS tool is callable | PASS |
| 3 | Cut over and verify public HTTPS | reload succeeds and the public endpoint returns a trusted response | PASS |
| 4 | Rollback and preserve operator state | rollback hook succeeds and state remains readable | PASS |
| 5 | Post-rollback verification | endpoint remains reachable and /dashboard/admin retains the banner | PASS |

## Dashboard banner persistence

| Check | Result |
|---|---|
| Active state written before cutover | PASS |
| Rolled-back state written after rollback | PASS |
| State file retained for /dashboard/admin | PASS |

## Operator notes

Dry-run completed without changing DNS, Caddy, or customer traffic. Execute on the reference node after reviewing the hooks.
