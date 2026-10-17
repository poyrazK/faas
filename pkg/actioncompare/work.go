package actioncompare

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func managedWorkDigest(s *api.ScenarioActionSnapshot) string {
	if s.ManagedWork == nil {
		return ""
	}
	return s.ManagedWork.PlanSHA256
}

func addManagedWorkGroups(s *api.ScenarioActionSnapshot, groups map[string]Change, ids map[string]bool) error {
	e := s.ManagedWork
	if e == nil {
		return nil
	}
	if e.Validate() != nil || e.RunID != s.RunID || len(e.Bindings) != len(s.Workloads) {
		return errors.New("managed-work evidence is incomplete")
	}
	if fault := e.WorkerFault; fault != nil {
		found := false
		for _, attempt := range s.SimulatedAttempts {
			found = found || (attempt.ID == fault.CheckpointAttemptID && attempt.Workload == fault.Workload && attempt.Fixture == e.Plan.WorkerTermination.Checkpoint && attempt.Status == "completed")
		}
		if !found {
			return errors.New("worker checkpoint simulation receipt is missing")
		}
	}
	names := map[string]bool{}
	for _, w := range s.Workloads {
		names[w] = true
	}
	for _, b := range e.Bindings {
		if !names[b.Workload] {
			return errors.New("managed-work inventory differs")
		}
	}
	for _, j := range e.Jobs {
		parsed, _ := uuid.Parse(j.ID)
		if ids[parsed.String()] {
			return errors.New("duplicate managed-work identity")
		}
		ids[parsed.String()] = true
		key := rehearsalwork.Hash([]byte(j.Workload + "\x00async\x00" + j.Method + "\x00" + j.Path))
		outcomes := make([]string, 0, len(j.Attempts))
		for _, a := range j.Attempts {
			outcomes = append(outcomes, a.Outcome)
		}
		payload, _ := json.Marshal(struct {
			PayloadSHA256 string   `json:"payload_sha256"`
			Outcomes      []string `json:"outcomes"`
		}{j.PayloadSHA256, outcomes})
		g := groups[key]
		if g.Key == "" {
			g = Change{Key: key, Workload: j.Workload, Source: "async", Name: j.Method + " " + j.Path}
		}
		g.Before = append(g.Before, Value{ReceiverSHA256: e.PlanSHA256, Type: "managed.async", Payload: payload})
		groups[key] = g
	}
	return nil
}
