package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AIBudgetAlert is one AI budget threshold crossing an ai_budget_alerts
// alert counted (ADR 20261027132936 amendment 2).
type AIBudgetAlert struct {
	ID, IntegrationName, Budget string
	ThresholdPercent            int
	Limit, Used                 int64
	EndUser, Tag                string
	CrossedAt                   time.Time
}

// AIBudgetAlertStore reads what the ai_budget_alerts alert metric counts;
// meterd's evaluator type-asserts it.
type AIBudgetAlertStore interface {
	// AIBudgetAlertsSince returns up to limit of the budget threshold
	// crossings at or after since of the integrations bound to the app,
	// newest first, and how many there are in all.
	AIBudgetAlertsSince(ctx context.Context, accountID, appID string, since time.Time, limit int) ([]AIBudgetAlert, int64, error)
}

func (s *PgStore) AIBudgetAlertsSince(ctx context.Context, accountID, appID string, since time.Time, limit int) ([]AIBudgetAlert, int64, error) {
	rows, err := sqlc.New().AIBudgetAlertsSince(ctx, s.pool, sqlc.AIBudgetAlertsSinceParams{
		AppID: appID, AccountID: accountID, Since: pgTimestamptzUTC(since), RowLimit: int32(limit),
	})
	if err != nil || len(rows) == 0 {
		return nil, 0, err
	}
	out := make([]AIBudgetAlert, 0, len(rows))
	for _, r := range rows {
		out = append(out, AIBudgetAlert{ID: r.ID, IntegrationName: r.IntegrationName, Budget: r.Budget,
			ThresholdPercent: int(r.ThresholdPercent), Limit: r.LimitValue, Used: r.UsedValue, EndUser: r.EndUser, Tag: r.Tag,
			CrossedAt: r.CrossedAt.Time.UTC()})
	}
	return out, rows[0].Total, nil
}

// AIBudgetAlertsSince: the in-memory store keeps no AI usage, so no
// threshold is ever crossed.
func (m *MemStore) AIBudgetAlertsSince(context.Context, string, string, time.Time, int) ([]AIBudgetAlert, int64, error) {
	return nil, 0, nil
}
