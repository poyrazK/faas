package imaged

// adr: 435. Deployment and live policy consume composed findings.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Any retained main or image-sidecar producer prevents legacy scan fallback,
// including incomplete lineage. Freshness is established by the scan store.
func producedRuntimePresent(ctx context.Context, store state.Store, app state.App, dep state.Deployment) (bool, error) {
	if presence, ok := store.(state.DeploymentRuntimeProducerPresenceStore); ok {
		return presence.HasDeploymentRuntimeProducers(ctx, app.AccountID, app.ID, dep.ID)
	}
	if _, private := store.(state.DeploymentRegistryRootfsStore); private {
		return true, state.ErrApplicationStandardsPending
	}
	return false, nil
}

func (h *Handler) runProducedRuntimeScanGate(ctx context.Context, app state.App, dep state.Deployment) error {
	start := time.Now()
	if _, err := h.renewProducedDeploymentSignatures(ctx, app, dep); err != nil {
		return runtimeScanPipelineFailure(ctx, err)
	}
	value, err := h.ScanAndPublishProducedRuntime(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return runtimeScanPipelineFailure(ctx, err)
	}
	h.observeProducedRuntimeScan(app, value, time.Since(start))
	reader, ok := h.store.(state.DeploymentRuntimeScanStore)
	if !ok {
		return verifiedScanFailure(api.AppSecurityPolicyEnforce, "composed scan store unavailable")
	}
	current, err := reader.GetFreshDeploymentRuntimeScan(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return runtimeScanPipelineFailure(ctx, err)
	}
	if app.SecurityPolicy == api.AppSecurityPolicyEnforce && privateSecurityScanFailure(current) != "" {
		return verifiedScanFailure(app.SecurityPolicy, "composed scan found blocking vulnerabilities")
	}
	return ctx.Err()
}

func runtimeScanPipelineFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if producedEvidenceBusy(err) {
		return err
	}
	// All retained producer paths require complete evidence even in advisory
	// mode. Safe detail avoids returning native paths or scanner output.
	return verifiedScanFailure(api.AppSecurityPolicyEnforce, "composed runtime evidence unavailable")
}

func (h *Handler) observeProducedRuntimeScan(app state.App, value state.DeploymentRuntimeScan, duration time.Duration) {
	h.ops.ObserveDeployScanDuration(app.Slug, value.Input.Status, duration)
	h.ops.ObserveDeployScanTotal(app.Slug, value.Input.Status)
	for _, view := range value.Input.Reports {
		for severity, count := range map[string]int{SeverityCritical: view.Report.SeverityCounts.Critical, SeverityHigh: view.Report.SeverityCounts.High, SeverityMedium: view.Report.SeverityCounts.Medium, SeverityLow: view.Report.SeverityCounts.Low, SeverityUnknown: view.Report.SeverityCounts.Unknown} {
			h.ops.ObserveDeployScanVulns(app.Slug, severity, count)
		}
	}
	h.log.Info("imaged: composed runtime scan published", "deployment", value.Input.DeploymentID, "scan_id", value.ID)
}
