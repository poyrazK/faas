package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BindingVerificationStore = (*PgStore)(nil)

func (s *PgStore) createBindingVerificationTask(ctx context.Context, params CreateAppTaskParams) (AppTask, error) {
	raw, err := json.Marshal(params.BindingVerification)
	if err != nil {
		return AppTask{}, fmt.Errorf("state: encode binding verification pin: %w", err)
	}
	id, err := sqlc.New().CreateBindingVerificationTask(ctx, s.pool, sqlc.CreateBindingVerificationTaskParams{
		AccountID: mustPgUUID(params.AccountID), AppID: mustPgUUID(params.AppID), DeploymentID: mustPgUUID(params.DeploymentID),
		Command: params.Command, TimeoutSeconds: int32(params.TimeoutSeconds), MaxOutputBytes: int32(params.MaxOutputBytes),
		CreatedAt: pgtype.Timestamptz{Time: params.CreatedAt, Valid: true}, BindingVerification: raw,
		RequireLiveDeployment: params.RequireLiveDeployment,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: admit binding verification task: %w", mapErr(err))
	}
	return s.AppTaskByID(ctx, params.AccountID, params.AppID, uuidString(id))
}

func (s *PgStore) ListBindingVerificationTasks(ctx context.Context, accountID, appID string, kinds []string, selections ...BindingVerificationSelection) ([]BindingVerificationTask, error) {
	selection, err := resolveBindingVerificationSelection(selections)
	if err != nil {
		return nil, err
	}
	selectedID := pgtype.UUID{}
	if selection.DeploymentID != "" {
		selectedID = mustPgUUID(selection.DeploymentID)
	}
	rows, err := sqlc.New().ListBindingVerificationTasks(ctx, s.pool, sqlc.ListBindingVerificationTasksParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Kinds: kinds,
		SelectedDeploymentID: selectedID, ExactDeployment: selection.DeploymentID != "" && !selection.AllowFallback,
	})
	if err != nil {
		return nil, fmt.Errorf("state: list binding verification tasks: %w", err)
	}
	items := make([]BindingVerificationTask, 0, len(rows))
	for _, row := range rows {
		var pin BindingVerificationPin
		if err := json.Unmarshal(row.BindingVerification, &pin); err != nil {
			return nil, fmt.Errorf("state: decode binding verification pin: %w", err)
		}
		var exit *int
		if row.ExitCode.Valid {
			value := int(row.ExitCode.Int32)
			exit = &value
		}
		items = append(items, BindingVerificationTask{
			Pin: pin, DeploymentID: uuidString(row.DeploymentID), Scope: row.DeploymentScope,
			Status: row.Status, Stdout: row.Stdout, OutputTruncated: row.Truncated.Bool,
			ExitCode: exit, CreatedAt: row.CreatedAt.Time.UTC(), FinishedAt: optionalHealthTime(row.FinishedAt),
		})
	}
	return items, nil
}
