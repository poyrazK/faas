package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// reconcileSecurityScans periodically re-evaluates live image deployments.
// The deploy-time gate protects the admission boundary; this pass catches a
// vulnerability database update that makes an already-live image unsafe.
// It records an append-only audit signal and moves enforce-mode apps into the
// existing evicted_cold drain path. The deployment parking reason makes the
// quarantine durable, so a missed notification cannot reopen the app.
func (l *Loop) reconcileSecurityScans(ctx context.Context, now time.Time, every time.Duration) {
	if l == nil || l.store == nil || l.handler == nil {
		return
	}
	if every <= 0 {
		every = securityScanSweepEvery
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		l.log.Warn("imaged: list deployments for security re-scan", "err", err)
		return
	}
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || strings.TrimSpace(dep.ImageDigest) == "" {
			continue
		}
		if !securityScanDue(dep, now.UTC(), every) {
			continue
		}
		app, err := l.store.AppByID(ctx, dep.AppID)
		if err != nil {
			l.log.Warn("imaged: load app for security re-scan", "deployment", dep.ID, "app", dep.AppID, "err", err)
			continue
		}
		if app.Status != state.AppActive {
			continue
		}
		previousStatus := dep.ScanStatus
		previous := decodeScanEvidence(dep.ScanResult)
		if err := l.handler.runDeployScan(ctx, app, dep); err != nil {
			l.log.Warn("imaged: security re-scan failed", "deployment", dep.ID, "app", app.Slug, "err", err)
		}
		current, readErr := l.store.DeploymentByID(ctx, dep.ID)
		if readErr != nil {
			l.log.Warn("imaged: read security re-scan result", "deployment", dep.ID, "err", readErr)
			continue
		}
		currentResult := decodeScanEvidence(current.ScanResult)
		if app.SecurityPolicy != api.AppSecurityPolicyEnforce || !securityScanNeedsQuarantine(current.ScanStatus, currentResult) {
			continue
		}
		if securityScanRegression(previousStatus, previous, current.ScanStatus, currentResult) {
			if auditErr := l.appendSecurityScanRegression(ctx, app, dep, previousStatus, previous, current.ScanStatus, currentResult); auditErr != nil {
				l.log.Warn("imaged: append security scan regression audit", "deployment", dep.ID, "err", auditErr)
			}
		}
		if quarantineErr := l.quarantineSecurityRegression(ctx, app, dep); quarantineErr != nil {
			l.log.Warn("imaged: quarantine security regression", "deployment", dep.ID, "app", app.ID, "err", quarantineErr)
		}
	}
}

// reconcileSecurityLeases is the cheap safety net between full scanner runs.
// A complete scan is evidence about one exact live deployment, not a permanent
// allow-list. Once that evidence expires, or its digest/metadata no longer
// matches the deployment row, enforce-mode traffic is parked before the next
// request can use the stale app route. The scanner sweep remains responsible
// for refreshing otherwise-valid evidence and for discovering newly published
// vulnerabilities.
func (l *Loop) reconcileSecurityLeases(ctx context.Context, now time.Time, scanEvery time.Duration) {
	if l == nil || l.store == nil {
		return
	}
	if scanEvery <= 0 {
		scanEvery = securityScanSweepEvery
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		l.log.Warn("imaged: list deployments for security evidence lease", "err", err)
		return
	}
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || strings.TrimSpace(dep.ImageDigest) == "" {
			continue
		}
		app, err := l.store.AppByID(ctx, dep.AppID)
		if err != nil {
			l.log.Warn("imaged: load app for security evidence lease", "deployment", dep.ID, "app", dep.AppID, "err", err)
			continue
		}
		if app.Status != state.AppActive || app.SecurityPolicy != api.AppSecurityPolicyEnforce {
			continue
		}
		reason := securityScanLeaseFailure(dep, now.UTC(), scanEvery)
		if reason == "" {
			continue
		}
		if dep.ParkedReason == string(state.ParkReasonSecurityScanRegressed) {
			continue
		}
		if auditErr := l.appendSecurityScanLeaseAudit(ctx, app, dep, reason, now.UTC()); auditErr != nil {
			l.log.Warn("imaged: append security evidence lease audit", "deployment", dep.ID, "err", auditErr)
		}
		if quarantineErr := l.quarantineSecurity(ctx, app, dep, reason); quarantineErr != nil {
			l.log.Warn("imaged: quarantine expired security evidence", "deployment", dep.ID, "app", app.ID, "reason", reason, "err", quarantineErr)
		}
	}
}

