package imaged

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// reconcileSecurityScans periodically re-evaluates live image deployments.
// The deploy-time gate protects the admission boundary; this pass catches a
// vulnerability database update that makes an already-live image unsafe.
// It records an append-only audit signal for enforce-mode apps. A later
// traffic/quarantine controller can consume that signal without making this
// scanner own instance teardown or customer lifecycle transitions.
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
		if app.SecurityPolicy != api.AppSecurityPolicyEnforce || !securityScanRegression(previousStatus, previous, current.ScanStatus, currentResult) {
			continue
		}
		if auditErr := l.appendSecurityScanRegression(ctx, app, dep, previousStatus, previous, current.ScanStatus, currentResult); auditErr != nil {
			l.log.Warn("imaged: append security scan regression audit", "deployment", dep.ID, "err", auditErr)
		}
	}
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
