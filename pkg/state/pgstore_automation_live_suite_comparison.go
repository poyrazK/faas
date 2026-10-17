package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) CompareAutomationLiveSuites(ctx context.Context, appID, name, candidateID, baselineID string) (api.AutomationLiveSuiteComparison, error) {
	candidateID, baselineID, err := liveSuiteComparisonIDs(candidateID, baselineID)
	if err != nil {
		return api.AutomationLiveSuiteComparison{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.AutomationLiveSuiteComparison{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	views := make([]api.AutomationLiveSuite, 0, 2)
	for _, id := range []string{baselineID, candidateID} {
		record, err := getLiveSuiteSQL(ctx, tx, appID, name, id)
		if err != nil {
			return api.AutomationLiveSuiteComparison{}, err
		}
		view, err := liveSuiteViewSQL(ctx, tx, record)
		if err != nil {
			return api.AutomationLiveSuiteComparison{}, err
		}
		views = append(views, view)
	}
	return compareLiveSuites(views[0], views[1]), nil
}
