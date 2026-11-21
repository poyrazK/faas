# ADR-075 — Per-deploy grype scan surface (issue #464)

- **Status:** accepted
- **Date:** 2026-08-05
- **Issue:** #464
- **Depends on:** issue #299 (base-ext4 factory scan), ADR-038 (build
  attestation umbrella)
- **Supersedes:** nothing
- **Superseded by:** nothing — closes issue #464

## TL;DR

Surface — don't enforce. The per-deploy grype scan runs against
the per-app layer ext4 in imaged's deploy-complete hook and lands
the result on the `deployments` row (one column: `scan_result`
jsonb, plus two status columns: `scan_status` ∈ {pending, complete,
failed, skipped}, and `scanned_at`). The surface is read on
`GET /v1/deployments/{id}`, `GET /v1/deployments/{id}/scan`, the
dashboard deploy detail page, and `gregale deployment <id> --show-scan`.
CRITICAL-CVE images deploy successfully (AC #4) — the scan is
async, post-deploy, and never reverses the live flip.

## Context

`pkg/imaged/grype.go` runs at build time (issue #299) and writes
a fail-closed `CRITICAL=9999` sidecar to the storage backend keyed
by `wire.ScanKeyForBaseKey(baseKey)` — that's a **base-ext4
factory scan** used by vmmd's `bringUpScanCheck` to refuse boot of
an un-scanned base. The **per-deploy scan** that issue #464 wants
is a different surface: it runs over the app-layer squashfs *after*
the layers-above-base diff, and the result lands on the
`deployments` row so the customer (and the dashboard) can see what
CVEs are in the image they just deployed.

The two concerns are parallel verticals (cosign/sbom vs grype CVE);
co-locating them in ADR-038 would conflate them. This ADR is the
home for the per-deploy scan surface.

## Decisions

1. **Surface, not enforce.** Per-deploy scan failures **stamp**
   `scan_status='failed'` on the row; they never reject the deploy
   (AC #4). The 5-min SLA (AC #1) is observed but not gated on —
   the dashboard's "scan overdue" chip is the only consequence.

2. **Async dispatch.** The scan runs in imaged's deploy-complete
   hook, AFTER `SetDeploymentRootfs` and BEFORE the snapshotting
   transition. The deploy row flips to `live` first; the scan
   lands within Grype's per-layer ~1-3s plus the 30s retry backoff.
   ~5-30s p99 per deploy, well under the 5-min SLA.

3. **One-retry-backoff policy.** A grype runner error triggers one
   retry after 30s; persistent failure flips `scan_status='failed'`
   with the last error in the JSON `error` field. Continuous
   re-scanning is **out of scope** (the issue's scope is
   surface-the-result, not continuous-monitoring).

4. **Closed-enum `scan_status`.** `pending | complete | failed | skipped`
   (NOT a boolean), CHECK-constrained at the schema level. The
   pre-feature backfill stamps `skipped` on every pre-#464 row so
   the dashboard's "scan pending" chip never sticks on legacy
   rows.

5. **apid-only-writer to customer-intent tables.** Apid is the
   sole writer of the `scan_result` / `scan_status` / `scanned_at`
   columns. Imaged reaches the column via `state.Store.UpsertDeploymentScanResult`
   which the apid wiring owns (PR-3). The "who writes" rule is a
   daemon-coordination rule at the pg_notify boundary, not a
   Go-package boundary — same precedent as SetDeploymentRootfs.

6. **No new cross-component abstraction.** The seam is the
   existing `state.Store` interface — no `ScanResultSink` in
   `pkg/imaged` (cluster's design proposed one; the final form
   dropped it because imaged already imports `pkg/state` for
   the deploy-status update). One fewer indirection.

7. **Read-side surface:** four reads, one store column.
   - `GET /v1/deployments/{id}` — additive `scan` field on the
     response (decode jsonb, repin status from the authoritative
     column).
   - `GET /v1/deployments/{id}/scan` — full drill-down
     (counts + CVE list). 404 on `scan_status IS NULL`, 200 with
     `{status: "failed", error: "..."}` on `scan_status='failed'`.
   - Dashboard `/dashboard/apps/{slug}/deployments/{id}` — server-rendered
     full scan view.
   - `gregale deployment <id> --show-scan` — CLI flag, not a new
     subcommand (the `gregale scan` name is already taken by the
     Phase 3 repo-decomposition dry-run).

8. **Local types in pkg/dashboard.** pkg/dashboard defines
   `ScanPayload` / `SeverityBucket` / `VulnerabilityRow` locally
   rather than importing `pkg/api.ScanResult` — same
   package-isolation rule that drove `AppListItem` vs `pkg/api.App`.
   The handler at `cmd/apid/handlers_dashboard.go` is the only
   thing that crosses the api → dashboard boundary.

9. **Metrics.** Three new wire metrics, mirroring the issue #299
   imageScanVulns precedent:
   - `_deploy_scan_duration_seconds{app}` histogram with an
     explicit 300s SLO bucket.
   - `_deploy_scan_total{app, result}` counter labelled
     `complete|failed|skipped`.
   - `_deploy_scan_vulns_total{app, severity}` counter labelled
     the Grype closed set.
   - All three are safe on a nil receiver (the wire package's
     nil-safe receiver convention).

10. **tests/ pins.** Two unit tests pin the seam — not the SQL:
    - `pkg/imaged/scan_test.go::TestRunDeployScan_StampsComplete`
      — happy path round-trip.
    - `pkg/imaged/scan_test.go::TestRunDeployScan_StampsFailed`
      — grype failure stamps `scan_status='failed'` with the
      error preserved; the deploy's lifecycle status is untouched.
    - `cmd/e2e/scan_e2e_test.go` (PR-7) — boots apid + imaged +
      fakeregistry, walks the full path, plus the cross-account
      404 isolation test.

## What this ADR does NOT do

- Does NOT make wake depend on a scan landing (AC #4 forbids).
- Does NOT continuous-rescan (single scan per deploy).
- Does NOT export CVE findings to a separate table — the row
  carries the full payload in `scan_result` jsonb.
- Does NOT move the base-ext4 factory scan (issue #299) — the
  two stay parallel.

## Verification

- `go test ./pkg/imaged/... ./pkg/wire/...`
- `go test ./cmd/apid/... ./pkg/dashboard/...`
- `go test ./cmd/gregale/...`
- `make lint && make test` (the standard 4-gate sweep)
- `go test ./cmd/e2e/... -run TestE2E_Scan` (PR-7)

## Validation experiments

- V-1 (replay): PR-3 commit 1 contains the pre-fix output for
  the `state.Store.UpsertDeploymentScanResult` seam that the
  test 1+2 above now pin against.

## Subsequent amendments

### Top-10 dashboard cap (commit on issue #464 PR-B extension)

The dashboard's deployment detail page now renders the **top 10
CVEs by severity** with a "View full scan (JSON)" link to
`GET /v1/deployments/{id}/scan` when the scan had more findings
than the dashboard width allows — per the AC #3 text
("dashboard deploy detail page shows severity counts and the top
10 CVEs by severity, with a link to the full list"). The cap is
at the **handler edge** (`cmd/apid/handlers_dashboard.go::
dashboardScanPayload`) — the wire DTO, the `/scan` route, the
SDK, and the gregale `--show-scan` CLI flag keep the **full**
list, so customers reaching the API directly don't have to
reimplement the cap. Sort is stable on severity ordinal
(CRITICAL=0 → UNKNOWN=4, anything outside the enum sorts last)
then on ID for ties. The handler populates a new
`dashboard.ScanPayload.TotalCount` so the template can render
the "Showing N of M" copy + the link. The cap value
(`dashboardScanTopN = 10`) is a top-level const at the handler
edge so a unit test in `handlers_dashboard_test.go::
TestDashboardScanPayload_TopNCap` can iterate without
copy-paste; the test pins overflow (15 → cap to 10), the
no-cap-when-under-N path, zero-finding, and nil-scan.

### `Vulnerability.Paths` populated from `artifact.locations[].path`
(commit on issue #464 PR-B extension)

The wire `Vulnerability` DTO now carries `paths []string`
(grype's `artifact.locations[].path`); the issue's example JSON
explicitly cited this field. PR #651 dropped `Paths` from the
wire in `b9f44593 fix(scan): drop unused Vulnerability.Paths`
on review finding #54 ("customers don't need internal grype
match paths"); the PR-B extension reversed that stance when the
dashboard's "Path" column was added so customers can identify
which shared library to rebuild. `pkg/imaged/grype.go::
parseGrypeOutput` (extracted from `runGrypeImpl` so unit
tests don't need a grype subprocess) decodes the new fields
and emits a `pkg/imaged.Vulnerability` slice on
`ScanResult.Vulnerabilities` (omitempty, so zero-finding scans
stay compact). The base-ext4 sidecar continues to read only
`SeverityCounts`; the new fields flow through ScanResult and
are written to the deployment row, never to the sidecar. The
SDK regen (`make sdk-gen`) mirrors the new field on
`sdk/node/Vulnerability.ts` and `sdk/python/
faas_sdk/models/vulnerability.py`; sdk-go is hand-curated. Open
follow-up: persist all of `vulnerability.fix.versions[]` as a
slice if a CVE ever carries multiple fixed versions (grype
currently emits one fixed-version string per CVE).

### Scans that gate nothing run after the snapshot handoff (2026-11-17)

Decision 2 put the scan off the deploy's path; the image preparation
pipeline (ADR-463) later ran it in front of the snapshot handoff for every
app. In production every app was `security_policy=off`, so 631 deploys in
three days waited for a result nothing read: 17.6 s at p50 and 42 s at p90
of the 1,091 scans logged on both compute nodes over two days, 88% of them
fresh Grype runs.

imaged now hands the deployment to snapshot preparation first and queues
the scan when the deploy cannot depend on it: `security_policy` is `off` or
`warn`, the app does not require signed images, and the deployment has no
ADR-435 runtime producer (that route requires complete evidence in every
mode). Enforce mode, signed images and producer evidence keep the scan in
front, unchanged; so does any doubt, such as a failed producer lookup.

- The `security_scan` stage still records the gate; for a deferred scan it
  passes at once. `scan_status` stays empty until the scan lands, which the
  API and dashboard already show as "scan pending".
- The scan is queued only after the handoff checkpoint commits, so a crash
  at any earlier checkpoint resumes there and queues it exactly once.
- One deferred scan runs at a time per imaged (`api.ImagedDeferredScanWorkers`)
  and at most `api.ImagedDeferredScanQueue` wait, so deferral never runs
  more Grype processes than the serial scans it replaced. A scan dropped
  from a full queue, or lost with a restart, is picked up by the
  live-deployment rescan sweep, which treats a deployment with no scan as
  due; the sweep skips a deployment whose deferred scan is still queued or
  running on its node.
- The "per-deploy scan stamped" log carries `deferred` so the change can be
  read in production.

### Amendment 2026-11-18: an organization's app keeps its scan in front

An organization's app runs under application standards, and each of its
instances captures the deployment's `scan_status` and `scan_result_hash` at
admission (ADR-595). A deferred scan that landed during the snapshot prime
changed those mirrors under the prime instance, and the caller-input check
failed the deploy with "runtime inputs changed after admission: artifact".
Production release acceptance hit this on every org-app deploy whose scan
finished mid-prime. Such an app now scans before the handoff, as before the
amendment above; apps without an organization keep the deferral.

### Amendment 2026-11-19: an organization's app defers again unless a standard admits it natively

The 2026-11-18 amendment kept every organization's app on the serial path.
Every production app belongs to an organization (all 471 source deploys in
the three days to 2026-11-19), so no scan was ever deferred: 166 of 166
scans logged after the 2026-11-18 22:36Z release ran in front of the handoff,
15.6 s at p50 and 33.5 s at p90 of imaged's preparation time.

The failure that amendment answered came from the scheduler's caller-input
check (`state.CheckedInstanceApplicationStandardAdmission`), the one check
that compared the deployment's scan mirrors outside ADR-435 producer
captures. The publication guard (`application_standard_runtime_inputs_match`)
has ignored them since ADR 20261031190030: a scan renews `scan_status` and
`scan_result_hash` and changes nothing the instance runs, and a scan that
parks the deployment still changes `parked_reason`, which stays compared.
The caller-input check now ignores the mirrors in the same way, and
MemStore's guard for a booting instance follows the PostgreSQL guard.

imaged therefore defers an organization's app's scan under the conditions
above, with one more exception: an app whose standard enrollment admits its
instances natively (`ApplicationStandardEnrollmentRequiresNative`) keeps the
scan in front, because native captures compare the mirrors
(`application_standard_native_inputs_match`) and a scan mid-prime would
invalidate the prime's native snapshot grant. A failed enrollment read also
keeps the scan in front. On 2026-11-19 none of production's 81 organization
apps ran under such a standard.