// reconcileSecuritySignatures revalidates every live image whose app requires
// trusted provenance. It runs at startup and after trusted-signer changes so
// removing or replacing the last signer cannot leave an already-running image
// grandfathered into service. The deployment's durable parked reason keeps
// the quarantine closed across missed notifications and daemon restarts.
func (l *Loop) reconcileSecuritySignatures(ctx context.Context, now time.Time) {
	if l == nil || l.store == nil || l.handler == nil {
		return
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		l.log.Warn("imaged: list deployments for signature revalidation", "err", err)
		return
	}
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || dep.Kind != state.DeploymentKindImage ||
			strings.TrimSpace(dep.ImageDigest) == "" || strings.TrimSpace(dep.ParkedReason) != "" {
			continue
		}
		app, err := l.store.AppByID(ctx, dep.AppID)
		if err != nil {
			l.log.Warn("imaged: load app for signature revalidation", "deployment", dep.ID, "app", dep.AppID, "err", err)
			continue
		}
		if app.Status != state.AppActive || (!app.RequireSigned && !app.SecurityPolicy.RequiresSignedImage()) {
			continue
		}
		_, verifyErr := l.handler.checkImageSignature(ctx, app, dep.ImageDigest)
		if verifyErr == nil {
			continue
		}

		reason := "security_signature_unavailable"
		auditKind := "app.signature_invalid"
		switch {
		case errors.Is(verifyErr, cosign.ErrSignatureMissing):
			reason = "security_signature_missing"
			auditKind = "app.signature_missing"
		case errors.Is(verifyErr, cosign.ErrSignatureInvalid):
			reason = "security_signature_revoked"
			auditKind = "app.signature_revoked"
		}
		l.handler.emitSignatureAudit(ctx, auditKind, app, dep, dep.ImageDigest, "")
		if quarantineErr := l.quarantineSecurity(ctx, app, dep, reason); quarantineErr != nil {
			l.log.Warn("imaged: quarantine signature regression", "deployment", dep.ID, "app", app.ID, "reason", reason, "observed_at", now.UTC(), "err", quarantineErr)
		}
	}
}

// securityScanLeaseFailure returns a stable reason code when live scan
// evidence is no longer sufficient to keep an enforce-mode app serving.
// The lease is intentionally tied to the scheduled sweep interval rather than
// the five-minute deploy-admission window: a healthy live deployment has to
// survive until the next scanner pass, while a failed pass still expires it
// promptly on the following lease tick. One lease tick of grace prevents the
// cheap checker from racing a scanner result that is being persisted at the
// same boundary.
func securityScanLeaseFailure(dep state.Deployment, now time.Time, scanEvery time.Duration) string {
	if dep.ScanStatus != "complete" {
		return "security_scan_evidence_incomplete"
	}
	result := decodeScanEvidence(dep.ScanResult)
	if result == nil {
		return "security_scan_evidence_missing"
	}
	if strings.TrimSpace(result.ImageDigest) == "" || strings.TrimSpace(result.ImageDigest) != strings.TrimSpace(dep.ImageDigest) {
		return "security_scan_digest_drift"
	}
	if strings.TrimSpace(result.ArtifactDigest) == "" || strings.TrimSpace(result.ScannerVersion) == "" ||
		strings.TrimSpace(result.ScannerDBVersion) == "" || strings.TrimSpace(result.ScannerDBBuiltAt) == "" ||
		!strings.EqualFold(strings.TrimSpace(result.ScannerDBStatus), "valid") {
		return "security_scan_evidence_invalid"
	}
	scannedAt, err := parseScanEvidenceTime(result.ScannedAt)
	if err != nil {
		return "security_scan_evidence_invalid"
	}
	lease := scanEvery + securityScanLeaseSweepEvery
	if scannedAt.After(now) || now.Sub(scannedAt) > lease {
		return "security_scan_evidence_expired"
	}
	dbBuiltAt, err := parseScanEvidenceTime(result.ScannerDBBuiltAt)
	if err != nil || dbBuiltAt.After(now) || now.Sub(dbBuiltAt) > verifiedScannerDBAge {
		return "security_scan_database_stale"
	}
	counts := result.SeverityCounts
	if counts.Critical > 0 || counts.High > 0 || counts.Unknown > 0 || strings.TrimSpace(result.Error) != "" {
		return "security_scan_regressed"
	}
	return ""
}

