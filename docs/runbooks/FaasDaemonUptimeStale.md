# FaasDaemonUptimeStale

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `<daemon>_daemon_uptime_seconds{daemon,instance}`. Severity: warn.

## Symptom

A daemon has reported more than 7 days of uptime for at least one hour.
This is a rollout hygiene signal: the process may have missed a release,
been excluded from a host-level restart, or remained intentionally pinned.
It does not by itself mean the daemon is unavailable.

## Verify

```bash
# Confirm the uptime and identify the host.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query={__name__%3D~%22.*_daemon_uptime_seconds%22}%20%3E%20604800'

# Compare the running build with the desired release.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query={__name__%3D~%22.*_daemon_build_info%22}'

systemctl status faas-<name>.service --no-pager
journalctl -u faas-<name>.service --since '-24h' --no-pager | tail -100
```

Check `version`, `git_sha`, and `build_time` from `daemon_build_info`. Compare
them with the release currently intended for the fleet. Also check whether a
maintenance freeze, capacity constraint, or an open incident explains why
the host was not restarted.

## Recover

If the daemon is behind the intended release, include its host in the next
controlled rollout and wait for `/readyz` to return 200 before moving on.
Do not restart a production daemon during an active incident unless the
deployment owner has approved the change.

If the old build is intentionally pinned, record the reason, owner, and
expiry in the change or incident, then silence the alert for that bounded
period. The alert should clear after the process reports a fresh uptime
below the threshold following the planned rollout.
