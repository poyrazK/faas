package main

// handlers_scan.go — per-deploy grype scan HTTP handler
// (issue #464 / ADR-075 / PR-4 of the mega-PR).
//
// GET /v1/deployments/{id}/scan returns the per-deploy grype
// scan payload for one deployment. The route is the
// drill-down surface (the dashboard reads it; the CLI's
// `--show-scan` flag reads it; PR-5 / PR-6 don't redecode
// the typed payload — they hit this route and pass the JSON
// through to the consumer).
//
// Wire shape: api.ScanResult (pkg/api/dto_scan.go). Status
// is the closed enum that mirrors deployments.scan_status:
//
//   - "complete" — full payload (SeverityCounts +
//     Vulnerabilities). scannED_at is the wall clock the
//     scan landed.
//   - "failed" — payload carries Error only; SeverityCounts
//     is all-zero, Vulnerabilities is nil. The dashboard
//     renders the Error message on the "scan failed" pill.
//   - "skipped" — pre-feature backfill rows
//     (migrations/00135 stamps scan_status='skipped' on
//     every pre-#464 row). The payload carries a reason
//     sentinel in scan_result jsonb; we surface Status +
//     reason only.
//   - NULL / "" (no scan run yet) — 404. The dashboard
//     shows "scan pending" on the 404 (matches the
//     DeploymentResponse.Scan absence path on PR-3).
//
// IDOR posture: same AppByID + AccountID check as
// getDeployment (handler_ext.go:660). A cross-account
// probe returns 404 — never reveals whether the
// deployment exists.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getDeploymentScan is the GET /v1/deployments/{id}/scan
// handler (issue #464 / ADR-075 / PR-4). Returns the typed
// ScanResult on a successful scan, the failed shape on a
// retry-exhausted scan, the skipped shape on pre-feature
// backfill rows, or 404 when no scan has run yet (or the
// deployment doesn't exist / is cross-account).
//
// Auth: authLimited + requireMFA + read scope — same chain
// as getDeployment at server.go:712. The handler body is
// kept small (≤50 lines per CLAUDE.md convention) by
// delegating the wire-shape construction to scanResponse.
func (s *server) getDeploymentScan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("load deployment: %v", err)))
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		// Same posture as getDeployment: cross-account
		// probes get 404, not 403 — we never reveal
		// whether the deployment_id exists in another
		// account.
		s.notFound(w, "no such deployment")
		return
	}
	if d.ScanStatus == "" {
		// No scan has run yet — the deploy is still
		// mid-pipeline or predates #464 entirely. 404 is
		// the right answer; the dashboard's "scan
		// pending" pill renders on the absence.
		s.notFound(w, "scan not yet available")
		return
	}
	resp := s.scanResponse(d)
	writeJSON(w, http.StatusOK, resp)
}

// scanResponse builds the wire-shape *api.ScanResult from
// a state.Deployment row. Returns nil when the row has no
// scan_status (deploy mid-pipeline, pre-#464 row, or the
// pre-feature backfill wasn't a "skipped" sentinel). The
// returned pointer is the SINGLE source of truth for the
// per-deploy scan wire shape — deploymentResponse (PR-3,
// handlers_ext.go:2121-2159) and getDeploymentScan (PR-4,
// this file) both go through it, so the two endpoints stay
// byte-identical for a given row.
//
// Decode-failure convention: a corrupt jsonb payload does
// NOT surface a 500. The handler logs at WARN and the
// returned ScanResult carries Error = "scan_result decode
// failed (server logs carry the detail)" so the dashboard
// renders the same "scan failed" pill it renders for a
// real scan_status='failed' row. Status is re-pinned from
// the column because the column is authoritative; the
// jsonb payload is data only.
func (s *server) scanResponse(d state.Deployment) *api.ScanResult {
	if d.ScanStatus == "" {
		return nil
	}
	out := api.ScanResult{Status: d.ScanStatus}
	if !d.ScannedAt.IsZero() {
		out.ScannedAt = d.ScannedAt.UTC().Format(time.RFC3339)
	}
	out.ImageDigest = d.ImageDigest
	if len(d.ScanResult) > 0 {
		if err := json.Unmarshal(d.ScanResult, &out); err != nil {
			// Decode failure is logged but doesn't
			// surface a 500 — the row carries a Status
			// the customer can act on (re-deploy, contact
			// support). The empty SeverityCounts +
			// nil Vulnerabilities render as the
			// "scan summary unavailable" view on the
			// dashboard.
			out.SeverityCounts = api.SeverityCounts{}
			out.Vulnerabilities = nil
			out.Error = "scan_result decode failed (server logs carry the detail)"
			s.log.Warn("apid: decode scan_result", "deployment", d.ID, "err", err)
		}
		// Re-pin Status from the column — the column is
		// authoritative; the jsonb payload is data only.
		out.Status = d.ScanStatus
		// Older imaged builds embedded severity fields at the JSON root,
		// while the public DTO has always documented severity_counts. When
		// detailed findings are present, derive the histogram from that
		// canonical evidence so stored legacy rows and new nested rows agree.
		if len(out.Vulnerabilities) > 0 {
			out.SeverityCounts = severityCountsForVulnerabilities(out.Vulnerabilities)
		}
	}
	return &out
}