type appStatusCompareAndSetter interface {
	CompareAndSetAppStatus(context.Context, string, state.AppStatus, state.AppStatus) (bool, error)
}

// quarantineSecurityRegression projects a scan regression into the existing
// app lifecycle drain path. It is deliberately idempotent: a retry after a
// missed notification or a partial failure keeps the first parked timestamp
// and only emits the app_changed hint again.
func (l *Loop) quarantineSecurityRegression(ctx context.Context, app state.App, dep state.Deployment) error {
	return l.quarantineSecurity(ctx, app, dep, "security_scan_regressed")
}

// quarantineSecurity projects any security evidence failure into the existing
// app lifecycle drain path. The parked reason remains the durable closed-set
// value used by the gateway; the finer-grained reason is carried in audit and
// notification payloads for operators.
func (l *Loop) quarantineSecurity(ctx context.Context, app state.App, dep state.Deployment, reason string) error {
	if l == nil || l.store == nil {
		return fmt.Errorf("imaged: security quarantine is not wired")
	}
	at := time.Now().UTC()
	if l.now != nil {
		at = l.now().UTC()
	}
	if err := l.store.SetDeploymentParked(ctx, dep.ID, string(state.ParkReasonSecurityScanRegressed), at); err != nil {
		return fmt.Errorf("stamp deployment quarantine: %w", err)
	}

	if app.Status != state.AppEvictedCold {
		if cas, ok := l.store.(appStatusCompareAndSetter); ok {
			claimed, err := cas.CompareAndSetAppStatus(ctx, app.ID, state.AppActive, state.AppEvictedCold)
			if err != nil {
				return fmt.Errorf("quarantine app status: %w", err)
			}
			if !claimed {
				current, readErr := l.store.AppByID(ctx, app.ID)
				if readErr != nil {
					return fmt.Errorf("read quarantined app status: %w", readErr)
				}
				if current.Status != state.AppEvictedCold {
					return fmt.Errorf("quarantine app status lost race: status=%s", current.Status)
				}
			}
		} else {
			parked := state.AppEvictedCold
			if _, err := l.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
				return fmt.Errorf("quarantine app status: %w", err)
			}
		}
	}

	if l.handler == nil || l.handler.notif == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]string{
		"kind":            "parked",
		"app_id":          app.ID,
		"slug":            app.Slug,
		"deployment_id":   dep.ID,
		"image_digest":    dep.ImageDigest,
		"status":          string(state.AppEvictedCold),
		"reason":          string(state.ParkReasonSecurityScanRegressed),
		"evidence_reason": reason,
	})
	if err != nil {
		return fmt.Errorf("marshal quarantine notification: %w", err)
	}
	if err := l.handler.notif.Notify(ctx, db.NotifyAppChanged, string(payload)); err != nil {
		return fmt.Errorf("notify security quarantine: %w", err)
	}
	return nil
}

