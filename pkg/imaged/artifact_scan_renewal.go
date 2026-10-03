package imaged

// adr: 435

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Separate from the legacy sweep: private five-minute composed evidence must
// renew even when the configured legacy scan interval is six hours.
func (l *Loop) runProducedEvidenceRenewal(ctx context.Context) {
	ticker := time.NewTicker(api.ApplicationStandardArtifactScanRenewEvery)
	defer ticker.Stop()
	l.reconcileProducedSecurityScans(ctx, l.now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			l.reconcileProducedSecurityScans(ctx, now)
		}
	}
}

func (l *Loop) reconcileProducedSecurityScans(ctx context.Context, now time.Time) {
	if l == nil || l.store == nil || l.handler == nil {
		return
	}
	scans, ok := l.store.(state.DeploymentRuntimeScanStore)
	if !ok {
		return
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		l.log.Warn("imaged: list deployments for composed scan renewal")
		return
	}
	for _, dep := range deployments {
		if ctx.Err() != nil {
			return
		}
		if dep.Status != state.DeployLive && dep.Status != state.DeploySnapshotting || dep.ParkedReason == string(state.ParkReasonSecurityScanRegressed) {
			continue
		}
		app, err := l.store.AppByID(ctx, dep.AppID)
		if err != nil || app.Status != state.AppActive {
			continue
		}
		present, err := producedRuntimePresent(ctx, l.store, app, dep)
		if err != nil {
			l.log.Warn("imaged: read producers for composed renewal", "deployment", dep.ID)
			continue
		}
		if !present {
			continue
		}
		due, err := producedRuntimeRenewalDue(ctx, scans, app, dep, now.UTC())
		if err != nil {
			l.log.Warn("imaged: read composed evidence for renewal", "deployment", dep.ID)
			continue
		}
		if !due {
			continue
		}
		l.renewProducedRuntimeDeployment(ctx, app, dep)
	}
}

// Pending release/prime work can outlive a scan lease. Its facts must renew
// without quarantining the app's previously serving deployment.
func (l *Loop) renewProducedRuntimeDeployment(ctx context.Context, app state.App, dep state.Deployment) {
	if dep.Status == state.DeployLive {
		l.rescanLiveDeployment(ctx, app, dep)
		return
	}
	if err := l.handler.runProducedRuntimeScanGate(ctx, app, dep); err != nil && !producedEvidenceBusy(err) && ctx.Err() == nil {
		l.log.Warn("imaged: pending composed scan renewal refused", "deployment", dep.ID)
	}
}

func producedRuntimeRenewalDue(ctx context.Context, scans state.DeploymentRuntimeScanStore, app state.App, dep state.Deployment, now time.Time) (bool, error) {
	current, err := scans.GetCurrentDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if current.Input.Status != "complete" || producedEvidenceRenewalDue(current.ScannedAt, current.ExpiresAt, now) {
		return true, nil
	}
	fresh, err := scans.GetFreshDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	if producedEvidenceBusy(err) || ctx.Err() != nil {
		return false, err
	}
	if err != nil {
		return true, nil
	} // A failed current binding cannot be reused.
	return producedEvidenceRenewalDue(current.ScannedAt, fresh.ExpiresAt, now), nil
}

func producedEvidenceRenewalDue(at, expires, now time.Time) bool {
	return at.After(now) || !at.Add(api.ApplicationStandardArtifactScanRenewEvery).After(now) || !expires.After(now.Add(api.ApplicationStandardArtifactScanRenewEvery))
}

// A concurrent owner must finish before another worker can publish evidence.
// Busy preserves a refusal at admission and is retried by the live worker;
// it neither creates failed findings nor extends the selected evidence lease.
func producedEvidenceBusy(err error) bool {
	return errors.Is(err, state.ErrApplicationStandardRuntimeBusy) || errors.Is(err, state.ErrApplicationStandardReviewBusy)
}
