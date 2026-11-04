# FaasGeoIPDatabaseStale

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-091 amendment 2026-11-02, later).
Metric: `geoip_db_build_timestamp_seconds` (gatewayd-internal; the loaded
country database's build time, NaN when none is loaded).
Design: ADR-091 D21, ADR-143 (the `geoip` role).
Severity: warn.

## Symptom

gatewayd-internal resolves client countries for `kind=geo` rules and
`country` match conditions from the DB-IP country database the `geoip` role
stages at `/var/lib/faas/geoip/dbip-country-lite.mmdb`. The release is pinned
by month and checksum (`faas_geoip_release`) and nothing refreshes it on its
own (`FAAS_GEOIP_AUTO_REFRESH` stays 0). The loaded file was built more than
75 days ago: two monthly releases were skipped. Addresses moved to another
country since then still resolve to the old one, and newly allocated ones
resolve to none. A deny-only geo rule lets an unresolved address through; an
allow list refuses it.

## Verify

```promql
(time() - geoip_db_build_timestamp_seconds) / 86400
```

```bash
journalctl -u faas-gatewayd-internal --no-pager | grep 'geoip: loaded' | tail -1
grep faas_geoip_release deploy/ansible/roles/geoip/defaults/main.yml
```

The boot log line names the file's build date.

## Check

- Every gateway on the same release fires together: the pin is old. Bump it.
- One gateway fires alone: that node was not re-bootstrapped after the last
  bump, or the role failed on it. Compare the file's SHA-256 with
  `faas_geoip_sha256`.

## Silence

```bash
amtool silence add --matchers='alertname=FaasGeoIPDatabaseStale' --duration=72h \
  --comment='geoip release bump in review'
```

## Recover

1. Download the current month's `dbip-country-lite-YYYY-MM.mmdb.gz` (and the
   ASN file, which shares the cadence), record the SHA-256 of the `.gz` and of
   the decompressed `.mmdb`, and update `faas_geoip_release`,
   `faas_geoip_gz_sha256` and `faas_geoip_sha256` (and the `asn` trio) in
   `deploy/ansible/roles/geoip/defaults/main.yml` in a PR.
2. After it merges, re-run `bootstrap-compute` (and the control plane if it
   serves edge traffic). The role restarts gatewayd-internal when the file
   changes; the gauge then shows the new build time.
