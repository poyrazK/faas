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