func (l *Loop) appendSecurityScanLeaseAudit(ctx context.Context, app state.App, dep state.Deployment, reason string, observedAt time.Time) error {
	deploymentID, err := uuid.Parse(dep.ID)
	if err != nil {
		return fmt.Errorf("parse deployment id: %w", err)
	}
	var accountID *uuid.UUID
	if parsed, parseErr := uuid.Parse(app.AccountID); parseErr == nil {
		accountID = &parsed
	}
	data, err := json.Marshal(map[string]any{
		"app_id":              app.ID,
		"app_slug":            app.Slug,
		"image_digest":        dep.ImageDigest,
		"reason":              reason,
		"quarantine_required": true,
		"observed_at":         observedAt.Format(time.RFC3339Nano),
		"source":              "scheduled_security_evidence_lease",
	})
	if err != nil {
		return fmt.Errorf("marshal lease evidence: %w", err)
	}
	_, err = l.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{
		DeploymentID: deploymentID,
		AccountID:    accountID,
		Kind:         state.DeployScanRegressed,
		Actor:        "system:imaged-security-lease",
		Data:         data,
	})
	return err
}

func securityScanDue(dep state.Deployment, now time.Time, every time.Duration) bool {
	if dep.ScannedAt.IsZero() {
		return true
	}
	return !now.Before(dep.ScannedAt.UTC().Add(every))
}

func decodeScanEvidence(raw []byte) *ScanResult {
	if len(raw) == 0 {
		return nil
	}
	var result ScanResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	return &result
}

func securityScanNeedsQuarantine(status string, result *ScanResult) bool {
	if status != "complete" || result == nil || strings.TrimSpace(result.Error) != "" {
		return true
	}
	counts := result.SeverityCounts
	return counts.Critical > 0 || counts.High > 0 || counts.Unknown > 0
}

func securityScanRegression(previousStatus string, previous *ScanResult, currentStatus string, current *ScanResult) bool {
	if !securityScanNeedsQuarantine(currentStatus, current) {
		return false
	}
	if previousStatus == "" && previous == nil {
		return true
	}
	return !securityScanNeedsQuarantine(previousStatus, previous)
}

func (l *Loop) appendSecurityScanRegression(ctx context.Context, app state.App, dep state.Deployment, previousStatus string, previous *ScanResult, currentStatus string, current *ScanResult) error {
	deploymentID, err := uuid.Parse(dep.ID)
	if err != nil {
		return fmt.Errorf("parse deployment id: %w", err)
	}
	var accountID *uuid.UUID
	if parsed, parseErr := uuid.Parse(app.AccountID); parseErr == nil {
		accountID = &parsed
	}
	data, err := json.Marshal(map[string]any{
		"app_id":              app.ID,
		"app_slug":            app.Slug,
		"image_digest":        dep.ImageDigest,
		"previous_status":     previousStatus,
		"previous_counts":     scanSeverityCounts(previous),
		"current_status":      currentStatus,
		"current_counts":      scanSeverityCounts(current),
		"scanner_db_version":  scanString(current, func(r *ScanResult) string { return r.ScannerDBVersion }),
		"scanner_db_built_at": scanString(current, func(r *ScanResult) string { return r.ScannerDBBuiltAt }),
		"quarantine_required": true,
		"source":              "scheduled_security_rescan",
	})
	if err != nil {
		return fmt.Errorf("marshal regression evidence: %w", err)
	}
	_, err = l.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{
		DeploymentID: deploymentID,
		AccountID:    accountID,
		Kind:         state.DeployScanRegressed,
		Actor:        "system:imaged-security-rescan",
		Data:         data,
	})
	return err
}

func scanSeverityCounts(result *ScanResult) SeverityCounts {
	if result == nil {
		return SeverityCounts{}
	}
	return result.SeverityCounts
}

func scanString(result *ScanResult, pick func(*ScanResult) string) string {
	if result == nil {
		return ""
	}
	return pick(result)
}
