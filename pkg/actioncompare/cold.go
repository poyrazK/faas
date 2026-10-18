package actioncompare

import (
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func coldStartDigest(s *api.ScenarioActionSnapshot) string {
	if s.ColdStart == nil {
		return ""
	}
	return s.ColdStart.PlanSHA256
}

func coldBudgetExceeded(s *api.ScenarioActionSnapshot) bool {
	if s.ColdStart != nil {
		for _, p := range s.ColdStart.Preparations {
			if p.StartupMS > s.ColdStart.Spec.WithinMS {
				return true
			}
		}
	}
	return false
}

func addColdStartGroups(s *api.ScenarioActionSnapshot, groups map[string]Change) error {
	e := s.ColdStart
	if e == nil {
		return nil
	}
	if e.RunID != s.RunID || e.Validate(s.Workloads) != nil {
		return errors.New("cold-start evidence is incomplete")
	}
	bindings := map[string]string{}
	for _, p := range e.Preparations {
		bindings[p.Workload] = p.BootInstanceID
		if s.ManagedWork != nil {
			for _, b := range s.ManagedWork.Bindings {
				if b.Workload == p.Workload && (b.AppID != p.AppID || b.DeploymentID != p.DeploymentID) {
					return errors.New("cold-start managed-work binding differs")
				}
			}
		}
		outcome := "within_budget"
		if p.StartupMS > e.Spec.WithinMS {
			outcome = "budget_exceeded"
		}
		payload, _ := json.Marshal(struct {
			Method  string `json:"method"`
			Outcome string `json:"outcome"`
		}{p.Method, outcome})
		key := rehearsalwork.Hash([]byte(p.Workload + "\x00lifecycle\x00cold-start"))
		groups[key] = Change{Key: key, Workload: p.Workload, Source: "lifecycle", Name: "cold-start", Before: []Value{{ReceiverSHA256: e.PlanSHA256, Type: "lifecycle.cold_start", Payload: payload}}}
	}
	if s.ManagedWork != nil {
		for _, job := range s.ManagedWork.Jobs {
			for _, a := range job.Attempts {
				if a.InstanceID != bindings[job.Workload] {
					return errors.New("managed work did not use the verified cold-start instance")
				}
			}
		}
	}
	return nil
}
