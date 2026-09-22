# FaasSyntheticCanaryFailed

Source: `.github/workflows/synthetic-canary.yml` (pushed to Alertmanager by
`scripts/ops/canary_alert.sh`; **not** a Prometheus rule).
Severity: page.

## Symptom

A scheduled synthetic deploy failed. Something between `gregale deploy` and a
200 from the app's health path is broken **right now**, on the release that is
currently serving.

Unlike every other page in this directory, this alert is not derived from a
metric. It is the outcome of
`deploy/scripts/production-release-acceptance.sh` — the same script
`cd-platform` runs as its final release gate — executed against production on
a six-hourly schedule.

## Why this alert exists

The four SLO rules that would otherwise cover the deploy path are
structurally silent when nothing is happening. Each ends with a zero-volume
guard, for example `FaasBuildSuccessLow`:

```promql
and sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m])) > 0
```

The guard is correct — without it a quiet night divides by zero and pages —
but it means a broken deploy pipeline with no deploys in flight produces no
denominator and therefore no alert. `FaasApiAvailabilityLow`,
`FaasBuildSuccessBurnRateHigh` and `FaasApiAvailabilityBurnRateHigh` share
the shape.

The precedent is PR #1286: `FAAS_FUNCTION_RUNNER_*` was unset on imaged, so
every function deploy failed platform-wide while warm instances kept serving
existing traffic. No passive metric could see it.

So: **a green dashboard does not contradict this alert.** If the two disagree,
this one is the one describing a customer's experience.

## First moves

The annotation carries a `run_url`. Open it — the failing step's log is the
tail that was also embedded in the alert description, and it names which phase
failed.

The script covers several phases; the log tells you which one:

| Failure text | Meaning |
|---|---|
| `one or more production acceptance deployments failed` | `gregale deploy --wait` did not reach `live`. Build or imaging path. |
| receipt verification (`jq -e ... .status == "live"`) | Deploy reported live but the hosting receipt or smoke status was not verified. |
| `curl` against `${app_url}${health_path}` | Deployed and live, but not reachable through the public edge. Gateway or routing. |
| `verify-placement` mismatch | Deployments did not spread across the expected `active_node_count`. Scheduler or a node out of rotation. |
| `previous serving revision became unavailable during redeploy` | Zero-downtime redeploy regressed — the old revision stopped serving before the new one was ready. |

## When the failure is `health probe reached deployment ""`

An EMPTY served deployment (not a different one) means the gateway answered
the health path at the edge instead of proxying to the candidate. The platform
deployment header is set in exactly one place —
`pkg/gateway/handler.go` after a target is picked — and only when the
deployment-smoke bypass authorizes. `pkg/gateway/edge_answers.go` short-circuits
the health path unless that same bypass passes:

```go
r.URL.Path == normalizeHealthPath(app.HealthPath) && !app.HealthPathWakes && !h.authorizedDeploymentSmoke(r, app)
```

So the question is always "why did the bypass fail", and two counters answer it
directly:

```promql
sum by (outcome) (rate(gateway_smoke_validation_total[15m]))
sum by (outcome) (rate(gateway_smoke_challenge_total[15m]))
```

| `gateway_smoke_validation_total` outcome | Means |
|---|---|
| `match` | Bypass worked. The empty header is not from this gateway. |
| `no_challenge` | No challenge under this (app, deployment). The map is process-local, so either the `deployment_smoke_challenge` notification never reached THIS gateway process, or the publisher and gateway disagree on the app identity. |
| `token_mismatch` | A challenge IS present but the presented token differs — a stale or duplicated challenge, not a delivery problem. |
| `expired` | The challenge arrived but aged out before the probe. Look at the publish-to-probe gap against the ~15s token lifetime. |
| `missing_token` | The request reached the gateway without the token header. Suspect a hop between `imaged` and `gatewayd-internal` dropping `X-Faas-Platform-Smoke-Token`. |

`gateway_smoke_challenge_total{outcome="stored"}` rising while validation shows
`no_challenge` means the challenge landed on a DIFFERENT gateway process than
the one serving the probe — expected to be impossible, since `pg_notify` fans
out to every listener, so it points at a gateway whose LISTEN connection is
down.

Check `healthEdgeAnswered` alongside: a spike there during a deploy confirms
the edge answered the probe.

## Correlate before digging

Check whether a real alert already fired and this is a downstream symptom:

- `FaasBuildQueueBacklog` / `FaasBuilderSliceOOMKill` → builds, not the canary.
- `FaasDaemonDown` / `FaasDaemonNotReady` → a daemon is out; fix that first.
- `FaasInstanceDivergence` / `FaasStaleDeploymentBacklog` → scheduler state.
- `FaasDeployVersionSkew` → the fleet is mid-rollout; a canary during a
  `cd-platform` run can fail on contention rather than on a real defect.

If a `cd-platform` run was in flight, re-run the canary manually before
treating it as a defect. The two are not mutually excluded — the canary's
`concurrency` group only serializes canaries against each other.

## Verify by hand

From the control plane, the same probe the workflow runs:

```bash
env RELEASE_SHA="$(basename "$(readlink -f /opt/faas/current)")" \
    RUN_ID="$(date +%s)" \
    ACTIVE_NODE_COUNT=1 \
    bash /opt/faas/current/../deploy/scripts/production-release-acceptance.sh
```

The script mints its own two-hour token, revokes it, and deletes every app it
created on exit — including on Ctrl-C. It is safe to run ad hoc.

## Expected resolution

No resolve is sent. `endsAt` is set to three hours, so a passing canary lets
the alert lapse on its own and a second failure refreshes it. If you have
fixed the cause and do not want to wait, run the workflow manually
(`workflow_dispatch`) and let a green run expire it, or silence it in
Alertmanager.

## False-alarm cases worth knowing

- A `cd-platform` deploy running concurrently (see above).
- Fleet capacity genuinely exhausted — the canary needs room for
  `2 × active_node_count` short-lived apps. Check `FaasHighResidentRamPct`.
- A compute node out of rotation makes `verify-placement` fail on node count
  while single-node deploys still work.
