# FaasDeployVersionSkew

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `*_faas_deploy_version` and `*_daemon_build_info`. Severity: warn.

## Symptom

Prometheus has observed more than one daemon release version for 10 minutes.
This normally means a rollout is still in progress, stopped halfway, or
left one host serving an older build. Readiness can remain green during this
condition, so this alert complements `FaasDaemonNotReady`.

## Verify

```bash
# How many release versions are currently visible?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=count(count%20by%20(version)%20({__name__%3D~%22.*_faas_deploy_version%22}))'

# Which daemon and host report each build?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query={__name__%3D~%22.*_daemon_build_info%22}'

# On an affected host, confirm the active unit and release link.
systemctl status 'faas-*.service' --no-pager
readlink -f /usr/local/bin/faas-<daemon>
```

Use `daemon_build_info{daemon,instance,version,git_sha,build_time}` to map the
skew to a host and exact commit. A deployment that is actively rolling out
may legitimately trigger this alert; it should clear once every scrape
target reports the same release.

## Recover

1. Check the deployment controller and CI job for a failed or interrupted
   host.
2. Complete the rollout on the stale host, or roll the newer hosts back to
   the last known-good release. Use the normal deploy controller so systemd,
   readiness, and the release symlink move together.
3. Confirm that the distinct-version query returns `1` and that every
   affected daemon's `daemon_build_info` has the expected `git_sha`.

Do not silence this alert for an unexplained mixed-version fleet. If the
versions are intentionally different, document the compatibility boundary
and silence only for the planned rollout window.
