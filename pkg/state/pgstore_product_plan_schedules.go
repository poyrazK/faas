package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func applyDueProductPlanPG(ctx context.Context, tx pgx.Tx, scope ProductBillingScope, now time.Time) error {
	params := sqlc.ProductOwnerDueScheduleParams{
		AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID),
		TenantID: mustPgUUID(scope.TenantID), ScheduledFor: pgTime(now),
	}
	q := sqlc.New()
	due, err := q.ProductOwnerDueSchedule(ctx, tx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt api.ProductCustomerChangeReceipt
	if err := json.Unmarshal(due.Receipt, &receipt); err != nil {
		return err
	}
	current, err := productCustomerForChangePG(ctx, tx, scope.AccountID, scope.AppID, scope.TenantID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	status := productPlanNeedsReview
	if err == nil && receipt.Before != nil && sameProductAssignment(current, *receipt.Before) {
		status, err = productPlanActivationStatusPG(ctx, tx, scope, current, receipt)
		if err != nil {
			return err
		}
	}
	updated, err := q.ProductOwnerResolveSchedule(ctx, tx, sqlc.ProductOwnerResolveScheduleParams{
		ID: due.ID, AccountID: params.AccountID, AppID: params.AppID, TenantID: params.TenantID,
		ScheduleStatus: pgtype.Text{String: status, Valid: true},
	})
	if err != nil || updated == 0 || status != productPlanApplied {
		return err
	}
	target := receipt.After
	var org pgtype.UUID
	if current.AuthOrganizationID != "" {
		org = mustPgUUID(current.AuthOrganizationID)
	}
	return q.ProductControlSetCustomer(ctx, tx, sqlc.ProductControlSetCustomerParams{
		AccountID: params.AccountID, AppID: params.AppID, TenantID: params.TenantID,
		Revision: target.Revision, Plan: target.Plan, Suspended: current.Suspended,
		AuthOrganizationID: org, EffectiveAt: pgTime(receipt.EffectiveAt),
	})
}

func productPlanActivationStatusPG(ctx context.Context, tx pgx.Tx, scope ProductBillingScope, current api.ProductCustomer, receipt api.ProductCustomerChangeReceipt) (string, error) {
	target, err := productCatalogTx(ctx, tx, scope.AccountID, scope.AppID, receipt.After.Revision)
	if errors.Is(err, ErrNotFound) {
		return productPlanNeedsReview, nil
	}
	if err != nil {
		return "", err
	}
	if _, exists := target.Spec.Plans[receipt.After.Plan]; !exists {
		return productPlanNeedsReview, nil
	}
	catalog, err := productCatalogTx(ctx, tx, scope.AccountID, scope.AppID, current.Revision)
	if err != nil {
		return "", err
	}
	if pendingProductTrialFallback(current, catalog.Spec.Plans[current.Plan], receipt.EffectiveAt) {
		changed, err := sqlc.New().ProductOwnerPlanChangedAfter(ctx, tx, sqlc.ProductOwnerPlanChangedAfterParams{
			AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID),
			TenantID: mustPgUUID(scope.TenantID), EffectiveAt: pgTime(*current.TrialEndsAt),
		})
		if err != nil {
			return "", err
		}
		if !changed {
			return productPlanNeedsReview, nil
		}
	}
	return productPlanApplied, nil
}

func (s *PgStore) ensureProductPlanAt(ctx context.Context, scope ProductBillingScope, now time.Time) error {
	if err := validateProductBillingScope(scope); err != nil {
		return err
	}
	_, err := sqlc.New().ProductOwnerDueSchedule(ctx, s.pool, sqlc.ProductOwnerDueScheduleParams{
		AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID),
		TenantID: mustPgUUID(scope.TenantID), ScheduledFor: pgTime(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.productBillingLockedTx(ctx, scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := applyDueProductPlanPG(ctx, tx, scope, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ResolveProductPlanSchedules(ctx context.Context, now time.Time) error {
	rows, err := sqlc.New().ProductOwnerDueScheduleScopes(ctx, s.pool, sqlc.ProductOwnerDueScheduleScopesParams{
		ScheduledFor: pgTime(now), Limit: api.ProductControlTrialResolutionBatch,
	})
	if err != nil {
		return err
	}
	var result error
	for _, row := range rows {
		scope := ProductBillingScope{pgUUIDString(row.AccountID), pgUUIDString(row.AppID), pgUUIDString(row.TenantID), productcontrol.Period(now)}
		err := s.ensureProductPlanAt(ctx, scope, now)
		if errors.Is(err, ErrNotFound) {
			// A deleted app can no longer activate commercial terms.
			continue
		}
		result = errors.Join(result, err)
	}
	return result
}

func (s *PgStore) GetProductPlanSchedule(ctx context.Context, scope ProductBillingScope) (api.ProductPlanSchedule, error) {
	if err := s.ensureProductPlanAt(ctx, scope, time.Now()); err != nil {
		return api.ProductPlanSchedule{}, err
	}
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductPlanSchedule{}, err
	}
	row, err := sqlc.New().ProductOwnerLatestSchedule(ctx, s.pool, sqlc.ProductOwnerLatestScheduleParams{
		AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID),
	})
	if err != nil {
		return api.ProductPlanSchedule{}, mapErr(err)
	}
	result := api.ProductPlanSchedule{Status: row.ScheduleStatus.String}
	err = json.Unmarshal(row.Receipt, &result.ProductCustomerChangeReceipt)
	return result, err
}

func (s *PgStore) CancelProductPlanSchedule(ctx context.Context, scope ProductBillingScope, id string) (api.ProductPlanSchedule, error) {
	if !validProductUUID(id) {
		return api.ProductPlanSchedule{}, ErrInvalidArgument
	}
	tx, err := s.productBillingTx(ctx, scope)
	if err != nil {
		return api.ProductPlanSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	key := sqlc.ProductOwnerGetChangeParams{
		ID: mustPgUUID(id), AccountID: mustPgUUID(scope.AccountID),
		AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID),
	}
	row, err := q.ProductOwnerGetChange(ctx, tx, key)
	if err != nil {
		return api.ProductPlanSchedule{}, mapErr(err)
	}
	if !row.ScheduleStatus.Valid {
		return api.ProductPlanSchedule{}, ErrNotFound
	}
	result := api.ProductPlanSchedule{Status: row.ScheduleStatus.String}
	if err := json.Unmarshal(row.Receipt, &result.ProductCustomerChangeReceipt); err != nil {
		return api.ProductPlanSchedule{}, err
	}
	if result.Status == productPlanPending {
		updated, err := q.ProductOwnerResolveSchedule(ctx, tx, sqlc.ProductOwnerResolveScheduleParams{
			ID: key.ID, AccountID: key.AccountID, AppID: key.AppID, TenantID: key.TenantID,
			ScheduleStatus: pgtype.Text{String: productPlanCancelled, Valid: true},
		})
		if err != nil {
			return api.ProductPlanSchedule{}, err
		}
		if updated == 1 {
			result.Status = productPlanCancelled
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return api.ProductPlanSchedule{}, err
	}
	if result.Status != productPlanCancelled {
		return api.ProductPlanSchedule{}, ErrConflict
	}
	return result, nil
}
