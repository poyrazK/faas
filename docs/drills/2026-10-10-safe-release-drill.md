# Safe-release drill — 2026-10-10 (ADR-911)

## Acceptance bar

ADR-911 §Rollout lists four drills before progressive rollouts may claim more
than `preview`: a bad release aborts and restores its predecessor, a
low-traffic release reaches 100%, a defaulted deploy falls back when the
canary worker is down, and rollback recovery is at most 60 s p95. The drill
also covers the crash-loop gate added to ADR-911.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | 2026-10-10 |
| Host | `gregale-internal-test-1` (n2-highmem-2, nested KVM) |
| Test | `cmd/e2e/safe_release_drill_metal_test.go` `TestSafeReleaseDrillMetal` |
| Branch | `feat/safe-releases-by-default` |
| Signals | real apid, meterd canary progression, schedd, vmmd, gatewayd-internal; breaker PromQL answered from schedd's and the gateway's real `/metrics` |
| Verdict | **PASS** — all six drills after the rollback fix (finding 1) |
| Final full run | run 10 at `ab57a4b03`: `TestSafeReleaseDrillMetal` PASS in 940 s (default 71 s, low-traffic advance 350 s + rollback recovery 8.7 s, bad release 111 s, crash loop 127 s, worker fallback 108 s) |

| Drill | Result | Evidence |
|---|---|---|
| Flag-free release of a live app gets the safe default | PASS | second deploy stamped `balanced` at 1% with `rollback_on_5xx` (runs 4–6) |
| Clean low-traffic canary advances past the bound | PASS | no traffic; `1@30s` stage reached 100% in 336–356 s (30 s stage + 5 min bound + 30 s ticks), runs 4–6 |
| Bad release aborts and restores the predecessor | PASS | `/api` 5xx at a 50% canary; breaker input candidate 26/26 5xx vs stable 0/25; abort `circuit breaker: 5xx error rate regression; restoring predecessor`, predecessor back to 100% (run 8) |
| Crash-looping release aborts on liveness restarts | PASS | `/livez` failing, `/` healthy; abort `circuit breaker: crash loop (repeated liveness restarts); restoring predecessor` (runs 5, 6) |
| Defaulted release falls back when the canary worker is down | PASS | meterd killed, lease expired; flag-free deploy went live at 100% with no canary and `rollback_on_5xx=true` (runs 4–6) |
| Rollback recovery ≤ 60 s | PASS | before the fix the target went `snapshotting` → `superseded` and never served (run 6); with `ab57a4b03` the predecessor served 100% again **8.3 s** after `POST /v1/apps/{slug}/rollback` (run 9) |

## Findings

1. **Plain rollback silently no-ops for image, GitHub, and preview
   deployments (product bug).** `PrepareDeploymentRollback` moves the target
   to `snapshotting` and primes it; imaged then promotes it through
   `MarkDeploymentLiveIfLatest` unless a checked-rollback operation exists
   (`pkg/imaged/handler.go`). An older revision is never the latest, so the
   fence supersedes it and returns `ErrDeploymentSuperseded`, which imaged
   treats as a normal outcome. The ADR-625 first-wake 5xx auto-rollback uses
   the same `rollbackAppCore` path, so it records an auto-rollback without
   restoring traffic. GitHub and preview kinds were fenced before; #4311
   (2026-10-08) added image. Canary aborts use rollout recovery and are not
   affected. Fixed in this branch: `deployments.rollback_prepared_at` marks a
   prepared rollback target and imaged promotes it unfenced (ADR-911
   §Plain rollback is promoted unfenced); rollback recovery is re-run below.
2. **A defaulted canary requires imaged's hosting smoke verifier.** imaged
   requires the post-readiness smoke for every canary deployment; a host
   without `FAAS_API_HOSTING_SMOKE_URL` fails it. Production compute nodes
   configure it. Recorded in ADR-911 and the runbook.
3. **The post-readiness smoke rejects a release whose `/` returns 5xx**
   before it serves any canary traffic, so the bad-release fixture fails
   `/api` instead.
4. **Harness gaps fixed in this branch:** prebuilt hello-server override,
   scrapeable schedd metrics port, apid signing key for the rollback
   precheck, request telemetry opt-in, Hobby plan (Pro's public auth default
   is `bearer`).
5. **Harness teardown leaves one or two Firecracker VMs per run.** The node
   run script now records jail ids before the test and removes only the ones
   the run created.
