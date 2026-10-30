# ADR-20261028102540 · Reuse a scan while its bytes, scanner and database are unchanged

- **Status:** Proposed
- **Date:** 2026-10-28
- **Amends:** [ADR-595](595-inherited-application-standards.md), the
  shared-base scan record and its renewal ("only a fresh signature
  verification and a new actual scan provide current component evidence"),
  and the legacy six-hour live rescan (`securityScanSweepEvery`).
- **Unchanged:** the five-minute scan lease, the immutable records, fresh
  byte checks, the 30-day database age limit, enforce-mode refusals and
  quarantine, and every first scan of new bytes.

## Context

Production on rc.297, 2026-10-26/27, measured from imaged's logs and the
`base_image_scans` table:

- **The immutable base is rescanned on almost every deploy.** Shared-base scan
  evidence has a five-minute lease and is renewed when two minutes old.
  `base/base-amd64.ext4` was scanned 71 times in 24 hours, and the fleet made
  97 base scans on 10-27, while the base's bytes never changed. The renewal
  runs Grype under the runtime-base lock, so a deploy's `runtime_base` phase
  took 59 s at the median and 212 s at p90, and other deploys waited behind it.
- **Live apps are rescanned every six hours regardless.** 117 of 221 Grype
  runs in 48 hours were these rescans. The vulnerability database changes
  about once a day.
- **Grype is slow here.** It took 67 s at the median and 179 s at p90 per
  run, 217 Grype-minutes in 48 hours on two 4-vCPU nodes.

Grype's findings depend on three things: the bytes scanned, the Grype
version, and the vulnerability database build. Every scan report already
records the last two (`scanner_version`, `scanner_db_version`,
`scanner_db_built_at`). A rescan with all three unchanged repeats the stored
report.

## Decision

1. **Scanner identity.** imaged reads the installed identity with
   `grype version -o json` and `grype db status -o json`. Neither scans nor
   updates the database, and the pair takes about 3 s. It keeps the identity
   for `api.ScannerIdentityCacheTTL` (one minute). Every real scan replaces it
   with the identity its own report names, so a database that a scan just
   updated is never compared against an older reading. With no probe
   configured, or on any probe error, nothing is reused.
2. **Shared-base renewal without rescanning.** When renewal is due, imaged
   first takes the selected scan for the base key. It reuses that scan only
   if all of the following hold:
   - the scan is complete;
   - it binds the same producer ID, input hash and artifact (digest and size);
   - its report names the installed identity;
   - the stored bytes still match the artifact, freshly hashed as today.

   imaged then publishes a new immutable record with the same report. The
   record gets a new storage-clock lease from the store and names the copied
   scan in `reused_scan_id`. The store validates the report exactly as for a
   real scan, including database age, so a database older than 30 days still
   forces a scan that will fail. Anything else, including a refused
   publication, falls through to a real scan.
3. **The database is refreshed on a schedule.** See Consequences.
4. **Live rescans skip current evidence.** The six-hour sweep skips a
   deployment whose last scan completed with no critical, high or unknown
   findings under the installed identity. A deployment's layer is immutable,
   so only a new scanner or database can change its findings. Failed,
   unsafe or unidentified evidence is rescanned as before. A skipped
   deployment keeps its `scanned_at`, which still tells the truth.

## Consequences

- Base scans drop from about one per deploy to about one per base per
  database build. Deploys stop waiting for them under the runtime-base lock.
  Live rescans run once per database build, not four times a day.
- Grype updates its database only inside a scan, and reuse removes most
  scans. So imaged runs `grype db update` itself on its own goroutine, at
  start and every `api.ScannerDBRefreshEvery` (six hours), and then forgets
  the cached identity. A newer database therefore reaches reuse decisions,
  and the next live sweep rescans everything under it, whether or not
  anything was deployed.
- `reused_scan_id` makes renewals without rescanning auditable. It is
  omitted for real scans, so existing record hashes hold.
- Not covered: per-deploy scans of new layers, which always run; the ADR-595
  component scans for registry images, whose renewal worker could take the
  same rule later; and moving real base scans out from under the runtime-base
  lock.

## Amendment 1 (2026-10-29): skipped rescans renew the enforce lease

Decision 4 left a skipped deployment's evidence exactly as it was. The
enforce-mode evidence lease (`securityScanLeaseFailure`) only knows when the
evidence was last checked, so it parked every enforce app six hours after its
last real scan whenever the database had not changed (bug hunt 2026-10-28 B33).
Separately, the six-hourly sweep found a deployment due only once
`scanned_at + 6 h` had passed, and `scanned_at` is stamped when a scan
finishes, so the next tick usually landed just short of the mark and the lease
(6 h plus one minute) expired first (B34, live since September).

- A skip stamps `verified_at` on the stored result with a conditional write
  that applies only while the row still holds the evidence that was checked,
  so it never replaces a newer scan another imaged wrote. `scanned_at` in the
  result stays the time Grype ran; the row's `scanned_at` column, which only
  schedules rescans, moves to the renewal.
- The lease counts from `verified_at` when it is set and later than
  `scanned_at`. Nothing else about the lease changes: digest, metadata,
  database age and findings are checked as before.
- A deployment is due a sixth of the interval early (an hour of six), the
  sweep runs every sixth of that (ten minutes) and picks only due
  deployments, oldest evidence first, and a sweep still running skips the
  next tick. The six-hour interval and the lease are unchanged.

## Amendment 2 (2026-10-30): a failed shared-base renewal keeps the last verdict

A renewal that failed for a reason unrelated to the bytes (`scanner_unavailable`,
`scanner_invalid`, `artifact_read`) published the CRITICAL=9999 refusal, so a
Grype database fetch failure or a storage blip stopped every boot on a base
whose unchanged bytes had just scanned clean: three production outages in three
days (bug hunt 2026-10-28 H102).

- Such a failure keeps the base's current complete scan in force when that scan
  covers the same producer bytes and is younger than
  `api.SharedBaseScanFailureGrace` (24 hours). imaged republishes the scan's
  sidecar, counts the failure in `imaged_base_scan_failures_held_total`, and
  `FaasBaseScanFailureHeld` pages after ten minutes of failures.
- After the grace, changed bytes (`artifact_mismatch`), or a scan that
  completes, the result is published as before: blocking findings and unscanned
  bytes still refuse.
- Staging new base bytes is unchanged: they have no earlier scan to hold.
