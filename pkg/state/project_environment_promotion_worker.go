package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrPromotionWorkerNotReady = errors.New("promotion worker has no current runtime startup proof")

func promotionSnapshotWorkload(w *promotionReleaseWorkload, p ProjectEnvironmentPromotion) bool {
	if w.SourceSettings.WorkloadClass != WorkloadClassWorker && w.TargetSettings.WorkloadClass != WorkloadClassWorker {
		return promotionSnapshotHTTP(w.SourceSettings, w.Source, p.Migrate) && promotionSnapshotHTTP(w.TargetSettings, w.Previous, p.Migrate)
	}
	if !p.Workers {
		return false
	}
	for _, pair := range []struct {
		settings ProjectEnvironmentWorkloadSettings
		artifact projectCloneArtifact
	}{{w.SourceSettings, w.Source}, {w.TargetSettings, w.Previous}} {
		s, a := pair.settings, pair.artifact
		if s.WorkloadClass != WorkloadClassWorker || s.Manifest.ExecutionMode != api.ExecutionModeWorker || s.WorkPolicies != nil || len(a.ReleaseCommand) != 0 || a.ReleaseCommandShell || nonemptySecretJSON(a.Sidecars) || nonemptySecretJSON(a.OverrideEnvSecrets) || a.SecretReloadSignal != "" {
			return false
		}
	}
	return true
}

func (c *promotionReleaseConfiguration) prepareWorker(p ProjectEnvironmentPromotion) error {
	if !p.Workers {
		if len(c.workers()) > 0 {
			return ErrReleaseConfigurationTransferUnsupported
		}
		return nil
	}
	if c.To != "production" || len(c.workers()) == 0 || len(c.workers()) > api.ProjectPromotionMaxWorkers {
		return ErrReleaseConfigurationTransferUnsupported
	}
	if len(c.workers()) > 1 && 2*len(c.workers()) > api.MustLimitsFor(c.Plan).WorkerReplicasMax {
		return ErrReleaseConfigurationTransferUnsupported
	}
	count := 0
	policies := map[string]*api.ProjectReleaseWorker{}
	for _, worker := range c.workers() {
		if worker == nil || policies[worker.AppID] != nil {
			return ErrConflict
		}
		policies[worker.AppID] = worker
	}
	for _, w := range c.Workloads {
		if w.SourceSettings.WorkloadClass != WorkloadClassWorker && w.TargetSettings.WorkloadClass != WorkloadClassWorker {
			continue
		}
		count++
		if w.SourceSnapshot == nil && !w.SourceWorkerQualified {
			return ErrReleaseConfigurationTransferUnsupported
		}
		grace := func(s ProjectEnvironmentWorkloadSettings) int {
			if s.Manifest.StopGracePeriodS == 0 {
				return api.ProjectReleaseWorkerDefaultDrainSeconds
			}
			return s.Manifest.StopGracePeriodS
		}
		worker := policies[w.AppID]
		if worker == nil || w.Previous.ID != worker.PreviousDeploymentID || grace(w.SourceSettings) != worker.DrainSeconds || grace(w.TargetSettings) != worker.DrainSeconds {
			return ErrReleaseConfigurationTransferUnsupported
		}
	}
	if count != len(policies) {
		return ErrReleaseConfigurationTransferUnsupported
	}
	return nil
}
func promotionWorkerPolicy(c promotionReleaseConfiguration, worker *api.ProjectReleaseWorker) (*api.ProjectReleaseWorker, error) {
	for _, w := range c.Workloads {
		if w.AppID == worker.AppID {
			copy := *worker
			copy.PreviousDeploymentID = w.Previous.ID
			return &copy, nil
		}
	}
	return nil, ErrConflict
}
func validatePromotionWorkerAdmission(p ProjectEnvironmentPromotion, c *promotionReleaseConfiguration) error {
	if c == nil || len(c.workers()) == 0 {
		if p.Workers || len(api.ProjectPromotionWorkers(p.HealthPolicy)) > 0 {
			return ErrInvalidArgument
		}
		return nil
	}
	if !p.Workers || p.HealthPolicy == nil || len(api.ProjectPromotionWorkers(p.HealthPolicy)) == 0 || api.ProjectPromotionWorkerSetID(c.Worker, c.Workers) != api.ProjectPromotionWorkerSetID(p.HealthPolicy.Worker, p.HealthPolicy.Workers) {
		return ErrInvalidArgument
	}
	return nil
}
func copyPromotionWorker(w *api.ProjectReleaseWorker) *api.ProjectReleaseWorker {
	if w == nil {
		return nil
	}
	copy := *w
	return &copy
}

// These samples are produced by the store from the owned delivery ledger.
func promotionWorkerSample(h *api.ProjectPromotionHealth, now time.Time) *api.ProjectSafeRelease {
	deadline := h.Deadline
	if h.Phase == "preflight" || h.Phase == "cutover" || h.Phase == "restoring" {
		deadline = now.Add(api.ProjectReleaseVerificationTimeout)
	}
	checked := h.CheckedAt
	if h.Phase == "preflight" || h.Phase == "cutover" || h.Phase == "restoring" {
		checked = nil
	}
	return &api.ProjectSafeRelease{Worker: h.Worker, Verification: &api.ProjectReleaseVerification{Deadline: &deadline, CheckedAt: checked, WorkerHealth: h.WorkerHealth}}
}
func promotionWorkerBaselineHealthy(h *api.ProjectPromotionHealth) bool {
	for _, one := range promotionWorkerHealthViews(h) {
		if one.WorkerHealth == nil {
			return false
		}
		b := one.WorkerHealth.Baseline
		if ProjectWorkerJobVerdict(b, b) != "healthy" {
			return false
		}
	}
	return true
}
func promotionWorkerVerdict(h *api.ProjectPromotionHealth, verdict string) string {
	views := promotionWorkerHealthViews(h)
	if len(views) == 0 {
		return verdict
	}
	if !promotionWorkerBaselineHealthy(h) {
		return "unknown"
	}
	if verdict == "regressed" {
		return verdict
	}
	if h.Phase != "preflight" {
		for _, one := range views {
			if one.WorkerHealth != nil && one.WorkerHealth.Status == "regressed" && verdict == "healthy" {
				return "regressed"
			}
		}
	}
	if !promotionWorkersReady(h) {
		return "unknown"
	}
	if h.Phase == "preflight" {
		return verdict
	}
	for _, one := range views {
		if one.WorkerHealth.Status != "healthy" {
			return "unknown"
		}
	}
	return verdict
}

// Desired production settings cannot reclassify an immutable retained HTTP
// deployment as a worker rollback target.
func validatePromotionWorkerBaseline(settings ProjectEnvironmentWorkloadSettings, worker *api.ProjectReleaseWorker) error {
	grace := settings.Manifest.StopGracePeriodS
	if grace == 0 {
		grace = api.ProjectReleaseWorkerDefaultDrainSeconds
	}
	if settings.WorkloadClass != WorkloadClassWorker || settings.Manifest.ExecutionMode != api.ExecutionModeWorker || grace != worker.DrainSeconds {
		return ErrReleaseConfigurationTransferUnsupported
	}
	return nil
}