func severityCountsForVulnerabilities(vulnerabilities []api.Vulnerability) api.SeverityCounts {
	var counts api.SeverityCounts
	for _, vulnerability := range vulnerabilities {
		switch strings.ToUpper(vulnerability.Severity) {
		case "CRITICAL":
			counts.Critical++
		case "HIGH":
			counts.High++
		case "MEDIUM":
			counts.Medium++
		case "LOW", "NEGLIGIBLE":
			counts.Low++
		default:
			counts.Unknown++
		}
	}
	return counts
}

// getDeploymentSecretScan is the GET
// /v1/deployments/{id}/secret-scan handler (PR-A, imaged-layer
// secret scan). Mirrors getDeploymentScan structurally — same
// IDOR posture (AppByID + AccountID check → cross-account probes
// get 404), same 404 on no-row-yet. The "no scan yet" case is
// derived from `SecretScannedAt == nil` rather than a dedicated
// secret_scan_status column (migration 00264 added the audit
// jsonb + scanned_at timestamptz but no status column; the
// wire-level Status is reconstructed from the jsonb payload).
//
// Auth: authLimited + requireMFA + read scope — same chain as
// getDeployment at server.go:712. Handler body kept small (≤50
// lines per CLAUDE.md convention) by delegating wire-shape
// construction to secretScanResponse.
func (s *server) getDeploymentSecretScan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("load deployment: %v", err)))
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		// Same posture as getDeployment + getDeploymentScan:
		// cross-account probes get 404, not 403 — we never
		// reveal whether the deployment_id exists in
		// another account.
		s.notFound(w, "no such deployment")
		return
	}
	resp := s.secretScanResponse(d)
	if resp == nil {
		// No scan has run yet — the deploy is still
		// mid-pipeline or predates PR-A entirely. 404 is
		// the right answer; the dashboard's "scan
		// pending" pill renders on the absence
		// (SecretScan == nil on DeploymentResponse).
		s.notFound(w, "secret scan not yet available")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// secretScanResponse builds the wire-shape
// *api.SecretScanResult from a state.Deployment row. Returns
// nil when the row has no SecretFindings yet (deploy
// mid-pipeline, pre-PR-A row). The returned pointer is the
// SINGLE source of truth for the per-deploy secret-scan wire
// shape — deploymentResponse (PR-A, handlers_ext.go
// deploymentResponse) and getDeploymentSecretScan both go
// through it, so the two endpoints stay byte-identical for a
// given row.
//
// Decode-failure convention: a corrupt jsonb payload does NOT
// surface a 500. The handler logs at WARN and the returned
// SecretScanResult carries an empty Findings slice + Status =
// "complete" (the dashboard renders the same "scan failed"
// pill it renders for an unreadable row). ImageDigest /
// ScannedAt fall back to the row columns when the jsonb
// payload can't decode.
func (s *server) secretScanResponse(d state.Deployment) *api.SecretScanResult {
	if d.SecretScannedAt == nil {
		return nil
	}
	// Status starts empty. The earlier "Status: 'complete'"
	// default silently miscategorised finding-positive rows
	// whose jsonb failed to unmarshal — Status would read
	// "complete" while findings>0 implied "complete_with_redactions".
	// Treating the decoded payload as authoritative means a
	// decode failure surfaces an Error+empty-Status result the
	// dashboard can render as the same "scan summary unavailable"
	// pill it renders for an unreadable row.
	out := api.SecretScanResult{}
	out.ScannedAt = d.SecretScannedAt.UTC().Format(time.RFC3339Nano)
	out.ImageDigest = d.ImageDigest
	if len(d.SecretFindings) > 0 {
		if err := json.Unmarshal(d.SecretFindings, &out); err != nil {
			out.Findings = nil
			out.Error = "secret_findings decode failed (server logs carry the detail)"
			s.log.Warn("apid: decode secret_findings",
				"deployment", d.ID, "err", err)
		}
	}
	return &out
}
