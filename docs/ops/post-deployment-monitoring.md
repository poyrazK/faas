# Post-deployment monitoring verification

`gregalectl obs verify-deployment` waits for usable monitoring evidence after a
rollout and saves a release-linked JSON report. It checks expected serving
probes, the aggregate notification pipeline and every expected notification
receiver. It reads the existing operator APIs and performs no deployment,
rollback, direct URL probe or notification send.

## Run it

After confirming release convergence, record the boundary and run the verifier
on the control plane, where the API defaults to loopback:

```bash
release="$(git rev-parse HEAD)" # checkout of the selected signed release
evidence_after="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
gregalectl obs verify-deployment \
  --release "$release" --after "$evidence_after" \
  --report post-deployment-monitoring.json --json
```

`--release` requires the full 40-character lowercase Git SHA. `--after` requires
an RFC3339 timestamp representing the rollout evidence boundary (allowing the
existing 30-second clock skew). Record it **after** release convergence and
retain it across retries. Setting an older boundary weakens the freshness claim.
The SHA labels the report; these monitoring APIs do not prove runtime identity.
The platform workflow separately verifies fleet release convergence.

Use an existing operator session, or the existing bearer fallback
`FAAS_ADMIN_TOKEN`. The APIs enforce operator read authorization, MFA and the
allowlist. Automation can use `--token-stdin` to read a bearer from one
newline-terminated stdin line; this explicitly takes precedence over saved
sessions and environment tokens. It cannot be combined with `--admin-token`.
Never place the credential in report files or workflow command arguments.

The default wait is the largest expected probe's `max_result_age_seconds`
plus 120 seconds for fresh collection and evaluation. A policy age of 900
seconds therefore gives a 17-minute budget. Missing policy uses 20 minutes;
all automatic budgets are capped at one hour. `--wait-timeout 5m` overrides the
budget, and `--poll-interval 10s` sets the polling interval (1 second to 1 minute).
Each HTTP read has a 10-second ceiling and shares the remaining overall budget.

## Interpret the report

| Exit | State | Meaning |
|---|---|---|
| 0 | `passed` | All three checks have complete usable evidence. |
| 1 | `degraded` | A current confirmed fault remains when the budget expires. |
| 3 | `unknown` | Evidence is incomplete, unavailable, unsupported or cancelled. |
| 2 | local error | Invalid options/credentials or a report could not be written. |

The three named checks are `serving_probes`, `notification_pipeline` and
`notification_receivers`. Each includes a stable reason and runbook link.
Confirmed faults take precedence over unknown peers. Transient faults can
recover during polling; `had_confirmed_faults=true` preserves that history even
if the final observation passes or becomes unavailable. The report retains the
latest validated snapshot, not a full history of every poll.

Fresh means both post-boundary and no older than 120 seconds for source
observations and serving evaluations. A healthy serving probe must also have
run after the boundary and remain within its own configured result-age policy.
A fresh evaluation of an old run cannot pass. All expected pairs must pass;
missing or unconfigured intent and explicitly empty inventories stay unknown
for deployment qualification.

Receiver counters and the observation of each last-success gauge must be fresh
after the boundary. The success-clock **value** may be historical: it records
when the collector previously confirmed a successful completed request. A
quiet route can therefore pass with a freshly observed historical confirmation.
A route whose success has never been observed stays unknown. Verification does
not send a notification to manufacture that evidence. Follow the
[receiver qualification procedure](notification-delivery.md) when needed.
Ten-minute notification counter windows can include pre-deployment failures;
a detected fault does not establish that this release caused it. This is a
convergence check and does not establish sustained availability, per-alert
delivery, provider acceptance or human receipt.

Schema version 1 records the requested SHA, evidence boundary, start/update
times, wait budget, attempts, checks, the latest validated source evidence and
limitations. `--report` first replaces prior evidence with unknown, then
atomically replaces an owner-readable file after each poll, including
cancellation when the process can handle the signal. JSON
stdout contains the final report. Raw serving URLs, query tokens, addresses,
arbitrary source labels and provider error text are excluded. Node names and
SHA-256 URL IDs retain their [existing meaning](serving-health.md).
Always honor the command's exit status and report update time. Invalid local
arguments or report I/O failures cannot guarantee a replacement file.

## Platform workflow and activation

Use [production monitoring qualification](monitoring-qualification.md) for
repeatable, test-bound operator attestations of notification receipts. Health
verification itself remains read-only and sends no qualification messages.

The canonical `.github/workflows/cd-platform.yml` job initializes an unknown
report, confirms control-plane and compute release convergence, and records
`MONITORING_NOT_BEFORE`. Existing health and acceptance gates run next. Monitoring
verification runs after the existing rollback step, so its own failure cannot
trigger rollback. A nonzero result fails the job and requires investigation.
Standalone control-plane, compute and unit repair workflows retain their
existing gates; use the CLI explicitly for those paths.

The wrapper comes from the workflow commit, while the remote CLI comes from the
requested signed release. It uses pinned host keys, disables PTY allocation,
passes the token through SSH stdin, reads the loopback API and checks the active
release pointer before and after collection. A changed pointer, missing token,
old CLI/API, invalid report or transport error leaves unknown evidence. It
never provisions tokens or bypasses API authorization.

Before activation:

1. Deploy the APIs and CLI described in [serving health](serving-health.md) and
   [notification health](notification-delivery.md#operator-api-and-cli).
2. Converge independent serving/receiver inventories, collectors and rules.
   Qualify expected receiver routes, including external watchdog delivery.
3. Configure `MONITORING_OPERATOR_TOKEN` as an environment secret in each target
   GitHub deployment environment. Use an authorized operator bearer with the
   existing admin scope, enrolled MFA and allowlist policy; verify it can read
   both endpoints through the control-plane loopback API. Keep it separate from
   customer canary credentials. Missing or expired credentials fail as unknown.
4. Review the first deployment report and its source clocks. The normal
   five-minute probe cadence should converge within the default 17-minute budget.

The job attempts an artifact upload with `if: always()`, named
`post-deployment-monitoring-<run_id>-<run_attempt>`, retained for 14 days. An
earlier gate failure retains `verification_not_started` with a null evidence
boundary if convergence was never reached. A killed runner or failed checkout
can prevent artifact creation/upload; absence of an artifact is never a pass.
Remote collection interruptions retain the initialized unknown fallback rather
than a partial remote stdout stream. Direct CLI use with `--report` preserves
each completed local poll.

Run local checks with `make post-deployment-monitoring-test` and
`go test ./cmd/gregalectl`. Tests use local HTTP fixtures and an SSH stub; they
do not contact production or send real notifications.
