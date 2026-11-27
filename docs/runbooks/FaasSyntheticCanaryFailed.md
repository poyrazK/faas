# FaasSyntheticCanaryFailed

Source: `.github/workflows/synthetic-canary.yml` (pushed to Alertmanager by
`scripts/ops/canary_alert.sh`; **not** a Prometheus rule).
Severity: page.

## Symptom

A scheduled synthetic deploy failed, or it passed but could not report success
to its independent heartbeat monitor. The alert summary and failing workflow
step distinguish these cases. An acceptance failure means something between
`gregale deploy` and a 200 from the app's health path is broken on the release
that is currently serving.

Unlike every other page in this directory, this alert is not derived from a
metric. It comes from the workflow that runs
`deploy/scripts/production-release-acceptance.sh` — the same script
`cd-platform` runs as its final release gate — against production every six
hours and reports successful scheduled runs to an external monitor.

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

When acceptance failed, **a green dashboard does not contradict this alert**:
the canary directly exercises a customer's deploy path. A heartbeat-only
failure means that path passed but its independent monitoring needs repair.

## First moves

The annotation carries a `run_url`. Open it — the failing step's log is the
tail that was also embedded in the alert description, and it names which phase
failed.

The SSH preflight runs before any deployment probe. Failure at this stage
means customer deployment health was **not tested**. The workflow prints fixed
`canary-ssh` codes without host addresses, keys or raw connection errors:

| Code | Meaning |
|---|---|
| `ssh_configuration_missing` / `ssh_host_invalid` | Missing control-plane host/key, or an invalid host value. |
| `ssh_host_pins_missing` | No operator-provided `COMPUTE_KNOWN_HOSTS` pins. |
| `ssh_host_keys_unavailable` / `ssh_host_scan_timeout` | No keys were obtained, or the whole scan timed out. Check reachability before concluding a key changed. |
| `ssh_host_key_untrusted` | A presented key did not match the pins, or strict verification failed during authentication. Independently verify the host identity before updating pins. |
| `ssh_authentication_failed` | The pinned host rejected authentication. |
| `ssh_preflight_failed` / `ssh_preflight_timeout` | The authenticated no-op did not complete. Review control-plane connectivity and SSH service health. |

The scan has a 15-second deadline and authentication a 20-second deadline,
each with at most five more seconds before forced termination. Subsequent
connections use batch authentication, one connection attempt, a ten-second
connection timeout and SSH keepalives. Keys and readiness are kept in a private
runner-local directory; failed setup clears earlier readiness.

If setup failed, notification skips SSH and tries the independent
`CANARY_FALLBACK_WEBHOOK_URL` immediately. Otherwise the complete Alertmanager
submission attempt has a 120-second deadline plus at most five seconds before
forced termination, then fallback has a three-second connection timeout and
15-second total limit. The failure-notification step has a three-minute budget.
The original job remains failed even if notification submission succeeds.

`notification_submitted_alertmanager` and `notification_submitted_fallback`
mean only that the submission returned success, not that the operator received
it. Missing fallback configuration reports `fallback_unconfigured`. An
invalid HTTPS URL reports `fallback_url_invalid`. Transport errors or
non-2xx responses (including redirects) report
`fallback_submission_failed`. Raw webhook URLs, errors and responses are not
printed. Both submission paths still require the existing delivery qualification.

If the heartbeat step failed, use the
[missing-run monitor procedure](../ops/synthetic-canary-heartbeat.md). Its
fixed error code identifies missing configuration, an invalid URL or failed
delivery. Do not print the secret URL or repeat an ad-hoc success ping. An
external missed-ping alarm can also arrive without any failed workflow run;
check for a disabled schedule, queued job, runner loss or cancellation.

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
