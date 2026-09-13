# FaasDomainDoctorStalled

The same poller owns TXT verification. Inspect `apid_domain_verification_cycles_total`,
`apid_domain_verification_batch_size`, `apid_domain_verification_backlog`,
`apid_domain_verification_oldest_due_seconds`, and
`apid_domain_verification_results_total`. A bounded batch is expected; a growing
oldest-due age with no cycle increments indicates a stalled loop. Failed TXT
lookups back off from 30 seconds to one hour and expire after seven days.
Customers can re-arm an expired pending challenge with
`POST /v1/domains/{domain}/retry`; this resets its attempts and seven-day window.

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `apid_domain_doctor_oldest_observation_seconds` (gauge),
`apid_domain_doctor_skipped_flag_disabled_total` (counter).
ADR: ADR-120 (Domain Doctor) Tier A1 (operators observe the
doctor on a dashboard + an alert fires when it goes stale).
Severity: page on `FaasDomainDoctorStalled` (≥26m without a
successful pass). Warn on `FaasDomainDoctorStretched` (≥90s
between passes; cadence is broken but the loop is alive).
Info on `FaasDomainDoctorDisabledByOperator` (operator has
explicitly turned the doctor off via `FAAS_DOMAIN_DOCTOR_ENABLED`
— no page, just an annotation so a customer "why is my doctor
not running?" ticket can be triaged without paging on-call).

## Symptom

The per-domain doctor probe pass (cmd/apid/dns_poller.go::
runDoctorOnce, ADR-120) is failing to keep up. The doctor is a
30 s ticker that runs five Render-style checks (DNS record
found / points to Gregale / TLS certificate / CAA permits /
IPv6 conflict) against every row in
`domain_doctor_observations` and writes a single observation
row per domain. Three signals stack:

- **`apid_domain_doctor_oldest_observation_seconds` (gauge)**:
  the wall-clock age of the oldest observation row at the
  moment each pass completes. A value of 0 means the loop just
  ran against an empty table (cold start). A value of ~30 s
  means the loop is healthy (one full tick cycle between
  passes). A value stuck above 30 s means the loop is running
  but a single row is failing to refresh; Prometheus alerts
  on **staleness** instead: `time() − timestamp(gauge) > 1560`
  (page, `FaasDomainDoctorStalled`) fires when the loop hasn't
  completed a pass in >26m (52x the 30 s cadence with 25.5 min
  slack for a Postgres hiccup that delays the next good pass
  by ~50 passes). A second alert at `> 90` (warn,
  `FaasDomainDoctorStretched`) trips when the cadence has
  stretched past 3x the interval but the gauge IS still
  updating (the loop is alive but degraded — likely a
  contention on the MIN(observed_at) query or a single
  tenant's batch starving the loop).
- **`apid_domain_doctor_skipped_flag_disabled_total` (counter)**:
  bumps once per dns_poller tick when the operator has set
  `FAAS_DOMAIN_DOCTOR_ENABLED` to a falsy value (0/false/no/off).
  The `FaasDomainDoctorDisabledByOperator` (info) alert fires
  when the rate is non-zero for 1h, surfacing the deliberate
  opt-out to the customer's "my doctor is not running" ticket
  path without paging on-call.
- **Per-domain dashboard rendering**: the `/dashboard/apps/{slug}/domains/{domain}/doctor`
  page (added in ADR-120 Tier A2) renders the per-check
  status. If `ObservedAt` is older than 5 min (the env-driven
  `FAAS_DOMAIN_DOCTOR_TTL_SECONDS` threshold) the page renders
  with `stale: true` and the customer's `gregale domains
  doctor <domain>` returns the same flag in the JSON output.
  A persistent "stale" badge is the customer-visible signal
  that this runbook's alert will eventually fire.

When the doctor is stalled, customers see "Last check Xm ago"
indefinitely on the dashboard, `gregale domains doctor
<domain>` returns rows that are older than the TTL, and the
`gregale domains verify <domain>` synchronous path still
works (it's a separate port-443 dial path, not gated on the
poller — but it does not update `domain_doctor_observations`
either).

## Triage

Walk top-down. The three signals are designed to compose:

1. **Is the loop even alive?** Read
   `apid_domain_doctor_oldest_observation_seconds`. If it's
   present and updating (the gauge's `timestamp()` keeps moving
   forward on each Prometheus scrape), the loop is firing
   passes — go to step 2. If it's missing or stale (the gauge
   froze at some earlier timestamp), the loop has not
   completed a successful pass since boot. Check apid logs for
   `dns_poller: list domains for doctor failed` (the List
   helper's error path emits this at every tick when the
   SELECT against `custom_domains` UNION `tenant_hostnames`
   fails). Restart apid (`systemctl restart apid`); the
   goroutine's first-pass-immediate path fires on boot. If
   still missing, escalate to on-call with the apid log
   bundle.

2. **Is the doctor explicitly off?** Check
   `apid_domain_doctor_skipped_flag_disabled_total`'s rate. A
   non-zero rate means the operator has set
   `FAAS_DOMAIN_DOCTOR_ENABLED` to a falsy value on the apid
   host. This is the deliberate opt-out path (post-ADR-120
   Tier A3 the default is on; the env var can still disable
   it). The `FaasDomainDoctorDisabledByOperator` info alert
   annotates the same signal — if it's firing on the same
   alertmanager page as `FaasDomainDoctorStalled`, the
   stalled alert is a consequence of the explicit disable, not
   a loop bug. To re-enable, unset the env var (or set it to
   1/true/yes/on) and bounce the apid (`systemctl restart
   apid`).

3. **Is the cadence stretched but alive?** Read
   `apid_domain_doctor_oldest_observation_seconds`'s
   `timestamp()` delta on consecutive Prometheus scrapes. A
   delta of ~30 s means the loop is healthy. A delta of 60-90
   s means a single tenant's batch is dominating the 50/batch
   budget (`pkg/state/pgstore.go::ListAllCustomDomainsForDoctor`
   walks the union of `custom_domains` + `tenant_hostnames` —
   a single tenant with thousands of domains can starve the
   loop). To investigate, query Postgres directly:

   ```sql
   select count(*) from custom_domains
   union all
   select count(*) from tenant_hostnames;
   ```

   A single-tenant skew (>10x the median tenant) is the
   smoking gun. The remediation is in PR-121 (per-app
   batching); for now, document the skew in
   #operations-channel and let the next 30 s pass clear the
   backlog.

4. **Did the gauge freeze despite no error in the log?** This
   is the rare path where `runDoctorOnce` is returning
   successfully but `emitDoctorOldestObservationGauge` is
   failing to read MIN(observed_at). Check apid logs for
   `dns_poller: oldest doctor observation read failed`. The
   failure mode is a transient Postgres connection-pool
   exhaustion — the gauge is left untouched (previous value
   retained) so a transient DB hiccup doesn't false-page
   on-call. If the log is non-empty for >5 min, escalate.

5. **Is there a customer-side issue masquerading as a
   stalled loop?** A single stuck domain can pin the gauge
   (the gauge is the OLDEST row's age — if one customer's
   `points_to_gregale` probe is timing out, the row's
   observed_at won't refresh). Check the dashboard for the
   per-domain `ObservedAt` column; a single row older than
   5 min is the smoking gun. The remediation is to delete
   the stuck domain (`gregale domains remove api.example.com`)
   and have the customer re-add it.

If none of the above pin the issue, escalate to on-call with
the apid log bundle, a `psql` snapshot of the
`domain_doctor_observations` table, and the last 30 minutes
of `apid_domain_doctor_oldest_observation_seconds` from
Prometheus.

## Recovery

The recovery is automatic once the loop is unblocked: the
30 s ticker keeps firing, the next successful pass Sets the
gauge, and Prometheus's `time() − timestamp(gauge)` expression
resolves within 30 m. No operator action is required to clear
the alert beyond fixing the underlying issue identified in
triage. If the alert does NOT clear after 30 m of healthy
gauge updates, check that the alertmanager routing config
has not flipped the alert to a silenced state during the
investigation.

## Prevention

- The `FAAS_DOMAIN_DOCTOR_ENABLED` env var is the deliberate
  opt-out — operators can disable the doctor without
  bouncing the apid by setting the var to 0/false/no/off.
  Setting it back to 1/true/yes/on (or unsetting it, post-Tier-A3)
  re-enables the doctor on the next dns_poller tick.
- The single-registry pattern demands the gauge + counter
  fields are present on every daemon's OpsMetrics, but only
  apid increments since only apid runs the doctor loop. A
  rollout that omits the new fields on a single host will
  fail this alert's `for: 30m` window only on the missing
  host — the gauge's per-instance series will be absent in
  Prometheus's federation.

## Related

- ADR-120 (Domain Doctor), `docs/adr/120-domain-doctor.md`.
- `pkg/api/flags.go::DomainDoctorEnabled` — the flag's
  default-on (post-Tier A3) and explicit-off token set.
- `cmd/apid/dns_poller.go::runDoctorOnce` /
  `emitDoctorOldestObservationGauge` / `emitDoctorSkip` — the
  loop and the two metric-emission helpers.
- `pkg/dashboard/templates/domain_doctor.html` — the
  per-domain dashboard page that surfaces "Last check Xm ago".
- `docs/runbooks/FaasAuditRetentionExhaustion.md` — the
  precedent this runbook mirrors.
