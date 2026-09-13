# FaasInternalMTLSRenewal

The platform reads every active leaf below `/etc/faas/tls` on each host. A
daily serialized workflow renews only hosts inside the 30-day issuance
threshold. The control plane keeps the fleet CA private key and produces a
separate trust-only bundle for every compute identity.

## Signals

`faas_internal_mtls_leaf_earliest_expiry_timestamp_seconds` is the earliest
real leaf `NotAfter` for each host and daemon. The metrics freshness series
proves the collector read the complete role-specific leaf set. Renewal success,
failure, and partial-state series come from
`/var/lib/faas/pki-renewal/status.json`.

Check the affected host and the scheduled GitHub run:

```sh
systemctl status faas-internal-pki-metrics.timer
journalctl -u faas-internal-pki-metrics.service --since '30 minutes ago'
cat /var/lib/node_exporter/textfile_collector/faas_internal_pki.prom
sudo /usr/local/bin/gregalectl pki status --root-dir /etc/faas/tls --box-role compute-only
```

Pass the host's inventory `faas_node_name` and private transport address as
`--cn` and `--transport-san` when checking a compute node manually.

## Recovery

Re-run the **internal-pki-renewal** workflow. It issues on the single
`control_plane` inventory host after proving that inventory exactly matches
the active compute registry. Each target exports only its public CA and leaves.
The issuer preserves safe leaves, renews only leaves inside the threshold, and
the target reloads only affected active consumers before running the same
dependency-aware readiness checks as a fleet rollout.

Never copy `/etc/faas/tls/ca/ca.key` to a compute node. Do not replace the CA
through this leaf-renewal path. A CA mismatch is rejected before any live file
changes. If `phase=activation_pending`, rerunning the workflow resumes vmmd's
database fingerprint compare-and-swap and the affected consumer reloads. If an
on-disk transaction journal remains, run `gregalectl pki recover-install`; it
restores an interrupted batch or cleans a committed batch before renewal.

## Verification

The workflow must finish successfully for every inventory host. Confirm that
each enabled daemon is active and ready, then force one metrics refresh:

```sh
sudo systemctl start faas-internal-pki-metrics.service
sudo systemctl --failed
sudo /usr/local/bin/gregalectl doctor
cat /var/lib/faas/pki-renewal/status.json
```

The last success timestamp must be newer than the last failure, `partial` must
be false, and every expiry series must be more than 30 days in the future.
Prometheus must also report `up{job="node-compute"} == 1` for every active
compute node.

## Escalation

If issuance fails on the control plane, verify the CA certificate and private
key modes and validity without moving the key. If installation rejects the
candidate, compare the public CA fingerprints and inventory identity. A
different CA requires the separate fleet trust-root rotation procedure.
