# Safe-release drill — 2026-10-10 (ADR-1446)

## Acceptance bar

ADR-1446 §Rollout lists four drills before progressive rollouts may claim more
than `preview`: a bad release aborts and restores its predecessor, a
low-traffic release reaches 100%, a defaulted deploy falls back when the
canary worker is down, and rollback recovery is at most 60 s p95. With
ADR-847 probes as quiet-canary evidence, a low-traffic release advances on
probe evidence and a quiet release that fails after its hosting smoke aborts
on it. The drill also covers the crash-loop gate added to ADR-1446.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | 2026-10-10 |
| Host | `gregale-internal-test-1` (n2-highmem-2, nested KVM) |
| Test | `cmd/e2e/safe_release_drill_metal_test.go` `TestSafeReleaseDrillMetal` |
| Branch | `feat/safe-releases-by-default` |
| Signals | real apid, meterd canary progression, schedd, vmmd, gatewayd-internal; breaker PromQL answered from schedd's and the gateway's real `/metrics` |
| Verdict | **PASS** — all seven drills after the rollback fix (finding 1) |
| Final full run | 2026-10-13 at `aa4cc17664`, the branch rebased on main `2c4bea3d53`: `TestSafeReleaseDrillMetal` PASS in 1079 s (default 119 s, low-traffic advance on probe evidence 174 s with candidate 0/30 and stable 0/21 probe 5xx, rollback recovery 8.3 s with a `complete` receipt of `recovery_ms=7777`, bad release 111 s, probe abort 176 s, crash loop 91 s, worker fallback 101 s), leakcheck after the run clean |
| Previous full run | 2026-10-13 at `e96f95701a`, the branch rebased on main `7979d6b21d` with probe-backed quiet canaries: `TestSafeReleaseDrillMetal` PASS in 972 s (default 68 s, low-traffic advance on probe evidence 132 s + rollback recovery 13.5 s with a `complete` receipt, bad release 103 s, probe abort 148 s, crash loop 125 s, worker fallback 105 s) and leakcheck clean. The run before, at `b3f58e2fff`: PASS in 856 s (advance 156 s, recovery 6.8 s, probe abort 72 s) but one leaked VM (finding 5) |
| Earlier full run | run 11 (2026-10-12) at `f7266e3698`, the branch rebased on main `2bf07d55de` with verified rollback outcomes: `TestSafeReleaseDrillMetal` PASS in 1568 s (default 221 s, low-traffic advance 350 s + rollback recovery 11.8 s with a `complete` receipt, bad release 224 s, crash loop 217 s, worker fallback 227 s). Run 10 at `ab57a4b03`, before the rebase: PASS in 940 s |

| Drill | Result | Evidence |
|---|---|---|
| Flag-free release of a live app gets the safe default | PASS | second deploy stamped `balanced` at 1% with `rollback_on_5xx` (runs 4–6) |
| Clean low-traffic canary advances | PASS | no traffic; before probes the `1@30s` stage reached 100% past the 5-minute bound in 336–356 s (runs 4–6). With default probes of `/` it advances on probe evidence in 132–182 s, candidate and stable each 0/30 probe 5xx (2026-10-13) |
| Quiet release that fails after its smoke aborts on probe evidence | PASS | the candidate passes its hosting smoke, then `/` returns 5xx; with no customer traffic the probes alone abort it in 72–176 s with `circuit breaker: 5xx error rate regression; restoring predecessor`, and the predecessor serves 100% again (2026-10-13) |
| Bad release aborts and restores the predecessor | PASS | `/api` 5xx at a 50% canary; breaker input candidate 26/26 5xx vs stable 0/25; abort `circuit breaker: 5xx error rate regression; restoring predecessor`, predecessor back to 100% (run 8) |
| Crash-looping release aborts on liveness restarts | PASS | `/livez` failing, `/` healthy; abort `circuit breaker: crash loop (repeated liveness restarts); restoring predecessor` (runs 5, 6) |
| Defaulted release falls back when the canary worker is down | PASS | meterd killed, lease expired; flag-free deploy went live at 100% with no canary and `rollback_on_5xx=true` (runs 4–6) |
| Rollback recovery ≤ 60 s | PASS | before the fix the target went `snapshotting` → `superseded` and never served (run 6); with `ab57a4b03` the predecessor served 100% again **8.3 s** after `POST /v1/apps/{slug}/rollback` (run 9); at `f7266e3698` it served again in 11.8 s and `GET /v1/apps/{slug}/rollbacks/{id}` returned `status=complete trigger=manual recovery_ms=8961` (run 11); 2026-10-13 receipts: `recovery_ms=6782`, `7777` and `13481` |

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
   prepared rollback target and imaged promotes it unfenced (ADR-1446
   §Plain rollback is promoted unfenced); rollback recovery is re-run below.
2. **A defaulted canary requires imaged's hosting smoke verifier.** imaged
   requires the post-readiness smoke for every canary deployment; a host
   without `FAAS_API_HOSTING_SMOKE_URL` fails it. Production compute nodes
   configure it. Recorded in ADR-1446 and the runbook.
3. **The post-readiness smoke rejects a release whose `/` returns 5xx**
   before it serves any canary traffic, so the bad-release fixture fails
   `/api` instead.
4. **Harness gaps fixed in this branch:** prebuilt hello-server override,
   scrapeable schedd metrics port, apid signing key for the rollback
   precheck, request telemetry opt-in, Hobby plan (Pro's public auth default
   is `bearer`), and a second labelled Hobby account for the probe-abort case
   (the drill account is at its 5-app limit).
5. **The drill leaked a running VM.** The harness does not destroy VMs on
   teardown and vmmd leaves microVMs running when it stops, so an app woken
   just before the end (`sr-fallback`) kept its Firecracker process. The drill
   now parks every app it created before the harness stops, as
   `dev_loop_metal_test.go` does; the next run's leakcheck was clean. The node
   run script also records jail ids before the test and removes only the
   ones the run created.
6. **Host contention times out deploy waits.** A run on 2026-10-13 while two
   other sessions compiled on the node (load above 4 on 2 vCPUs) left three
   deployments in `imaging` or `snapshotting` past the 3-minute wait. The
   node script now waits for a host with no Go compiles before the drill.
