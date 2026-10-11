// adr: 597 — caller selection remains live through atomic smoke admission.
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) createServiceBindingSmokeTask(ctx context.Context, params CreateAppTaskParams) (AppTask, error) {
	row, err := sqlc.New().CreateServiceBindingSmokeTask(ctx, s.pool, sqlc.CreateServiceBindingSmokeTaskParams{
		AccountID: mustPgUUID(params.AccountID), AppID: mustPgUUID(params.AppID), DeploymentID: mustPgUUID(params.DeploymentID),
		Command: params.Command, TimeoutSeconds: int32(params.TimeoutSeconds), MaxOutputBytes: int32(params.MaxOutputBytes),
		CreatedAt: pgtype.Timestamptz{Time: params.CreatedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: admit service binding smoke task: %w", mapErr(err))
	}
	return appTaskFromSQLC(row)
}
