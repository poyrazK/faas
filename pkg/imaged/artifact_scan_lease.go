package imaged

// adr: 435

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) privateSecurityLeaseFailure(ctx context.Context, app state.App, dep state.Deployment) (bool, string, error) {
	present, err := producedRuntimePresent(ctx, l.store, app, dep)
	if !present && err == nil {
		allowed, readErr := l.legacySecurityLeaseAllowed(ctx, app)
		if allowed {
			return false, "", nil
		}
		if readErr != nil && !leaseEvidenceDefinitive(readErr) {
			l.log.Warn("imaged: security evidence lease skipped; enrollment unreadable", "deployment", dep.ID, "err", readErr)
			return true, "", readErr
		}
		return true, "security_scan_evidence_missing", readErr
	}
	reader, ok := l.store.(state.DeploymentRuntimeScanStore)
	if err == nil && !ok {
		return true, "security_scan_evidence_unavailable", nil
	}
	var value state.DeploymentRuntimeScanEvidence
	if err == nil {
		value, err = reader.GetFreshDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	}
	if err != nil {
		if producedEvidenceBusy(err) || ctx.Err() != nil {
			return true, "", err
		}
		if !leaseEvidenceDefinitive(err) {
			// A store read that failed (a CP Postgres timeout) says
			// nothing about the evidence; it parked enforce apps whose
			// evidence was fine (bug hunt 2026-10-28 H84). The next
			// minute reads again.
			l.log.Warn("imaged: security evidence lease skipped; evidence unreadable", "deployment", dep.ID, "err", err)
			return true, "", err
		}
		reason := "security_scan_evidence_invalid"
		if errors.Is(err, cosign.ErrSignatureInvalid) {
			reason = "security_signature_revoked"
		}
		return true, reason, err
	}
	return true, privateSecurityScanFailure(value), nil
}

func privateSecurityScanFailure(value state.DeploymentRuntimeScanEvidence) string {
	if value.Scan.ID == "" || value.Scan.Input.Status != "complete" || len(value.Scan.Input.Reports) == 0 || value.CheckedAt.IsZero() || !value.ExpiresAt.After(value.CheckedAt) {
		return "security_scan_evidence_invalid"
	}
	for _, view := range value.Scan.Input.Reports {
		counts := view.Report.SeverityCounts
		if view.Report.Error != "" || counts.Critical > 0 || counts.High > 0 || counts.Unknown > 0 {
			return "security_scan_regressed"
		}
	}
	return ""
}

func (l *Loop) legacySecurityLeaseAllowed(ctx context.Context, app state.App) (bool, error) {
	if app.OrgID == "" {
		return true, nil
	}
	reader, ok := l.store.(state.ApplicationStandardEnrollmentStore)
	if !ok {
		return false, state.ErrApplicationStandardsPending
	}
	value, err := reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err != nil {
		return false, err
	}
	return len(value.Adoptions) == 0 && len(value.MaterializedFields) == 0, nil
}

// leaseEvidenceDefinitive reports whether a lease read error is a verdict on
// the evidence (none fresh, a stale runtime, a missing or invalid signature)
// rather than a failure to read it.
func leaseEvidenceDefinitive(err error) bool {
	return errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrApplicationStandardRuntimeStale) ||
		errors.Is(err, state.ErrApplicationStandardsPending) ||
		errors.Is(err, cosign.ErrSignatureInvalid) || errors.Is(err, cosign.ErrSignatureMissing)
}
