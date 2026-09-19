package imaged

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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

type appStatusCompareAndSetter interface {
	CompareAndSetAppStatus(context.Context, string, state.AppStatus, state.AppStatus) (bool, error)
}

// quarantineSecurityRegression projects a scan regression into the existing
// app lifecycle drain path. It is deliberately idempotent: a retry after a
// missed notification or a partial failure keeps the first parked timestamp
// and only emits the app_changed hint again.
func (l *Loop) quarantineSecurityRegression(ctx context.Context, app state.App, dep state.Deployment) error {
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
		"kind":   "parked",
		"app_id": app.ID,
		"slug":   app.Slug,
		"status": string(state.AppEvictedCold),
		"reason": string(state.ParkReasonSecurityScanRegressed),
	})
	if err != nil {
		return fmt.Errorf("marshal quarantine notification: %w", err)
	}
	if err := l.handler.notif.Notify(ctx, db.NotifyAppChanged, string(payload)); err != nil {
		return fmt.Errorf("notify security quarantine: %w", err)
	}
	return nil
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
