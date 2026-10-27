package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A stabilization checkpoint is complete as a rollout, but still owns a bounded watch.
func decideProjectStabilization(p ProjectCanaryPolicyState, o ProjectCanaryObservation, now time.Time) (ProjectCanaryPolicyState, ProjectCanaryEffect, error) {
	if err := p.validate(); err != nil {
		return p, ProjectCanaryNoEffect, err
	}
	if !p.Stabilizing || p.Status != "complete" || o.Generation != p.Generation || !sameCanaryCheckpoint(p.CheckedAt, o.PreviousCheckedAt) || now.Before(p.StageStartedAt) {
		return p, ProjectCanaryNoEffect, nil
	}
	if !now.Before(p.Deadline) {
		p.Generation++
		return p, ProjectCanaryComplete, nil
	}
	temporary := p
	temporary.Status, temporary.Stabilizing = "running", false
	next, effect, err := DecideProjectCanary(temporary, o, now)
	if err != nil {
		return p, effect, err
	}
	next.Stabilizing = true
	if effect == ProjectCanaryComplete {
		next.HealthyRounds = 0
		effect = ProjectCanaryNoEffect
	}
	if next.Status == "running" || next.Status == "complete" {
		next.Status = "complete"
	}
	return next, effect, nil
}

func advanceProjectStabilization(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState, round *ProjectReleaseVerificationRound, ready, baselineReady bool, now time.Time, effects canaryEffects) (ProjectCanaryPolicyState, error) {
	if !api.ProjectReleaseMonitoring(safe) {
		return p, nil
	}
	observation := canaryObservation(safe, p, round, ready, baselineReady, now)
	next, effect, err := decideProjectStabilization(p, observation, now)
	if err != nil || next.Generation == p.Generation {
		return p, err
	}
	monitor := *safe.Verification.Stabilization
	safe.Verification.Stabilization = &monitor
	if effect != ProjectCanaryComplete {
		monitor.Health = "unknown"
		if observation.BaselineHealthy && observation.HTTPVerdict == "regressed" {
			monitor.Health = "regressed"
		}
		if observation.BaselineHealthy && (observation.WorkerVerdict == "healthy" || !p.WorkerRequired) {
			monitor.Health = observation.HTTPVerdict
		}
		if observation.BaselineHealthy && p.WorkerRequired && observation.WorkerVerdict == "regressed" && observation.HTTPVerdict == "healthy" {
			monitor.Health = "regressed"
		}
	}
	switch effect {
	case ProjectCanaryRestore:
		if err = effects.restore(); err != nil {
			return p, err
		}
		next.Status = "rolled_back"
		monitor.Status = "rolled_back"
		safe.Message = "Stabilization detected repeated regressions with a healthy retained baseline; graph and configuration were restored together."
		if safe.Worker != nil {
			safe.Worker.Status, safe.Worker.DeploymentID, safe.Worker.InstanceID = "restored", safe.Worker.PreviousDeploymentID, ""
		}
	case ProjectCanaryComplete:
		monitor.Status = "completed"
		safe.Message = "Stabilization window ended; monitoring health is reported separately from rollout success."
	default:
		safe.Message = "Rollout succeeded; stabilization continues watching HTTP and worker health."
	}
	if round != nil && effect != ProjectCanaryComplete {
		safe.Verification.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), round.Evidence...)
	}
	syncCanaryVerification(safe, next)
	return next, next.validate()
}

func (m *MemStore) stabilizationSupersededLocked(c projectCanaryRecord) bool {
	if !c.Policy.Stabilizing {
		return false
	}
	for _, other := range m.projectCanaries {
		if other.ProjectID == c.ProjectID && other.AttemptID != c.AttemptID && other.Policy.Status == "running" {
			return true
		}
	}
	return false
}
