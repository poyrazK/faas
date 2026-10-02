package imaged

// adr: 430

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) privateSecurityLeaseFailure(ctx context.Context, app state.App, dep state.Deployment) (bool, string, error) {
	reader, ok := l.store.(state.DeploymentArtifactScanEvidenceStore)
	if !ok {
		allowed, err := l.legacySecurityLeaseAllowed(ctx, app)
		if allowed {
			return false, "", nil
		}
		return true, "security_scan_evidence_unavailable", err
	}
	value, err := reader.GetFreshDeploymentArtifactScanEvidence(ctx, app.AccountID, app.ID, dep.ID)
	if errors.Is(err, state.ErrDeploymentArtifactScanEvidenceAbsent) {
		allowed, readErr := l.legacySecurityLeaseAllowed(ctx, app)
		if allowed {
			return false, "", nil
		}
		return true, "security_scan_evidence_missing", readErr
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

func privateSecurityScanFailure(value state.DeploymentArtifactScanEvidence) string {
	if len(value.Components) == 0 || value.CheckedAt.IsZero() || !value.ExpiresAt.After(value.CheckedAt) {
		return "security_scan_evidence_invalid"
	}
	for _, scan := range value.Components {
		if privateScanUnsafe(scan.Result) {
			return "security_scan_regressed"
		}
	}
	for _, scan := range value.Bases {
		if privateScanUnsafe(scan.Result) {
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

func privateScanUnsafe(value api.ScanResult) bool {
	counts := value.SeverityCounts
	return value.Status != "complete" || value.Error != "" || counts.Critical > 0 || counts.High > 0 || counts.Unknown > 0
}
