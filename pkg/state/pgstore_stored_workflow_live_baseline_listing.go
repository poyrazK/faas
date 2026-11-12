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

func (s *PgStore) ListStoredWorkflowLiveBaselines(ctx context.Context, appID, name string) (api.ListStoredWorkflowLiveBaselinesResponse, error) {
	out := api.ListStoredWorkflowLiveBaselinesResponse{Baselines: []api.StoredWorkflowLiveBaselineHealth{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.GetStoredWorkflowForLiveVerification(ctx, tx, sqlc.GetStoredWorkflowForLiveVerificationParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return out, err
	}
	rows, err := q.ListStoredWorkflowLiveBaselineHeads(ctx, tx, sqlc.ListStoredWorkflowLiveBaselineHeadsParams{AppID: mustPgUUID(appID), Name: name, Limit: api.StoredWorkflowLiveBaselineLimit})
	if err != nil {
		return out, err
	}
	out.ObservedAt = time.Now().UTC()
	for _, row := range rows {
		head, err := baselineFromSQL(row)
		if err != nil {
			return out, err
		}
		var view *api.StoredWorkflowLiveSuite
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
