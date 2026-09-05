# FaasLogArchiveShipperDegraded

Source: `pkg/fcvm/logbuf` eviction → `cmd/vmmd/log_archive.go` (compute-side
producer + shipper) and `pkg/logarchive/shipper.go` (apid in-process shipper) +
`cmd/gatewayd-internal/app_logs_archive.go` (PR-B read-back handler).
Metrics use the daemon prefix (`apid_` or `vmmd_`):
`<daemon>_log_archive_files_uploaded_total{status}`,
`<daemon>_log_archive_bytes_uploaded_total`,
`<daemon>_log_archive_failures_total{reason}`,
`<daemon>_log_archive_local_bytes`,
`<daemon>_log_archive_local_bytes_max`,
`<daemon>_log_archive_flush_duration_seconds`,
`<daemon>_log_archive_upload_duration_seconds`.
For control-plane checks use the `apid` prefix and `apid` service below; on a
compute host substitute the `vmmd` prefix and `faas-vmmd` service.
Issue: #562.
Severity: page (ticket-tier at warn; page at critical when local
spool approaches `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX`).

## Symptom

The per-Firecracker-instance log archive pipeline is degraded.
Three failure shapes show up first:

1. **No new objects in the bucket.** The apid shipper writes
   gzip+JSONL objects under `faas-logs/{instance}/{YYYY}/{MM}/
   {DD}.jsonl.gz`. A quiet bucket with non-zero
   `*_log_archive_local_bytes` (the on-disk spool is
   growing) is the canonical sign of a bucket outage.
2. **`*_log_archive_failures_total{reason="auth"}` spiking.**
   SigV4 signing failed — usually the unsealed creds envelope
   at `/etc/faas/secrets/storage-box/archive-creds.json` is
   stale or rotated. Customers get no archive back-fill and
   the read-back handler returns 503
   `log_archive_unconfigured` once the bucket falls out of
   sync.
3. **`*_log_archive_local_bytes` near the cap
   (`FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX`, default 10 GB).**
   Spool is full; the shipper is now in back-pressure mode
   and new per-instance rings will spill to the local
   filesystem (`reason="spool_full"`). `reason="queue_full"` means
   vmmd's bounded eviction-to-disk queue is saturated.

A fourth shape is observable through the `FaasLogArchiveShipperDegraded`
alert: `reason="throttle"` means the vendor is rate-limiting the
bucket. See *Recover #3* below.

## Verify

```bash
# (a) Did anything upload in the last 5m?
curl -fsS --data-urlencode "query=sum(rate(apid_log_archive_files_uploaded_total{status=\"ok\"}[5m]))" \
  'http://127.0.0.1:9095/api/v1/query'

# (b) What's the current spool size?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=apid_log_archive_local_bytes'

# (c) Failure breakdown by reason (the {reason} label set is
#     bounded — see pkg/logarchive/metrics.go:114).
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum+by+%28reason%29+%28rate%28apid_log_archive_failures_total%5B5m%5D%29%29'

# (d) Read apid's loopback metrics endpoint directly to bypass
#     Prometheus (handy when the alertmanager is also down).
curl -fsS 'http://127.0.0.1:9101/metrics' | grep -E '^apid_log_archive_'

# (e) Bucket-side: are objects from the last hour present?
aws s3 ls --recursive s3://<bucket>/faas-logs/ \
  | awk '$1 >= "'"$(date -u -d '1 hour ago' +%Y-%m-%dT%H)"'"' | head -20
```

Call (a) is the heartbeat — if `status="ok"` is non-zero, the
shipper is making progress. If it's zero AND (b) is growing,
the shipper is stuck; jump to *Recover #1*. If (c) shows a
single reason dominating, jump to that section in *Recover*.

## Check

### Spool-side evidence

```bash
journalctl -u apid --since '-15m' --no-pager | grep -i 'logarchive\.\(first_purge\|purge_failed\|purged\|spool_full\|upload_failed\)'
```

The shipper emits structured slog lines under `logarchive.*`.
The two operators actually need are:

