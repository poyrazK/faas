# FaasReleaseIntegrityFailed / FaasReleaseIntegrityStale / FaasReleaseManifestDiverged

Every control-plane and compute host runs `gregalectl release verify` hourly
(`faas-release-integrity.timer`,
[ADR-20261110183000](../adr/20261110183000-tetragon-runtime-detection.md)).
It checks:

- the current release (`/opt/faas/current`) against `release-manifest.json`,
  the manifest embedded in and signed with the release tarball (and, on the
  control plane, the deployment bundle's `manifest.json`);
- every process whose executable is under `/opt/faas`: it must run from an
  installed release that verifies, and from a file not replaced or removed
  since it started. `prestage/`, `cloudflared/` and `canaries/` are skipped.

It writes `faas_release_integrity_ok`, `faas_release_integrity_failures`,
`faas_release_integrity_checked_timestamp_seconds` and
`faas_release_manifest_info{release,digest}` to the textfile collector and
logs each failure to the journal as `faas-release-integrity`, which Alloy
ships to Loki under `{job="faas-security", ident="faas-release-integrity"}`.

## FaasReleaseIntegrityFailed (page)

1. Read the failures in Loki (they survive a wiped host), or on the host:
   `journalctl -u faas-release-integrity -n 50` and
   `sudo gregalectl release verify --json`.
2. Known benign causes:
   - an audit hotfix (`scripts/ops/request_evidence_hotfix_host.py`) running a
     daemon from `/opt/faas/hotfixes/`: it is an unsigned build, which is what
     this check reports. Confirm the hotfix was intended, then roll the host to
     a release.
   - a release directory edited by hand during an incident.
3. Otherwise treat the host as compromised: a platform binary differs from
   the signed release, or a daemon runs a binary that was swapped after it
   started. Drain it and follow
   [compute-node-compromise](compute-node-compromise.md) (compute) or the
   control-plane incident procedure. Keep the host for forensics.

## FaasReleaseManifestDiverged (page)

Hosts on the same release must report the same signed-manifest digest. A host
whose digest differs had its `release-manifest.json` changed after install,
for example to cover a swapped binary. Find it:

```promql
faas_release_manifest_info{release="<release>"}
```

and handle it like FaasReleaseIntegrityFailed.

## FaasReleaseIntegrityStale (warn)

The check has not reported for 3 hours. Look at
`systemctl status faas-release-integrity.timer faas-release-integrity.service`.
A stopped or removed timer on a host that also shows a security event is part
of that event.

## Limits

The check runs on the host it checks: root on that host can stop it or fake
its output. What it adds is evidence off the host (the stale alert, the
journal lines in Loki, the digest the other hosts report), not proof against
an attacker who controls the host from the start of the hour.
