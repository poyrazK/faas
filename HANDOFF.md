# Handoff: safe releases by default (ADR-1160)

> **Read this, then delete it.** This file exists only to hand the work to the
> next agent. Once you have read it, remove it in your first commit
> (`git rm HANDOFF.md`) so it never reaches `main`.

## Where the work is

- Branch: `feat/safe-releases-by-default-v2` on `origin` (poyrazK/faas).
- Base: `origin/main` merged in at `5e57f6489d`. Main had moved 6 more commits
  by 2026-10-13; merge it again (don't rebase or force-push) before a PR.
- No PR is open. The user opens PRs only when they ask. Push branches only.
- `feat/safe-releases-by-default` (without `-v2`) is the stale pre-rebase
  history. It is superseded, and the user may delete it.
- Design and decisions: [ADR-1160](docs/adr/1160-safe-releases-by-default.md).
  Drill record: [docs/drills/2026-10-10-safe-release-drill.md](docs/drills/2026-10-10-safe-release-drill.md).

## What the branch does

1. **Safe default.** apid fills in the balanced canary and `rollback_on_5xx`
   for production releases of live, request-mode, non-preview apps. Explicit
   flags win. The `release_policy` manifest field takes `safe` or `immediate`.
   The kill switch is `FAAS_SAFE_RELEASE_DEFAULT_ENABLED=false`. If the canary
   worker lease is down, the release cuts over immediately with rollback
   (`pkg/apid/release/default_policy.go`).
2. **Bounded low-traffic hold** (`api.CanaryLowTrafficMaxHold`, 5 minutes)
   and the **crash-loop gate**, which reads `liveness_restarts_total` in meterd.
3. **Plain rollback fence fix:** `deployments.rollback_prepared_at`. imaged
   promotes a prepared rollback target without the fence.
4. **Verified rollback outcomes:** the `deployment_rollbacks` ledger, the
   `Rollbacks.Run` worker, the receipt at `GET /v1/apps/{slug}/rollbacks/{id}`,
   `gregale rollback` waiting by default, plus metrics and alerts
   (`pkg/apid/release/rollback_outcomes.go`).
5. **Probe-backed low-traffic canaries** (latest commit, `a224feb75c` plus
   the drill fix):
   - apid probes `GET /` on the candidate and its predecessor during a
     safe-policy canary. This reuses the ADR-847 probe worker:
     `pkg/apid/apidserver/route_probe_worker.go` and `pkg/apid/release/probe.go`.
     The state side is `ListDefaultRouteProbeTargets` and `RouteProbeTotals`
     in `pkg/state/route_probe.go`.
   - meterd adds probe responses to the 5xx sample while organic traffic is
     below the floor (`cmd/meterd/canary_health.go`).
   - The breaker aborts on a probe-backed 5xx regression, and advances at the
     stage boundary on clean probes (`advance_probe_evidence`), in
     `pkg/canary/circuit_breaker.go` and `pkg/canary/preset.go`.
6. **Drill suite** `cmd/e2e/safe_release_drill_metal_test.go`. It has six cases
   and needs n2 metal.

## Verification status (2026-10-13, n2 node, rev `a224feb75c`)

- Linux unit tests pass:
  - `pkg/apid/release`, `pkg/canary`, `cmd/meterd`;
  - `pkg/state` default-probe store tests against Postgres
    (`TestDefaultRouteProbeStorePostgres`, `TestRouteProbePostgresSyntheticEvidence`).
- Drill, 5 of 6 pass:
  - The quiet canary advanced on probe evidence in 182 s, on 30 candidate
    and 21 stable probes with no 5xx.
  - A release that fails `/` after its smoke aborted on probes in 126 s.
  - Rollback recovered in 10.7 s, with receipt `complete`, `recovery_ms=9567`.
  - The bad-release and crash-loop aborts passed.
- **Failed, and fixed in the newest commit but not yet re-run:** the case
  "defaulted release falls back to immediate when the canary worker is down"
  hit a `403` creating its app. The new probe-abort case was the sixth app on
  a Hobby account with a 5-app limit. That case now uses its own Hobby account.
- **Open:** the `apidserver` test build has not completed anywhere. The
  internal test unit was OOM-killed on the laptop (9.1 GB peak, under
  `go vet`) and twice on the n2 node (`compile: signal: killed` under
  `-c=1 -dwarf=false`, `GOMEMLIMIT=6GiB`, with about 7 GB free). Nothing so
  far shows a code error, but the new and changed `apidserver` test files
  are still unverified.

## Next steps, in order

1. **Get the `apidserver` tests compiled and passing.** New and changed test files:
   - `pkg/apid/apidserver/route_probe_default_test.go` (external package);
   - `pkg/apid/apidserver/export_test.go` (added `SeedCanaryFixture`,
     `RouteProbeChallengeDelay`, `(*server).DrainRouteProbes`);
   - `pkg/apid/apidserver/route_probe_worker_test.go` (kill-switch line).

   Neither machine here has the memory for it, so let CI compile it: push and
   read the ci-light and apid test jobs. Alternatively, run
   `make test-apid RUN='^TestRouteProbeWorker'` on a host with at least 12 GB
   free. If it compiles, both `TestRouteProbeWorker*` tests must pass. The
   default-probe test covers the safe policy (20 probes of `/`), immediate
   policy (none), and kill switch (none).
2. **Re-run the full drill on n2** and confirm all six cases pass.
3. Record **run 12** in the drill record and update the ADR-1160 entry in
   `docs/STATUS.md`. Use the numbers above plus the re-run.
4. Merge `origin/main` again. Regenerate if needed:
   - `sqlc generate` (v1.31.1);
   - `go run ./cmd/capabilities-md > docs/capabilities.md`;
   - the CLI reference (`gregale man --markdown`);
   - `UPDATE_ENV_CONTRACT=1 go test ./pkg/daemonunitspec -run DocInSync`.
5. Open a PR only when the user asks.

## Running on the n2 node

- Access:
  `gcloud compute ssh gregale-internal-test-1 --zone=us-east1-b --project=gregale-prod --tunnel-through-iap --account=kucukarslanhuseyinpoyraz@gmail.com`
- Upload a source tarball as `/mnt/e2e-scratch/sr-upload/src-<rev>.tar.gz`
  (`git archive --format=tar.gz HEAD`). Then run
  `sudo -b setsid nohup bash /mnt/e2e-scratch/sr-node-verify3.sh <rev> >/dev/null 2>&1 </dev/null`.
  - It builds every drill binary into `/mnt/e2e-scratch/sr-drill-<rev>`.
  - It runs the unit, `pkg/state` Postgres, and `apidserver` route-probe tests.
  - Then it runs the drill through `run.sh`, which waits for the host lock.
  - Logs: `/mnt/e2e-scratch/sr-verify-<rev>/verify.log` and
    `/mnt/e2e-scratch/sr-drill-<rev>/run.log`.
  - Its `apidserver` step is OOM-killed on this node and its `grep` filter
    hides the reason. Remove that step, or ignore `APIDSERVER_EXIT`.
- The scratch disk is shared and nearly full (7 GB free on 2026-10-13).
  Clean only your own `sr-*` directories. Keep
  `/mnt/e2e-scratch/sr-verify-f7266e3698/gomod`, the module cache the script
  reuses, and the scripts and logs in `sr-drill-20261010`.
- Never export `GOTMPDIR`, `GOCACHE`, or `GOMEMLIMIT` into `run.sh`. The
  script builds in a subshell for this reason.
- Each run leaves one or two Firecracker VMs. `run.sh` removes only the jails
  it created. Leaks from other sessions show up in `LEAKCHECK_BEFORE`.

## Known failures that are not this branch's

- `cmd/gregale` data-API tests need Node 22, which is missing on the node and
  locally.
- `pkg/state` Postgres tests fail locally without a database.
- `make text-encoding-check` flags two files on `main`.

## Working rules from the user

- Do the work directly, with no subagents.
- Use one PR for the agreed scope, and open it only when asked.
- Never push to `main`. Never force-push.
- Run tests one package at a time and never compile in parallel; memory is
  the bottleneck.
- n2 nested-KVM metal runs count as evidence. Don't add native-acceptance
  caveats.

**Reminder: delete this file (`git rm HANDOFF.md`) once you have read it.**
