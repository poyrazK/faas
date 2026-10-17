// adr: 1304
package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListAutomationLiveBaselines(ctx context.Context, appID, name string) (api.ListAutomationLiveBaselinesResponse, error) {
	out := api.ListAutomationLiveBaselinesResponse{Baselines: []api.AutomationLiveBaselineHealth{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.GetAutomationForLiveVerification(ctx, tx, sqlc.GetAutomationForLiveVerificationParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return out, err
	}
	rows, err := q.ListAutomationLiveBaselineHeads(ctx, tx, sqlc.ListAutomationLiveBaselineHeadsParams{AppID: mustPgUUID(appID), Name: name, Limit: api.AutomationLiveBaselineLimit})
	if err != nil {
		return out, err
	}
	out.ObservedAt = time.Now().UTC()
	for _, row := range rows {
		head, err := baselineFromSQL(row)
		if err != nil {
			return out, err
		}
		var view *api.AutomationLiveSuite
		record, err := getLiveSuiteSQL(ctx, tx, appID, name, head.Suite.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return out, err
		}
		if err == nil {
			current, err := liveSuiteViewSQL(ctx, tx, record)
			if err != nil {
				return out, err
			}
			view = &current
		}
		out.Baselines = append(out.Baselines, baselineHealth(head, view, out.ObservedAt))
	}
	return out, nil
}
