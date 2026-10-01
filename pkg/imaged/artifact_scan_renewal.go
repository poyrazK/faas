package imaged

// adr: 393

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Separate from the legacy sweep: private five-minute component evidence must
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
	roots, ok := l.store.(state.DeploymentRegistryRootfsStore)
	scans, scansOK := l.store.(state.DeploymentArtifactScanStore)
	if !ok || !scansOK {
		return
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		l.log.Warn("imaged: list deployments for producer evidence renewal")
		return
	}
	for _, dep := range deployments {
		if ctx.Err() != nil {
			return
		}
		if dep.Status != state.DeployLive || dep.Kind != state.DeploymentKindImage || dep.ParkedReason != "" {
			continue
		}
		app, err := l.store.AppByID(ctx, dep.AppID)
		if err != nil || app.Status != state.AppActive {
			continue
		}
		root, err := roots.GetCurrentDeploymentRegistryRootfs(ctx, app.AccountID, app.ID, dep.ID, "")
		if err != nil || len(root.Input.Layers) == 0 && root.Input.BaseProducerID == "" {
			continue
		}
		due, err := l.producedDeploymentRenewalDue(ctx, scans, app, dep, root, now.UTC())
		if err != nil {
			l.log.Warn("imaged: read component evidence for renewal", "deployment", dep.ID)
			continue
		}
		if !due {
			continue
		}
		l.rescanLiveDeployment(ctx, app, dep)
	}
}

func (l *Loop) producedDeploymentRenewalDue(ctx context.Context, scans state.DeploymentArtifactScanStore, app state.App, dep state.Deployment, root state.DeploymentRegistryRootfs, now time.Time) (bool, error) {
	if root.Input.BaseProducerID != "" {
		bases, ok := l.store.(state.BaseImageScanStore)
		if !ok {
			return true, nil
		}
		base, err := bases.GetFreshBaseImageScan(ctx, root.Input.BaseProducerID, root.Input.BaseInputHash)
		if err != nil || producedEvidenceRenewalDue(base.ScannedAt, base.ExpiresAt, now) {
			return true, nil
		}
	}
	workloads := []string{""}
	var sidecars api.Sidecars
	if len(dep.Sidecars) > 0 && json.Unmarshal(dep.Sidecars, &sidecars) != nil {
		return true, nil
	}
	for _, sidecar := range sidecars {
		if sidecar.Image != "" {
			workloads = append(workloads, sidecar.Name)
		}
	}
	for _, workload := range workloads {
		current, err := scans.GetCurrentDeploymentArtifactScan(ctx, app.AccountID, app.ID, dep.ID, workload)
		if errors.Is(err, state.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if producedScanRenewalDue(current, now) {
			return true, nil
		}
	}
	return false, nil
}

func producedScanRenewalDue(value state.DeploymentArtifactScan, now time.Time) bool {
	return value.Input.Status != "complete" || producedEvidenceRenewalDue(value.ScannedAt, value.ExpiresAt, now)
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