- `logarchive.upload_failed err=<reason>` — single-object
  upload failed; the sealed file is left as `.upload` for retry.
- `logarchive.purged files=N retention_days=N` — local
  purger swept the spool. **No objects past retention days
  should still be on disk**; if they are, the in-process
  PurgeInterval is misconfigured.

### Creds envelope

```bash
sudo stat -c '%a %U:%G %n' /etc/faas/secrets/storage-box/archive-creds.json
# expected: 0400 root:root /etc/faas/secrets/storage-box/archive-creds.json
```

The `log_archive` ansible role asserts this on every run
(`deploy/ansible/roles/log_archive/tasks/main.yml:35-50`).
A drift to `0640 root:root` or `0400 faas:faas` means both
apid and gatewayd-internal will fail their next
`$CREDENTIALS_DIRECTORY` lookup with permission denied.

### Read-back handler evidence

```bash
journalctl -u faas-gatewayd-internal --since '-15m' --no-pager \
  | grep -i 'log archive'
```

The PR-B handler logs one of:

- `log archive disabled (FAAS_LOG_ARCHIVE_BUCKET unset)` —
  the envelope is missing the bucket field. The shipper is
  in the same state; this is the disabled-path branch.
- `log archive read-back armed endpoint=... region=...
  bucket=...` — the handler is up and reading from the
  bucket. Customers can hit `?archive=1`.
- `log archive config parse failed` — the envelope has a
  bad key (the JSON is unparseable). Run
  `gregalectl backup unseal-archive-creds` again.

### Bucket lifecycle policy

The plan-gated retention (Hobby 7d / Pro 30d / Scale 90d)
is enforced **at read time** by gatewayd-internal's
`withinRetention` check — refusing `?date=` values outside
the per-plan window with 403 +
`log_archive_retention_exceeded`. The bucket itself does
NOT know about per-plan retention, so the operator MUST
attach a lifecycle policy at bucket-provision time to
cap the storage bill. The recommended shape:

```json
{
  "Rules": [{
    "ID": "faas-log-archive-expire",
    "Status": "Enabled",
    "Filter": { "Prefix": "faas-logs/" },
    "Expiration": { "Days": 90 },
    "NoncurrentVersionExpiration": { "NoncurrentDays": 7 }
  }]
}
```

`Days: 90` matches the Scale-plan retention floor. Hobby
and Pro customers will see a wider effective window than
their plan promises — but the API refuses the early read,
so the *visible* retention stays plan-correct. The bucket
is over-storing, not under-deleting. Tune `Days` up if
you onboard an Enterprise tier with a longer floor.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasLogArchiveShipperDegraded' \
  --duration=2h \
  --comment='investigating log archive shipper degradation; bucket outage'
