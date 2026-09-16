# Canary artifact retention

This role installs the inventory and cleanup contract for production canaries,
native CI workspaces, and operator-created diagnostic fixtures. It runs on
control-plane, compute, and designated native-acceptance hosts.

Create the artifact directory first, then register it before copying fixtures:

```sh
sudo faas-canary-artifacts register \
  --path /opt/faas/canaries/example-123 \
  --run-id 123-1 \
  --kind rollout-canary
```

Every success, failure, and cancellation path must finish the record:

```sh
sudo faas-canary-artifacts finish \
  --path /opt/faas/canaries/example-123 \
  --state success
```

`finish` records the terminal state before removal. It leaves the artifact for
the hourly timer when a live process or active systemd unit still references
the path. Active records have a six-hour lease. Unregistered artifacts have a
24-hour retention window. Unregistered terminal artifacts older than two hours
are also eligible for oldest-first removal when the declared roots exceed the
five-GiB host cap. Active leases remain protected from pressure cleanup.

The closed path set lives in `defaults/main.yml`; arbitrary paths are rejected.
The set covers `/opt/faas/canaries`, native CI and `gregale-*` workspaces in
`/var/tmp`, restore-failure inputs, and stale diagnostic cache fixtures. It does
not include releases, customer snapshots, build caches, or runtime volumes.

The helper publishes node-exporter textfile metrics under
`/var/lib/node_exporter/textfile_collector`. See
`docs/runbooks/FaasCanaryArtifactRetention.md` for alert triage.
