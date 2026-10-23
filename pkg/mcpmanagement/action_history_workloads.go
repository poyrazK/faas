package mcpmanagement

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// Older bridges reject unfamiliar action kinds in actions/. Keep the extension
// separate while committing both histories and replay payloads in one snapshot.
const workloadActionsNamespace = "workload-actions/"

type actionWorkload struct {
	Mode                    string `json:"mode"`
	DeploymentID            string `json:"deployment_id,omitempty"`
	PredecessorDeploymentID string `json:"predecessor_deployment_id,omitempty"`
	RetainedDeploymentID    string `json:"retained_deployment_id,omitempty"`
}

type actionObservation struct {
	Tool      string                `json:"tool"`
	Arguments actionObservationArgs `json:"arguments"`
}

type actionObservationArgs struct {
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Project      string `json:"project,omitempty"`
	Environment  string `json:"environment,omitempty"`
}

func workloadHistoryAction(action string) bool {
	return action == "workload_rollout" || action == "service_abort"
}

func workloadActionPhase(p *releasePlan) actionPhase {
	phase := actionPhase{State: "not_attempted"}
	if p.attempted {
		phase.State = "outcome_unknown"
	}
	d := p.result
	if !p.attempted || d == nil || validateSegment(d.ID, "deployment_id") != nil || d.AppID != p.AppID || normalizedScope(d.Scope) != p.Scope {
		return phase
	}
	if p.Action == "workload_rollout" && (!nativeRolloutArtifactMatches(p, *d) || d.ID == p.TargetDeploymentID || p.RolloutMode == api.ExecutionModeService && (d.RolloutState != "rolling_out" || d.TrafficPercent != 0) || p.RolloutMode == api.ExecutionModeWorker && d.TrafficPercent != 100) {
		return phase
	}
	r := &actionReceipt{DeploymentID: d.ID}
	if p.Action == "service_abort" {
		recovery := p.ServiceRecovery
		if recovery == nil || recovery.Status != "accepted" || recovery.DeploymentID != p.TargetDeploymentID || recovery.DeploymentID != d.ID || recovery.PredecessorDeploymentID != p.PredecessorDeploymentID || validateSegment(recovery.RequestID, "recovery_request_id") != nil {
			return phase
		}
		r.RecoveryRequestID = recovery.RequestID
	}
	if h := d.ServiceRolloutHandoff; h != nil && p.RolloutMode == api.ExecutionModeService {
		if h.Generation < 0 || p.Action == "service_abort" && h.PredecessorDeploymentID != "" && h.PredecessorDeploymentID != p.PredecessorDeploymentID {
			return phase
		}
		r.HandoffGeneration = h.Generation
	}
	return actionPhase{State: "accepted", Receipt: r}
}

func validateWorkloadAction(entry actionRecord) error {
	w := entry.Workload
	if workloadHistoryAction(entry.Action) {
		if w == nil || w.Mode != api.ExecutionModeService && w.Mode != api.ExecutionModeWorker {
			return errors.New("invalid action workload mode")
		}
		if entry.Action == "service_abort" {
			if w.Mode != api.ExecutionModeService || validateSegment(w.DeploymentID, "deployment_id") != nil || validateSegment(w.PredecessorDeploymentID, "predecessor_deployment_id") != nil || w.DeploymentID == w.PredecessorDeploymentID || w.RetainedDeploymentID != "" {
				return errors.New("invalid action recovery pair")
			}
		} else if w.DeploymentID != "" || w.PredecessorDeploymentID != "" || w.RetainedDeploymentID != "" && validateSegment(w.RetainedDeploymentID, "retained_deployment_id") != nil {
			return errors.New("invalid action retained generation")
		}
	} else if w != nil {
		return errors.New("unexpected action workload")
	}
	for _, phase := range []*actionPhase{&entry.Apply, entry.Promotion} {
		if phase == nil || phase.Receipt == nil {
			continue
		}
		r := phase.Receipt
		if !workloadHistoryAction(entry.Action) {
			if r.RecoveryRequestID != "" || r.HandoffGeneration != 0 {
				return errors.New("unexpected action workload receipt")
			}
			continue
		}
		if r.HandoffGeneration < 0 || w.Mode == api.ExecutionModeWorker && r.HandoffGeneration != 0 || r.RollbackOperationID != "" || r.RegressionVersion != "" || r.TriageAction != "" || r.TriageState != "" {
			return errors.New("invalid action workload receipt")
		}
		if entry.Action == "service_abort" {
			if r.DeploymentID != w.DeploymentID || validateSegment(r.RecoveryRequestID, "recovery_request_id") != nil {
				return errors.New("invalid action reverse-handoff receipt")
			}
		} else if r.RecoveryRequestID != "" || r.DeploymentID == w.RetainedDeploymentID {
			return errors.New("invalid action rollout generation")
		}
	}
	return nil
}

func workloadActionObservation(entry actionRecord) *actionObservation {
	if entry.Workload == nil {
		return nil
	}
	id := entry.Workload.DeploymentID
	if r := entry.Apply.Receipt; r != nil {
		id = r.DeploymentID
	}
	if id == "" {
		return nil
	}
	environment := entry.Scope
	if environment == api.DefaultEnvScope {
		environment = ""
	}
	return &actionObservation{Tool: "gregale_get_rollout", Arguments: actionObservationArgs{App: entry.App, DeploymentID: id, Project: entry.Project, Environment: environment}}
}
