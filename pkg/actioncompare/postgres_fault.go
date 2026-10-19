package actioncompare

import (
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func postgresFaultBudgetExceeded(s *api.ScenarioActionSnapshot) bool {
	return s != nil && s.ManagedWork != nil && s.ManagedWork.PostgresFault != nil && s.ManagedWork.PostgresFault.RecoveryMS > s.ManagedWork.PostgresFault.Spec.RecoverWithinMS
}

func addPostgresFaultGroup(s *api.ScenarioActionSnapshot, groups map[string]Change) error {
	if s.ManagedWork == nil || s.ManagedWork.PostgresFault == nil {
		return nil
	}
	f := s.ManagedWork.PostgresFault
	found := false
	for _, a := range s.SimulatedAttempts {
		found = found || (a.ID == f.CheckpointAttemptID && a.Workload == f.Spec.Workload && a.Fixture == f.Spec.Checkpoint && a.Integration == "checkpoint" && a.Adapter == "http" && a.Method == "POST" && a.Status == "completed" && a.ResponseStatus == 200 && a.Fault == "")
	}
	if !found {
		return errors.New("PostgreSQL fault checkpoint simulation receipt is missing")
	}
	outcome := "recovered"
	if postgresFaultBudgetExceeded(s) {
		outcome = "budget_exceeded"
	}
	payload, _ := json.Marshal(struct {
		Method  string `json:"method"`
		Outcome string `json:"outcome"`
	}{"disconnect_reject_reconnect", outcome})
	key := rehearsalwork.Hash([]byte(f.Spec.Workload + "\x00dependency\x00postgres"))
	groups[key] = Change{Key: key, Workload: f.Spec.Workload, Source: "dependency", Name: "postgres", Before: []Value{{ReceiverSHA256: f.PlanSHA256, Type: "dependency.postgres_recovery", Payload: payload}}}
	return nil
}
