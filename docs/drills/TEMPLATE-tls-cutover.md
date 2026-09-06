# TLS cutover drill — YYYY-MM-DD (M8 acceptance, issue #252)

> Current topology: Caddy/Cloudflare terminates customer TLS and forwards to
> `gatewayd-public`. Use `make tls-cutover-drill` to generate this record; do
> not hand-edit a PASS result without running the dry-run or execute path.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | |
| Started | |
| Finished | |
| Mode | dry-run / execute |
| Operator | |
| Box | |
| Public endpoint | |
| DNS provider | |
| DNS tool | |
| State file | |
| Run ID | |
| Verdict | **PASS** / **FAIL** |

## Five-step validation matrix

| # | Step | Expected | Result |
|---:|---|---|---|
| 1 | Pre-flight inputs and topology | endpoint, provider, and operator inputs are present | |
| 2 | Validate Caddy and DNS control path | configuration validates and the configured DNS tool is callable | |
| 3 | Cut over and verify public HTTPS | reload succeeds and the public endpoint returns a trusted response | |
| 4 | Rollback and preserve operator state | rollback hook succeeds and state remains readable | |
| 5 | Post-rollback verification | endpoint remains reachable and `/dashboard/admin` retains the banner | |

## Dashboard banner persistence

| Check | Result |
|---|---|
| Active state written before cutover | |
| Rolled-back state written after rollback | |
| State file retained for `/dashboard/admin` | |

## Operator notes

<!-- Keep command output free of tokens, cookies, and certificate private keys. -->
