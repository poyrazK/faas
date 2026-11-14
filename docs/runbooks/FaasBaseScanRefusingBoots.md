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
| `critical` | The scan found a fix-available CRITICAL finding and its 72-hour grace (below) has ended, or the sidecar carries no grace start. A base that passed the release scan can fail here later, because the node scans with a newer vulnerability database |
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

## FaasBaseScanCriticalInGrace

> **Severity:** page. **Component:** vmmd. **Metric:** `vmmd_base_scan_grace_admits_total{base}`.

When a scan of a base first finds a fix-available CRITICAL finding, imaged
stamps `fixable_critical_since` in the sidecar. For
`api.BaseScanCriticalBootGrace` (72 hours) from that time vmmd keeps booting
the base so apps already serving on it keep waking, and counts each such boot
here. schedd refuses every new deployment on the base during the grace
(`scan_critical`, "Fixable CRITICAL vulnerability in base ext4"). A rescan of
the same generation keeps the original start, so rescans do not extend the
grace (deploy-path review P5).

1. Read the sidecar (Triage step 1): `fixable_critical_since` says when the
   grace ends (add 72 hours).
2. Find the CVEs and publish a patched base as for `critical` under Fix. The
   new generation's clean scan ends the alert.
3. If no patched base can ship before the grace ends, say so on the status
   page: every app on the runtime stops waking when it does.

## FaasBaseScanFailureHeld

> **Severity:** page. **Component:** imaged. **Metric:** `imaged_base_scan_failures_held_total{base, reason}`.

imaged renews a shared base's scan every two minutes. When a renewal of the same
bytes fails because of the scanner (`scanner_unavailable`, `scanner_invalid`) or
a storage read (`artifact_read`), imaged keeps the last complete scan of those
bytes in force instead of writing the `scan_failed` refusal, and counts the
failure here (bug hunt 2026-10-28 H102). Boots keep working. Once that scan is
older than `api.SharedBaseScanFailureGrace` (24 hours) the next failure
publishes the refusal and `FaasBaseScanRefusingBoots` follows.

1. Read imaged's log for `shared base scan failed; the last complete scan of
   these bytes stays in force`: it names the failure, the scan in force and when
   the base will be refused.
2. `scanner_unavailable` / `scanner_invalid`: check Grype on the node (`grype
   version`, `grype db status`) and the database download; a broken database
   update is the usual cause.
3. `artifact_read`: check the artifact store (GCS or the node cache) for the
   base key named in the alert.

Changed bytes (`artifact_mismatch`) and scans that complete with blocking
findings are never held: they refuse at once.
