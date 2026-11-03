# FaasDeployVersionSkew

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `*_faas_deploy_version` and `*_daemon_build_info`. Severity: warn.

## Symptom

Prometheus has observed more than one daemon release version. This normally
means a rollout is in progress, stopped halfway, or left one host serving an
older build. Readiness can remain green during this condition, so the alert
fires without a hold duration and complements `FaasDaemonNotReady`.

## Verify

```bash
# How many release versions are currently visible?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=count(count%20by%20(version)%20({__name__%3D~%22.*_faas_deploy_version%22}))'

# Which daemon and host report each build?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query={__name__%3D~%22.*_daemon_build_info%22}'

# Which active nodes have reached the desired release and have fresh liveness?
gregalectl compute-nodes release-status \
  --desired-release <40-character-git-sha> \
  --timeout 2m \
  --json

# On an affected host, confirm the active unit and release link.
systemctl status 'faas-*.service' --no-pager
readlink -f /usr/local/bin/faas-<daemon>
```

Per-unit control-plane releases report `unit-<commit>` versions and are
excluded from this alert: they deliberately run beside their base full
release until the next one (docs/adr/1650-per-unit-control-plane-releases.md).
`readlink -f /opt/faas/current` on the control plane and its
`unit-versions.json` show which daemons a unit release replaced. A host
restarts a daemon only when its code changed, so one unit release can leave
different `unit-*` versions on different hosts, or an older one beside a
newer release: each names the commit the running binary was built from, and
`unit_code` records its code digest.

Use `daemon_build_info{daemon,instance,version,git_sha,build_time}` to map the
skew to a host and exact commit. A deployment that is actively rolling out
may legitimately trigger this alert; it should clear once every scrape
target reports the same release.

Use the `cd-platform` workflow for production releases and pass every active
compute node in its `compute_targets` JSON array. It runs the control-plane
stage, prepares compute nodes in parallel while they continue serving the old
release, activates the prepared nodes serially, and always publishes the
`release-status` report in the workflow summary. A successful preparation or
stage workflow alone is not a successful platform rollout.

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

## After a failed node canary

`cd-platform` ends each compute activation leg with a node canary (the
"Prove the activated node serves and builds through production ingress" step
of `cd-compute`). It deploys acceptance apps until the node it just activated
serves one and has built one, so a failure stops the rollout with that node on
the new release and every later node on the old one. This alert fires until
the fleet is on one release again.

1. Read the step log. `node canary deployment for <node> failed` carries the
   failed deployment's receipt, CLI stderr and build log tail. `did not see
   <node> serve and build` prints the last `gregalectl release-acceptance
   verify-node` report: `instance` false means nothing placed on the node,
   `build` false means no build on the node succeeded.
2. If the node cannot serve or build, take it out of placement with
   `gregalectl deploy rollback-node --node <node> --yes`. It drains the row
   and leaves the host's files and services for forensics; the other nodes
   keep serving on the old release.
3. Fix forward with a new release, or re-run the failed leg once the cause
   was transient (`gh run rerun <run-id> --failed`); a later
   `join-node --resume` returns a rolled-back node to the desired state.
