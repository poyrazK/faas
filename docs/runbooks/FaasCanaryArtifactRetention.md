# FaasCanaryArtifactRetention

These alerts cover temporary production validation data under the declared
canary, native-CI, and diagnostic roots. The cleanup contract excludes release
directories, customer snapshots, runtime volumes, and builder caches.

## Signals

- `faas_canary_artifact_bytes` reports allocated filesystem bytes, including
  the real disk cost of sparse Firecracker images, and
  `faas_canary_artifact_count` reports the current bounded inventory.
- `faas_canary_artifact_oldest_age_seconds` reports the oldest creation time.
- `faas_canary_artifact_cleanup_total` counts successful lifecycle and orphan
  removals.
- `faas_canary_artifact_cleanup_failures_total` counts failed safety proofs and
  failed removals. A failed proof retains data.
- `faas_canary_artifact_last_sweep_timestamp_seconds` proves that the hourly
  systemd sweep is still completing.

## Triage

Identify the host from the Prometheus `instance` label, then inspect the timer,
the most recent sweep, and the durable owner records:

```sh
systemctl status faas-canary-artifact-sweep.timer
journalctl -u faas-canary-artifact-sweep.service --since '2 hours ago'
sudo find /var/lib/faas/canary-artifacts/records -maxdepth 1 -type f -print
sudo sed -n '1,160p' /var/lib/faas/canary-artifacts/records/*.json
```

Each record includes the owning run, host, kind, creation time, update time,
and state. An `active` record is protected for six hours. A terminal record is
eligible immediately. Unregistered artifacts expire after 24 hours. The five
GiB cap removes the oldest unregistered or terminal artifact after a two-hour
creation grace, while active leases remain protected.

If cleanup was deferred, verify whether the path is still referenced. The
helper checks the same live surfaces before every removal:

```sh
sudo systemctl list-units --state=active,activating,reloading,deactivating --no-pager
sudo grep -R -F '/path/from/record' /etc/systemd/system /run/systemd/system /usr/lib/systemd/system
sudo find /proc/[0-9]*/fd -lname '/path/from/record*' -print 2>/dev/null
```

## Recover

A referenced path is preserved. End the owning canary or transient test unit,
then run one guarded sweep and recheck the metrics:

```sh
sudo systemctl start faas-canary-artifact-sweep.service
sudo systemctl status faas-canary-artifact-sweep.service
cat /var/lib/node_exporter/textfile_collector/faas_canary_artifacts.prom
```

Do not delete a declared root wholesale. Register manual validation directories
before copying fixtures and call `faas-canary-artifacts finish` from every
success, failure, and cancellation path. The native GitHub workflows provide
the reference implementation.
