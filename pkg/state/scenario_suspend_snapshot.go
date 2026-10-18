package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func addScenarioSuspendSnapshot(ctx context.Context, tx pgx.Tx, s *api.ScenarioActionSnapshot, account string, policies []sqlc.ScenarioActionPoliciesForRunRow) error {
	if len(policies) == 0 || len(policies[0].SuspendResumeSpec) == 0 {
		return nil
	}
	e, err := suspendEvidenceTx(ctx, tx, account, s.RunID, policies)
	if err != nil {
		return err
	}
	if e.Status != "complete" {
		e.Status = "incomplete"
	}
	s.SuspendResume = &e
	return nil
}
func (m *MemStore) addScenarioSuspendSnapshotLocked(s *api.ScenarioActionSnapshot, apps []string) {
	if len(apps) == 0 {
		return
	}
	p := m.scenarioActionPolicies[apps[0]]
	if p.Member.SuspendResumeJSON == "" {
		return
	}
	e, err := m.suspendEvidenceLocked(p.Member.AccountID, s.RunID)
	if err != nil {
		return
	}
	if e.Status != "complete" {
		e.Status = "incomplete"
	}
	s.SuspendResume = &e
}