```

## Recover

In order — each step assumes the previous didn't help:

1. **Restart apid to clear any stuck goroutine.** The shipper
   is a single long-running loop
   (`pkg/logarchive/shipper.go:Run`); a SIGTERM closes the
   iterator and a SIGKILL during a PutObject leaves the
   `.upload` file on disk for the next boot to retry.
   ```bash
   sudo systemctl restart apid
   ```
   Wait 60s, then re-run the verify queries. If
   `apid_log_archive_local_bytes` is dropping, the restart
   unstuck the shipper.

2. **Rotate the unsealed creds envelope.** The most common
   auth failure is a stale access key after a vendor
   rotation. Run the unseal CLI (PR-A leaf):
   ```bash
   sudo gregalectl backup unseal-archive-creds
   sudo systemctl restart apid
   ```
   The unseal reads the host.age-sealed form, decrypts it
   into `/etc/faas/secrets/storage-box/archive-creds.json`
   (mode `0400 root:root`), and `systemctl restart apid`
   forces the shipper to re-read the new envelope on its
   next boot. Run the same restart on
   `faas-gatewayd-internal` so the read-back handler picks
   up the new key on the same boot — the two daemons
   should never disagree on the envelope.

3. **Vendor throttle.** Sustained
   `apid_log_archive_failures_total{reason="throttle"}` means
   the vendor is rate-limiting the bucket. Three options,
   in order of preference:

   a. **Wait.** The shipper backs off on 4xx `SlowDown` /
      `TooManyRequests` per the standard SigV4 retry
      guidance. A 5-10 minute pause clears a transient
      burst.

   b. **Tune `FAAS_LOG_ARCHIVE_INTERVAL`** (default 5m).
      Set to 10m on the apid unit's drop-in
      (`/etc/systemd/system/faas-apid.service.d/`) to halve
      the request rate. Note: the in-process PurgeInterval
      is also driven by this knob; check
      `pkg/logarchive/config.go` before coupling them.

   c. **Open a ticket with the vendor.** Provide the
      `apid_log_archive_upload_duration_seconds` p99 from
      call (d) above — sustained p99 > 2.5s suggests the
      vendor's edge latency has degraded even when the
      throttle label is not yet firing.

4. **Spool full.** If
   `apid_log_archive_local_bytes` is approaching
   `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX` (default 10 GB) AND
   `apid_log_archive_files_uploaded_total{status="err"}` is
   high, the bucket is unreachable for a sustained window
   and the spool has filled. Two options:

   a. **Raise the cap** by editing the apid unit's
      drop-in to set `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX=20G`
      (or similar). This buys time but does not fix the
      root cause; the spool will keep growing.

   b. **Stop accepting new rings** by lowering the
      shipper's per-tick batch size. Not currently
      exposed; file an ADR before raising the cap, since
      raising the cap is a tenant-side bill decision
      (CLAUDE.md: "new quota/limit → add to
      pkg/api/limits.go" — applies to operator-side caps
      too; the LoadCredential envelope is the analogue).

5. **Bucket-side: lifecycle policy.** If `aws s3 ls` shows
   objects older than 90 days, the operator hasn't attached
   the lifecycle policy described above. Attach it via
   `aws s3api put-bucket-lifecycle-configuration
   --bucket <bucket> --lifecycle-configuration file://
   policy.json`. Existing objects are NOT deleted
   retroactively by a new policy; run a one-shot
   `aws s3 rm --recursive --exclude '*' --include
   'faas-logs/*'` after the policy lands if the bucket is
   already over budget.

6. **Read-back handler stuck (PR-B).** If
   `apid_log_archive_files_uploaded_total{status="ok"}` is
   climbing but customers still report `503
   log_archive_unconfigured` on `?archive=1`, the
   gatewayd-internal daemon's S3 client is missing the
   same envelope. Run:
   ```bash
   sudo systemctl restart faas-gatewayd-internal
   ```
   The unit's drop-in
   (`/etc/systemd/system/faas-gatewayd-internal.service.d/
   99-faas-log-archive.conf`) re-loads the envelope on the
   next boot. If the customer error persists after the
   restart, the envelope has rotted; run
   `sudo gregalectl backup unseal-archive-creds` and the
   restart again.

## Escalation

- **Bucket outage > 30m:** open a vendor ticket with the
  request IDs from `apid_log_archive_failures_total` (the
  shipper logs the X-Amz-Request-Id from each 5xx). Local
  spool caps will trip within ~1h under default 5m flush +
  normal customer load; raise `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX`
  to buy time per *Recover #4*.
- **Creds envelope rotted AND `gregalectl backup unseal-archive-creds` fails:** the host.age-sealed source form is
  unreadable. Re-fetch from the sealed secrets bucket
  (`/etc/faas/secrets/host.age` + the cosign-keypair) and
  re-run the unseal. If the cosign-keypair is also gone,
  page the security on-call — issue #562's G2-lean
  sealed-at-rest story assumes the keypair is durable.
- **Sustained `auth` failures AFTER rotation:** the bucket
  policy may have denied the new access key. Attach the
  new key's ARN to the bucket policy (`s3:GetObject`,
  `s3:PutObject`, `s3:ListBucket` on the `faas-logs/*`
  prefix) and retry.
