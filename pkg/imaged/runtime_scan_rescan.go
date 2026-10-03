package imaged

// adr: 435. Composed publication failure and findings use durable quarantine.

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) rescanProducedRuntime(ctx context.Context, app state.App, dep state.Deployment) bool {
	present, err := producedRuntimePresent(ctx, l.store, app, dep)
	if !present && err == nil {
		return false
	}
	if producedEvidenceBusy(err) || ctx.Err() != nil {
		return true
	}
	scans, ok := l.store.(state.DeploymentRuntimeScanStore)
	if !ok {
		if app.SecurityPolicy == api.AppSecurityPolicyEnforce {
			if err := l.quarantineSecurityRegression(ctx, app, dep); err != nil {
				l.log.Warn("imaged: quarantine missing composed scan store", "deployment", dep.ID)
			}
		}
		return true
	}
	previous, _ := scans.GetCurrentDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	scanErr := l.handler.runProducedRuntimeScanGate(ctx, app, dep)
	if producedEvidenceBusy(scanErr) || ctx.Err() != nil {
		return true
	}
	if app.SecurityPolicy != api.AppSecurityPolicyEnforce {
		return true
	}
	fresh, readErr := scans.GetFreshDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	if producedEvidenceBusy(readErr) || ctx.Err() != nil {
		return true
	}
	if scanErr == nil && readErr == nil && privateSecurityScanFailure(fresh) == "" {
		return true
	}
	current, _ := scans.GetCurrentDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	if err := l.appendRuntimeScanRegression(ctx, app, dep, previous, current); err != nil {
		l.log.Warn("imaged: append composed scan regression", "deployment", dep.ID)
	}
	if err := l.quarantineSecurityRegression(ctx, app, dep); err != nil {
		l.log.Warn("imaged: quarantine composed scan regression", "deployment", dep.ID)
	}
	return true
}

func (l *Loop) appendRuntimeScanRegression(ctx context.Context, app state.App, dep state.Deployment, previous, current state.DeploymentRuntimeScan) error {
	if previous.ID == current.ID && previous.Input.Status != "complete" {
		return nil
	}
	deploymentID, err := uuid.Parse(dep.ID)
	if err != nil {
		return err
	}
	accountID, err := uuid.Parse(app.AccountID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"app_id": app.ID, "previous_scan_id": previous.ID, "current_scan_id": current.ID, "composed_input_hash": current.Input.Facts.InputHash, "current_status": current.Input.Status, "quarantine_required": true, "source": "scheduled_composed_runtime_scan"})
	if err != nil {
		return err
	}
	_, err = l.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: deploymentID, AccountID: &accountID, Kind: state.DeployScanRegressed, Actor: "system:imaged-runtime-scan", Data: raw})
	return err
}
