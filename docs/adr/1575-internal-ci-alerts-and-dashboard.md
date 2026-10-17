# ADR-1575: Internal CI operational alerts and dashboard

- Status: implemented with every shipped CI flag off
- Date: 2026-10-16
- Extends: ADR-1579

## Decision

Add five internal warning alerts, a private Grafana dashboard and shared
Prometheus recording rules. Reuse the installed private apid/githubd scrapes,
consolidated rule file, alert routing and Grafana provisioning. These assets
observe state; they do not admit work, retry execution, change a lease or enable
a service flag. Native compute/provider qualification remains separate.

## Configuration is independent evidence

Add `<daemon>_ci_worker_enabled{worker}` for only the workers owned by that
daemon. apid owns dispatcher, metadata admission, automatic admission and
retention; githubd owns metadata and execution Checks. Use the same bounded
`Config.WorkerConfiguration` helper for worker launch and metric wiring.
Configuration is observed independently of database health and dependency
readiness. Unknown keys are never emitted as labels.

Extend the dormant metrics contract: all-flags-off emits observation-disabled
and local worker-disabled samples. It still performs no CI health query, starts
no CI worker and writes no heartbeat. Removing admission leaves independently
configured dispatcher/retention drain ownership intact. Check and automatic
admission switches require their existing allowlist/GitHub gates.

A fresh disabled switch suppresses its worker, expired-lease and Check-delivery
warnings even if old failed observations or pending work remain. No switch
sample means configuration is unknown, not disabled. Enabled workers without
usable heartbeat evidence may alert only while durable health is readable.
A failed database observation raises a health warning instead of manufacturing
worker failure from missing dependent data.

## Shared fleet observations

The `faas_internal_ci` group evaluates every 30 seconds. Accept raw fleet data
only from a target with a successful scrape, observation enabled and a successful
durable read on the same `(job, instance)`. Each daemon reads the same durable
fleet. Take max by queue/condition, worker/condition and resource usage across
readable targets, and min by resource for global limits. Compute headroom from
those normalized inputs, clamped at zero. Do not sum shared snapshots across
replicas or daemons. A healthy replica can supply evidence while another fails.

Worker configuration is max by worker across current owner-daemon samples. An
enabled replica keeps that worker path expected to run. A healthy or starting
group counts as available; retained stale groups alone do not invalidate an
available group. Per-condition maxima can refer to different snapshots, so their
sum is not a process census or a bound on concurrent execution.

No readable fleet snapshot means queue, resource and worker-condition evidence
is absent. Do not replace it with healthy zeroes. Explicit unknown fallback
codes exist only for status panels: health read states are disabled/readable/
unavailable/unknown; worker states additionally distinguish healthy, starting,
degraded, blocked, stalled and stale. A missing scrape displays unknown. Existing
daemon-down alerts own lost targets; CI rules do not infer enabled intent from
an unreachable process or a removed target.

## Alert policy

All five alerts are `severity=warn`, `component=platform`, `family=internal_ci`,
`public_status=internal`. They use existing warn routing when deliberately
deployed; this implementation does not send a notification or deploy a rule.
Labels retain only bounded daemon, worker, queue, condition or resource values.
No host, account, repository, branch, SHA, execution or provider-error label is
introduced by the normalized alert series.

- `FaasCIHealthUnavailable`: five minutes with observed CI monitoring enabled
  and no readable replica of that daemon. Partial replica failures remain
  visible in raw target metrics; they do not duplicate a fleet warning.
- `FaasCIWorkerUnavailable`: five minutes without a healthy/starting group for
  an enabled worker, while the fleet health query is readable. Covers unknown,
  stale, stalled, degraded and blocked observations. Existing heartbeat logic
  owns its startup/progress age classification.
- `FaasCILeaseRecoveryStalled`: expired leases persist for five minutes while
  their owning worker is configured on. Time eligibility never grants native
  cleanup safety or permits dropping a fence.
- `FaasCICheckDeliveryDead`: dead metadata or execution Check delivery persists
  for five minutes while its publication worker is on. Recovery uses existing
  safe diagnostics and bounded audited execution-Check retry intent.
- `FaasCIQuotaPressure`: existing global usage is at least 80 percent for ten
  minutes. Active executions/dispatch leases require the dispatcher on; storage
  byte/object/I/O/permanent-journal budgets require dispatcher or retention on.
  Per-account history budgets are not invented as global limits.

Oldest-item age is displayed for investigation. It is measured from retained
item creation; it is not queue latency or delivery latency and never triggers
a standalone age alert. There is no automatic remediation or new quota.

## Dashboard and deployment

`deploy/grafana/internal-ci.json` has UID `faas-internal-ci`, a byte-identical
Ansible mirror and provisioning through the existing Grafana role. Panels show
read status, worker availability/configuration, queue counts, retained-item age,
Check/retry backlog, expired leases, global usage/headroom, storage bytes and
anonymous worker observations. Queries use the shared recording rules.

Status and count tables use instant queries; status reductions use the current
last value. Missing values display Unknown. Historical charts preserve gaps.
No healthy-zero fallback or last-non-null reduction hides missing evidence.
Default-off configuration displays Disabled where configuration is observed;
a missing scrape or missing rule group displays Unknown. The dashboard contains
no public customer navigation, account drilldown or capability enablement.

## Validation

Twenty-two real promtool scenarios exercise threshold timing, quiet disabled
paths, unreadable/missing health, each worker failure state, startup/restart,
recovery, owning-worker lease mapping, exhausted deliveries, independent drain
ownership, replica deduplication and exclusion of failed snapshots. Go tests
cover configuration/launch ownership, failed-read configuration metrics,
closed worker labels, internal alert metadata, dashboard recording references,
unknown mappings, instant status reads and provisioning mirror identity.

Use `make test-ci-observability`, with `PROMTOOL` set if the pinned binary is
outside PATH. Existing CI already walks the new `*.test.yml` fixture. This does
not require KVM, a running Grafana server or access to native/GCS/GitHub systems.
