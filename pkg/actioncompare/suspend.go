package actioncompare

import (
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func suspendResumeDigest(s *api.ScenarioActionSnapshot) string {
	if s.SuspendResume == nil {
		return ""
	}
	return s.SuspendResume.PlanSHA256
}
func suspendResumeBudgetExceeded(s *api.ScenarioActionSnapshot) bool {
	if s.SuspendResume != nil {
		for _, r := range s.SuspendResume.Receipts {
			if r.ResumeMS > s.SuspendResume.Spec.WithinMS {
				return true
			}
		}
	}
	return false
}
func addSuspendResumeGroups(s *api.ScenarioActionSnapshot, groups map[string]Change) error {
	e := s.SuspendResume
	if e == nil {
		return nil
	}
	if s.ColdStart != nil || e.RunID != s.RunID || e.Validate(s.Workloads) != nil {
		return errors.New("suspend/resume lifecycle evidence is incomplete")
	}
	bound := map[string]string{}
	for _, r := range e.Receipts {
		if e.Spec.ManagedJob() {
			if err := validateRunningSuspend(s, r); err != nil {
				return err
			}
		}
		bound[r.Workload] = r.InstanceID
		if s.ManagedWork != nil {
			for _, b := range s.ManagedWork.Bindings {
				if b.Workload == r.Workload && (b.AppID != r.AppID || b.DeploymentID != r.DeploymentID) {
					return errors.New("suspend/resume managed-work binding differs")
				}
			}
		}
		outcome := "within_budget"
		if r.ResumeMS > e.Spec.WithinMS {
			outcome = "budget_exceeded"
		}
		payload, _ := json.Marshal(struct {
			Method  string `json:"method"`
			Outcome string `json:"outcome"`
		}{r.Method, outcome})
		key := rehearsalwork.Hash([]byte(r.Workload + "\x00lifecycle\x00suspend-resume"))
		groups[key] = Change{Key: key, Workload: r.Workload, Source: "lifecycle", Name: "suspend-resume", Before: []Value{{ReceiverSHA256: e.PlanSHA256, Type: "lifecycle.suspend_resume", Payload: payload}}}
	}
	if s.ManagedWork != nil {
		for _, job := range s.ManagedWork.Jobs {
			if instance, selected := bound[job.Workload]; selected {
				for _, a := range job.Attempts {
					if a.InstanceID != instance {
						return errors.New("managed work did not use the verified resumed instance")
					}
				}
			}
		}
	}
	return nil
}

func validateRunningSuspend(s *api.ScenarioActionSnapshot, r rehearsalresume.Receipt) error {
	e := s.SuspendResume
	if s.ManagedWork == nil || s.ManagedWork.Plan.WorkerTermination != nil {
		return errors.New("running-job managed-work scope is unavailable")
	}
	workers := 0
	for _, p := range s.ManagedWork.Plan.Participants {
		if p.Async {
			workers++
			if p.Workload != r.Workload {
				return errors.New("running-job worker differs")
			}
		}
	}
	if workers != 1 {
		return errors.New("running-job worker inventory differs")
	}
	// Enrollment was validated with the immutable fixture manifest. Sealed evidence
	// must independently contain that exact completed broker attempt and job.
	found := false
	for _, a := range s.SimulatedAttempts {
		if a.ID == r.CheckpointAttemptID && a.Workload == r.Workload && a.Fixture == e.Spec.WorkerCheckpoint && a.Adapter == "http" && a.Method == "POST" && a.ResponseStatus == 200 && a.Fault == "" && a.Status == "completed" {
			found = true
		}
	}
	if !found || len(s.ManagedWork.Jobs) != 1 {
		return errors.New("running-job checkpoint simulation or single-job inventory is missing")
	}
	j := s.ManagedWork.Jobs[0]
	if j.ID != r.InvocationID || j.Workload != r.Workload || j.AppID != r.AppID || j.DeploymentID != r.DeploymentID || len(j.Attempts) == 0 || j.Attempts[0].Number != r.InvocationAttempt || j.Attempts[0].InstanceID != r.InstanceID || j.Attempts[0].DeploymentID != r.DeploymentID {
		return errors.New("running-job checkpoint differs from its independently journaled first dispatch")
	}
	return nil
}
