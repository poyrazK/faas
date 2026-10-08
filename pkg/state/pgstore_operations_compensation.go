package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func validateCompensationSourceTx(ctx context.Context, tx pgx.Tx, op Operation, report api.OperationMilestoneRequest) error {
	compensation, err := api.ParseOperationBusinessCompensation(report.Payload)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if compensation == nil {
		return nil
	}
	sourceID, _ := operationUUID(compensation.SourceEffect.OperationID)
	milestoneID, _ := operationUUID(compensation.SourceEffect.MilestoneID)
	account, _ := operationUUID(op.AccountID)
	app, _ := operationUUID(op.AppID)
	tenant, _ := operationUUID(op.PlatformTenantID)
	payload, err := sqlc.New().GetCustomerOperationCompensationSource(ctx, tx, sqlc.GetCustomerOperationCompensationSourceParams{SourceOperationID: sourceID, SourceMilestoneID: milestoneID, AccountID: account, AppID: app, TenantID: tenant, Scope: op.Scope, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: compensation source must be a retained confirmed effect in the same customer, app, and environment", ErrInvalidArgument)
	}
	if err != nil {
		return err
	}
	return validateCompensationEffectPayload(payload)
}
