package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"sort"
)

func coldEvidence(snapshot *api.ScenarioActionSnapshot, raw string) *rehearsalcold.Evidence {
	if raw == "" {
		return nil
	}
	spec := rehearsalcold.Decode(raw)
	_, digest, _ := rehearsalcold.Normalize(spec, snapshot.Workloads)
	return &rehearsalcold.Evidence{Version: 1, RunID: snapshot.RunID, Spec: spec, PlanSHA256: digest, Status: "complete", Preparations: []rehearsalcold.Preparation{}}
}
func finishColdEvidence(s *api.ScenarioActionSnapshot) {
	if s.ColdStart == nil {
		return
	}
	sort.Slice(s.ColdStart.Preparations, func(i, j int) bool {
		return s.ColdStart.Preparations[i].Workload < s.ColdStart.Preparations[j].Workload
	})
	if s.ColdStart.Validate(s.Workloads) != nil {
		s.ColdStart.Status = "incomplete"
	}
}
func addScenarioColdSnapshot(ctx context.Context, tx pgx.Tx, s *api.ScenarioActionSnapshot, policies []sqlc.ScenarioActionPoliciesForRunRow) error {
	if len(policies) == 0 || len(policies[0].ColdStartSpec) == 0 {
		return nil
	}
	s.ColdStart = coldEvidence(s, string(policies[0].ColdStartSpec))
	q := sqlc.New()
	for _, p := range policies {
		var spec rehearsalcold.Spec
		if json.Unmarshal(p.ColdStartSpec, &spec) != nil || spec != s.ColdStart.Spec {
			s.ColdStart.Status = "incomplete"
			continue
		}
		r, err := q.ScenarioColdPreparation(ctx, tx, p.AppID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		s.ColdStart.Preparations = append(s.ColdStart.Preparations, coldRow(r))
	}
	finishColdEvidence(s)
	return nil
}
func (m *MemStore) addScenarioColdSnapshotLocked(s *api.ScenarioActionSnapshot, apps []string) {
	if len(apps) == 0 {
		return
	}
	s.ColdStart = coldEvidence(s, m.scenarioActionPolicies[apps[0]].Member.ColdStartJSON)
	if s.ColdStart == nil {
		return
	}
	for _, app := range apps {
		if rehearsalcold.Decode(m.scenarioActionPolicies[app].Member.ColdStartJSON) != s.ColdStart.Spec {
			s.ColdStart.Status = "incomplete"
		}
		if p, ok := m.scenarioColdPreparations[app]; ok {
			s.ColdStart.Preparations = append(s.ColdStart.Preparations, p)
		}
	}
	finishColdEvidence(s)
}
