# Runbook · FaasBaseScanRefusingBoots

> **Severity:** page. **Component:** vmmd. **Metric:** `vmmd_base_scan_refusals_total{base, reason}`.
> **Gate:** `Manager.bringUpScanCheck` in `pkg/fcvm/manager.go` (issue #299).

## Symptom

vmmd has kept refusing boots on one base ext4 for fifteen minutes. Every app on that
runtime fails to deploy ("snapshot prime failed ... scan_critical") and every
cold boot or restore on it fails. Customers see the deploy fail or their app
return 503.

## Why

imaged scans each staged base with Grype and writes a sidecar under
`scans/<base>.scan.json`; vmmd reads it before every boot. The `reason` label
says why the boot was refused:

| reason | Meaning |
| --- | --- |
| `critical` | The scan found a fix-available CRITICAL finding. A base that passed the release scan can fail here later, because the node scans with a newer vulnerability database |
| `sidecar_missing` | No sidecar for the base (not staged yet, or the storage read failed) |
| `incomplete` | The sidecar has no CRITICAL total |
| `scan_failed` | imaged's Grype scan of the base failed, and it wrote the fail-closed `CRITICAL=9999` placeholder (`wire.BaseScanFailedCount`). Customers see "Base image scan unavailable" |
| `generation_mismatch`, `generation_unavailable` | The sidecar names a different base generation than the release configured, or the node could not adopt the configured one |

## Triage

1. Read the sidecar: `gcloud storage cat gs://<artifacts bucket>/scans/<base>.scan.json`.
   It holds the image ref, the total findings and `fix_available_findings`.
2. For `critical`, find the CVEs: run Grype against the image ref in the
   sidecar on a build host (not on a control-plane or compute node).
3. For the other reasons, check `faas-imaged` logs for the base stage and
   `faas-vmmd` logs for `scan sidecar` on the node in the alert.

## Fix

- `critical`: rebuild the runtime image (for Wolfi bases a rebuild picks up
  the patched packages), release it, and let imaged stage the new base. Do not
  edit the sidecar by hand.
- Missing or stale sidecar, or `scan_failed`: check why Grype failed in the
  `faas-imaged` logs (`grype scan failed; writing fail-closed sidecar`; a full
  `/srv/fc` or a stale vulnerability database are the usual causes), fix that,
  then restart `faas-imaged` on the node so it restages and rescans the base;
  the fail-closed `CRITICAL=9999` sidecar is replaced on restart.
