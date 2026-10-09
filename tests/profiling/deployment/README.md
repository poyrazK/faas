# Deployed route profiling acceptance

For automatic fixture provisioning, evidence artifacts and cleanup, see the
[native CI gate](../../../docs/ops/profiling-native-ci.md). It is also available
as `make native-profiling-acceptance` on the dedicated candidate stack.

This runner sends real traffic, reads apid profile responses and creates saved
investigations. Use disposable deployments: it produces CPU load and retained
investigation records. It does not provision deployments or modify rollout policy.

## Setup

1. Build `./tests/profiling/deployment/workload` in this repository. Deploy four
   revisions of that binary **under the same app**, setting
   `PROFILE_ACCEPTANCE_MODE` to `baseline`, `regression`, `label_loss`, or `sparse`.
   The binary uses `PORT` (default 8080) and starts Gregale's Go collector.
2. Enable profiling in each deployment with `window_seconds: 2`. Declare the
   explicit synchronous route `GET /hot/{id}` in each host route contract.
   Keep `/healthz` available for startup readiness and `/readyz` for workload
   observations through the gateway. Use the runtime ID actually recorded
   for these deployments (`go124` in the example).
3. Provide a separate gateway URL pinned to each deployment. Traffic **must pass
   through Gregale's gateway** so deployment-scoped request telemetry is recorded.
   Direct VM URLs and random stable/canary routing cannot qualify this check.
   Prevent other clients, probes and retries from hitting the hot route during
   acceptance; the client count is intentionally compared exactly with gateway
   telemetry. All four deployments must remain queryable and runnable.
4. Copy `config.example.json` and replace the URLs, slug and deployment UUIDs.
   Provide an API token with profiling read and investigation write access in
   `GREGALE_ACCEPTANCE_TOKEN`. It is sent only to the configured API origin;
   requests reject redirects. The runner never writes the token to its report.

```sh
python3 tests/profiling/deployment/run.py \
  --config /path/to/private-config.json \
  --report /tmp/route-profile-acceptance.json
```

Default traffic takes eight minutes plus ingestion settling. Request concurrency
is one. The candidate performs four times the baseline CPU work. Label loss keeps
CPU labels but omits every second request-entry counter. Sparse sends only five
requests. Responses and reports are bounded; report permissions are restricted.

The report includes exact windows, successful client counts, independently
observed gateway counts, labeling shares, known hotspot presence, CPU/request
metrics, saved investigation URLs and native restore evidence. Check outcomes
must have the intended cause: a sparse route must have fewer than 20 observed
requests; label loss must have available but inconsistent labeling evidence.
Missing history does not pass either case. A failed run retains partial evidence
and its error type without printing potentially sensitive server error bodies.

Exit codes: **0 passed**, **1 failed**, **2 incomplete**. Ingestion failures or
unsuitable traffic/capture windows fail. Increase `settle_seconds` (1–120) for
telemetry latency; duration is bounded to 30–600 seconds. Maintain enough captures
and wholly contained request reports to meet the standard 80% coverage checks.
Do not lower thresholds merely to make an acceptance run pass.

## Native park/restore

The supplied `native_restore.py` adapter implements the lifecycle drill using
Gregale's park/wake APIs and authoritative wake timeline events. Configure it in
the same private config file:

```json
"native_restore_command": [
  "python3", "/absolute/repo/tests/profiling/deployment/native_restore.py",
  "--config", "/path/to/private-config.json"
]
```

Set `GREGALE_NATIVE_PROFILE_TOKEN` to a separate random secret of at least 32
characters, both in the runner environment and in the baseline deployment. The
workload exposes `/acceptance/profile-state` and `/acceptance/stale-profile` only
when this secret is configured, and requires its bearer token on both routes.
Restrict these fixture routes to the acceptance environment. The token is never
forwarded to the public API or written to the evidence receipt.

Requirements:

- Use a native KVM deployment with the updated scheduler from ADR-824. Profiled
  apps now take fresh terminal captures on park, with additional capture/storage
  cost. Deploy the updated profiling guest-init image and rebuild the workload.
- Baseline must be the app's live deployment for explicit wake, and have exactly
  one running instance. Disable background traffic, scheduled jobs and competing
  wake operations for the whole disposable app.
- The drill **parks the whole app**, including its other fixture deployments.
  Finish their traffic phases first. An API token needs app lifecycle write,
  instance read and wake timeline read access in addition to profile permissions.
- Recreate/redeploy the baseline before repeating a completed drill: the retained
  probe intentionally remembers its original staged epoch. No implicit deployment
  promotion or snapshot invalidation is performed by the adapter.

The adapter stages a valid **synthetic CPU-format probe** and records its digest
and the current bridge epoch before park. It observes a new `wake.park_completed`
event with a snapshot locator and confirms no instances remain resident. It
queues wake, verifies actual `wake.boot_completed` method `restore` with no cold
fallback, verifies that the restored baseline retained its process nonce and
staged probe, and observes a different collector epoch. It replays that staged
probe through guest loopback and requires the bridge's specific old-epoch HTTP
409 response. It never submits the synthetic probe with a fresh epoch.

Its JSON receipt includes snapshot event evidence, restore event evidence,
instance/wake IDs, process nonces, epochs and the probe digest. It emits only JSON
on stdout, has a bounded 150-second operation budget and exits nonzero on failure.
It attempts a recovery wake if it fails between park and wake; the receipt records
whether that recovery request was queued. No services are stopped or killed.

The parent runner preserves failed receipts. After successful native restore it
independently generates new traffic, checks profiling/counters resume, reconciles
fresh requests against gateway telemetry, and checks that old-window CPU and
accepted-capture counts did not grow. The receipt identifies observed API and
bridge evidence; it does not independently inspect hypervisor internals or claim
that the synthetic probe came from a sampled production capture.

The adapter can also be invoked directly:

```sh
python3 tests/profiling/deployment/native_restore.py --config /path/to/private-config.json
```

Custom native command adapters remain supported through the same receipt contract.
Keep secrets out of their receipts. Without native evidence the parent runner
returns `incomplete`.

## Validation status

The workload and scheduler build; both Python scripts pass syntax compilation.
A complete run requires deployed native endpoints and the two tokens. They are not
supplied by the portable workspace, and no KVM device is available here, so no
native acceptance pass is claimed. See the earlier local backend validation in
`docs/validation/route-profiling-2026-10-08.md`.
