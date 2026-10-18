package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func productDefinitionTx(ctx context.Context, db sqlc.DBTX, account, appID string, feature api.ProductFeature) (OperationDefinition, error) {
	a, err := operationUUID(account)
	if err != nil {
		return OperationDefinition{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return OperationDefinition{}, err
	}
	q := sqlc.New()
	id := feature.DefinitionID
	if feature.Operation != nil {
		id, err = q.ProductControlLiveDefinition(ctx, db, sqlc.ProductControlLiveDefinitionParams{AccountID: a, AppID: b, Scope: feature.Operation.Scope, Name: feature.Operation.Name})
		if errors.Is(err, pgx.ErrNoRows) {
			return OperationDefinition{}, ErrProductBindingUnavailable
		}
		if err != nil {
			return OperationDefinition{}, err
		}
	}
	c, err := operationUUID(id)
	if err != nil {
		return OperationDefinition{}, err
	}
	row, err := q.GetCustomerOperationDefinition(ctx, db, sqlc.GetCustomerOperationDefinitionParams{AccountID: a, ID: c})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.AppID != appID {
		return OperationDefinition{}, ErrProductBindingUnavailable
	}
	if err != nil {
		return OperationDefinition{}, err
	}
	return operationPGDefinition(row)
}

func (s *PgStore) ResolveProductFeature(ctx context.Context, req ProductFeatureRequest) (OperationDefinition, error) {
	fingerprint, err := productRequestFingerprint(req)
	if err != nil {
		return OperationDefinition{}, err
	}
	a, err := operationUUID(req.AccountID)
	if err != nil {
		return OperationDefinition{}, err
	}
	b, err := operationUUID(req.AppID)
	if err != nil {
		return OperationDefinition{}, err
	}
	if _, err := sqlc.New().ProductControlOwnedApp(ctx, s.pool, sqlc.ProductControlOwnedAppParams{AccountID: a, ID: b}); err != nil {
		return OperationDefinition{}, mapErr(err)
	}
	if req.IdempotencyKey != "" {
		id, err := s.productRetryDefinition(ctx, req, fingerprint)
		if err != nil {
			return OperationDefinition{}, err
		}
		if id != "" {
			return s.OperationDefinitionByID(ctx, req.AccountID, id)
		}
	}
	customer, err := productCustomerTx(ctx, s.pool, req.AccountID, req.AppID, req.TenantID)
	if errors.Is(err, ErrNotFound) {
		return OperationDefinition{}, ErrProductAccess
	}
	if err != nil {
		return OperationDefinition{}, err
	}
	catalog, err := productCatalogTx(ctx, s.pool, req.AccountID, req.AppID, customer.Revision)
	if err != nil {
		return OperationDefinition{}, err
	}
	feature, _, err := productFeatureGrant(catalog, customer, req.Feature)
	if err != nil {
		return OperationDefinition{}, err
	}
	return productDefinitionTx(ctx, s.pool, req.AccountID, req.AppID, feature)
}

func (s *PgStore) productRetryDefinition(ctx context.Context, req ProductFeatureRequest, fingerprint string) (string, error) {
	a, _ := operationUUID(req.AccountID)
	q := sqlc.New()
	key := productOperationKey(req.AccountID, req.AppID, req.TenantID, req.Feature, req.IdempotencyKey)
	row, err := q.GetCustomerOperationIdempotency(ctx, s.pool, sqlc.GetCustomerOperationIdempotencyParams{AccountID: a, ScopeDigest: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !row.ExpiresAt.Time.After(time.Now()) && !row.Active {
		return "", nil
	}
	id, _ := operationUUID(row.OperationID)
	raw, err := q.GetCustomerOperation(ctx, s.pool, sqlc.GetCustomerOperationParams{AccountID: a, ID: id, TenantID: req.TenantID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var op *Operation
	if err == nil {
		if err := json.Unmarshal(raw, &op); err != nil {
			return "", err
		}
		if op.AppID != req.AppID {
			return "", ErrNotFound
		}
	}
	return productRetryDefinition(op, row.ExpiresAt.Time, row.Fingerprint, fingerprint, time.Now())
}

func (s *PgStore) GetProductReceipt(ctx context.Context, account, appID, operation string) (ProductReceiptSnapshot, error) {
	a, err := operationUUID(account)
	if err != nil {
		return ProductReceiptSnapshot{}, err
	}
	b, err := operationUUID(appID)
	if err != nil {
		return ProductReceiptSnapshot{}, err
	}
	c, err := operationUUID(operation)
	if err != nil {
		return ProductReceiptSnapshot{}, err
	}
	row, err := sqlc.New().ProductControlGetReceipt(ctx, s.pool, sqlc.ProductControlGetReceiptParams{AccountID: a, AppID: b, OperationID: c})
	if err != nil {
		return ProductReceiptSnapshot{}, mapErr(err)
	}
	r := api.ProductOperationReceipt{Revision: row.Revision, Feature: row.Feature, Plan: row.Plan, Currency: row.Currency, Period: row.Period, UnitPriceMillicents: row.UnitPriceMillicents, AdmittedAt: row.AdmittedAt.Time}
	if len(row.AdmissionEvidence) > 0 {
		if err := json.Unmarshal(row.AdmissionEvidence, &r.Admission); err != nil {
			return ProductReceiptSnapshot{}, err
		}
	}
	out := ProductReceiptSnapshot{ProductReceiptEvidence: api.ProductReceiptEvidence{OperationID: operation, TenantID: pgUUIDString(row.TenantID), Receipt: r}}
	if row.SucceededAt.Valid {
		out.SucceededAt = &row.SucceededAt.Time
	}
	out.ResolvedState = api.OperationState(row.ResolvedState)
	if row.ResolvedAt.Valid {
		out.ResolvedAt = &row.ResolvedAt.Time
	}
	if row.WaivedAt.Valid {
		out.WaivedAt = &row.WaivedAt.Time
	}
	out.WaiverReason = row.WaiverReason.String
	if len(row.OperationRecord) > 0 {
		op, err := operationPGRecord(row.OperationRecord)
		if err != nil {
			return ProductReceiptSnapshot{}, err
		}
		if operationRetained(op, time.Now()) {
			out.Operation = &op
		}
	}
	return out, nil
}
