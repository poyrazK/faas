# Internal CI operational warnings

Gregale CI remains internal and disabled by default. This runbook covers
`FaasCIHealthUnavailable`, `FaasCIWorkerUnavailable`,
`FaasCILeaseRecoveryStalled`, `FaasCICheckDeliveryDead` and
`FaasCIQuotaPressure`. All are internal warnings. Native qualification and an
authorized internal pilot are separate from these observations.

Open the private Grafana dashboard `/d/faas-internal-ci` and inspect the read
status and configured workers first. When the account has internal opt-in,
admin authentication, the existing MFA policy and a trusted HTTPS origin, use:

```sh
gregalectl ci health
gregalectl ci checks list
gregalectl ci execution list
```

The CLI reports account-owned work and quotas. Grafana shows anonymous fleet
aggregates; per-account history limits are not global limits. Health and status
reads do not need compute, Check publication or storage transport enabled.
Detailed commands and release gates are in [the internal CI guide](../ops/internal-ci.md).

## Read the evidence

- Disabled means a fresh owner configuration says the path is off. Paused
  backlogs remain visible and their work-specific warnings stay quiet.
- Unknown means the scrape, declaration, recording group or worker evidence is
  missing. It must not be interpreted as a healthy empty queue.
- Unavailable means monitoring is configured on but no replica of that daemon
  can read durable health. If all fleet reads fail, dependent counts disappear.
- Starting and Healthy mean at least one matching group is available. Check
  per-condition counts for other blocked/degraded/stalled/stale groups.
- Heartbeats become stale after 45 seconds; fresh heartbeats without cycle
  progress for 120 seconds become stalled. The underlying diagnostic slots are
  bounded and fenced; they are not a complete census of daemon processes.

Raw fleet gauges repeat on every apid/githubd replica. Use the dashboard's
`faas_ci:*` recording rules, which discard unreadable targets, use max for
counts/usage and min for limits, and compute conservative headroom. Do not sum
replicas or per-condition maxima into a process total. Check ordinary daemon
scrape alerts for a lost target: its CI configuration is unknown until observed
again. A healthy second replica can retain fleet visibility.

## Health query unavailable

`FaasCIHealthUnavailable` waits five minutes. Inspect PostgreSQL reachability,
pool health and the thirteen CI migrations through the normal release process.
Read the responding daemon's configuration and private metrics. A health read
error omits dependent evidence; it does not turn queue depth into zero.

If another readable replica exists for the same daemon, its fleet read can
suppress this warning. Investigate individual unhealthy targets through their
ordinary daemon and database diagnostics. Keep connection strings, provider
URLs, credentials and raw payloads out of incident notes.

## Enabled worker unavailable

`FaasCIWorkerUnavailable` waits five minutes while durable health is readable.
Inspect the worker's current configuration and its observed conditions:

- Blocked: check its wired dependencies. Dispatcher/retention need managed
  storage; source admission needs GitHub wiring; publication needs the App
  service and Check writer. This does not justify enabling compute gates.
- Degraded: inspect safe queue failure codes and normal daemon diagnostics.
  Worker observations sample the latest completed decision; they are not a
  per-attempt failure history.
- Stalled: the process can pulse but has not reported progress within the
  existing bound. Inspect bounded RPCs, database contention and loop lifetime.
- Stale or Unknown: inspect process lifetime, startup wiring and scrape data.
  There are eight diagnostic slots per kind. Additional groups can reconcile
  without a slot, so check slot exhaustion before inferring that all work stopped.

A configured-off worker should stay off until the authorized rollout or drain
procedure calls for it. Restarting a worker preserves execution/attempt identity;
never replay work to repair its diagnostic observation.

## Expired lease recovery

`FaasCILeaseRecoveryStalled` waits five minutes with the owning worker enabled.
Queue ownership is: executions → dispatcher, metadata admission → metadata
admission, automatic admission → automatic admission, metadata/execution Checks
→ their corresponding publishers, retirement → retention. Unapplied Check retry
intent is displayed under its execution publisher but has no separate work lease.

Inspect worker health, next-attempt/backoff state, retry budgets and native task
state. Expired means time eligible; authority, active-work limits, stopped-VM
receipts, output settlement and retirement checks still apply. Preserve execution
fences and immutable attempts. Do not clear lease columns, delete Jobs rows or
force object retirement to silence a warning.

## Dead Check delivery

`FaasCICheckDeliveryDead` waits five minutes while publication is configured on.
Inspect safe delivery status and current GitHub repository/installation authority:

```sh
gregalectl ci checks status --id EXECUTION_UUID
```

Fix access or provider availability first. For an eligible exhausted execution
Check, use its current retry generation and a stable new request key:

```sh
gregalectl ci checks retry --id EXECUTION_UUID --generation CURRENT_GENERATION --key STABLE_RETRY_KEY
```

The existing API rechecks authority and records bounded audited retry intent;
githubd consumes that intent. Applied does not mean published. An ignored or
revoked delivery cannot be forced through retry. Metadata Check failures use
their existing binding/source diagnostics; the execution retry API does not
rearm metadata delivery. Never submit another execution to repair publication.

## Sustained quota pressure

`FaasCIQuotaPressure` waits ten minutes at or above 80 percent of an existing
global allowance. Execution/dispatch capacity requires the dispatcher on.
Storage pressure requires the dispatcher or retention on; intentional pauses
keep the work-specific warning quiet while retained usage remains visible.

Inspect active execution, lease, storage byte/object/I/O and permanent journal
usage/headroom. Allow normal cancellation, settlement and safe retention to
drain live work. Archive eligible terminal executions through the existing
operation when account detail history fills. Archival cannot retire unsafe data
or remove unfinished delivery intent.

Deleted payload tombstones and authenticated source deduplication evidence keep
their permanent journal charge. Never delete or reset that evidence to create
capacity. Plan a separately reviewed retention/capacity change when permanent
allowances fill. High oldest-item age alone is not a delivery/queue latency SLO:
an old retained item can become pending again.

## Recovery and release

Warnings clear when their condition resolves or its owning path is deliberately
paused. Status panels use current samples and show Unknown when evidence is
missing; historical charts preserve gaps. Confirm the observed state, then
resume only through the existing authorized configuration process.

Validate the implementation in a development checkout with:

```sh
make test-ci-observability
```

This evaluates synthetic Prometheus input and dashboard contracts. It performs
no deployment, notification, image publication or native/provider qualification.
Do not change production flags as part of this check.
