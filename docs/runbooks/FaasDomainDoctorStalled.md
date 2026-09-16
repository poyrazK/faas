# FaasDomainDoctorStalled

The domain doctor refreshes customer DNS and certificate observations every
30 seconds. Each cycle selects at most 128 of the oldest domains, interleaves
accounts so a large tenant cannot monopolize the batch, and uses at most 16
concurrent workers. The durable `observed_at` cursor lets the next cycle resume
where the previous cycle stopped and covers a healthy 1,256-domain fleet in
about five minutes.

The same poller owns TXT verification. Its separate signals are
`apid_domain_verification_cycles_total`,
`apid_domain_verification_batch_size`, `apid_domain_verification_backlog`,
`apid_domain_verification_oldest_due_seconds`, and
`apid_domain_verification_results_total`.

## Signals

- `apid_domain_doctor_cycles_total{outcome}` is the worker heartbeat. A cycle
  records `success`, `timeout`, or `error`. The counter only changes when the
  producer runs, so Prometheus scrapes cannot make a stopped worker look fresh.
- `apid_domain_doctor_batch_size` is the number selected in the latest cycle.
  A persistent value of 128 means the fleet has at least one full batch of due
  work; check the oldest-observation gauge to decide whether it is keeping up.
- `apid_domain_doctor_oldest_observation_seconds` measures customer-data age.
  It is zero when there are no observations. It measures backlog freshness,
  rather than worker liveness.
- `apid_domain_doctor_skipped_flag_disabled_total` increments when the runtime
  `domain_doctor_enabled` setting is false.

`FaasDomainDoctorStalled` pages when no successful cycle completes for ten
minutes. `FaasDomainDoctorStretched` warns when fewer than eight successful
cycles complete in five minutes for ten minutes. The normal rate is ten cycles
per five minutes. `FaasDomainDoctorDisabledByOperator` is informational.

## Triage

1. Compare the `success`, `timeout`, and `error` cycle counters. Errors before
   a batch starts usually indicate Postgres pool pressure or a failed batch
   query. Timeouts indicate slow DNS, TLS, or observation writes.
2. Check `journalctl -u faas-apid` for `list domains for doctor failed`,
   `upsert doctor observation failed`, and
   `oldest doctor observation read failed`.
3. Check the current work and customer-data age:

   ```promql
   apid_domain_doctor_batch_size
   apid_domain_doctor_oldest_observation_seconds
   sum by (outcome) (increase(apid_domain_doctor_cycles_total[10m]))
   ```

4. Confirm the feature is enabled in runtime configuration. If it is disabled,
   re-enable `domain_doctor_enabled`; the next 30-second tick resumes work.
5. If batches time out, check resolver latency and Postgres connections before
   restarting apid. A restart is safe because selection uses the stored
   observation timestamp rather than an in-memory cursor.

The customer-facing `/dashboard/apps/{slug}/domains/{domain}/doctor` page and
`gregale domains doctor <domain>` command report an observation as stale after
the configured TTL, which defaults to five minutes.

## Recovery

Recovery is automatic after the dependency is healthy. The next cycle advances
the counter and refreshes its selected observations. The alert resolves after
its Prometheus window contains enough successful cycles.

## Related

- ADR-120: `docs/adr/120-domain-doctor.md`
- Poller: `cmd/apid/dns_poller.go`
- Selection: `pkg/state/pgstore.go::ListCustomDomainsForDoctorBatch`
- Alert rules: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
