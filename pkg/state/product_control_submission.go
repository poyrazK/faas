package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateProductLookup(req ProductFeatureRequest) error {
	if req.Feature == "" || len(req.Feature) > api.OperationNameMaxBytes || req.IdempotencyKey == "" || len(req.IdempotencyKey) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(req.IdempotencyKey, "\r\n\x00") {
		return ErrInvalidArgument
	}
	for _, id := range []string{req.AccountID, req.AppID, req.TenantID} {
		if _, err := operationUUID(id); err != nil {
			return err
		}
	}
	return nil
}

// A lookup is read-only and never falls back to admitting work. The product
// key remains stable across definition, deployment and commercial changes.
func (m *MemStore) LookupProductSubmission(_ context.Context, req ProductFeatureRequest) (api.OperationSubmissionLookupResponse, error) {
	unresolved := api.OperationSubmissionLookupResponse{State: "unresolved"}
	if err := validateProductLookup(req); err != nil {
		return unresolved, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[req.AppID]
	if !ok || app.AccountID != req.AccountID || app.Status == AppDeleted {
		return unresolved, ErrNotFound
	}
	data := m.operationMemoryLocked()
	r, ok := data.receipts[productOperationKey(req.AccountID, req.AppID, req.TenantID, req.Feature, req.IdempotencyKey)]
	if !ok {
		return unresolved, nil
	}
	var op *Operation
	if original, ok := data.operations[r.OperationID]; ok && original.AccountID == req.AccountID && original.AppID == req.AppID && original.PlatformTenantID == req.TenantID {
		op = &original
	}
	return operationSubmissionObservation(op, r.ExpiresAt, time.Now()), nil
}

func (s *PgStore) LookupProductSubmission(ctx context.Context, req ProductFeatureRequest) (api.OperationSubmissionLookupResponse, error) {
	unresolved := api.OperationSubmissionLookupResponse{State: "unresolved"}
	if err := validateProductLookup(req); err != nil {
		return unresolved, err
	}
	a, _ := operationUUID(req.AccountID)
	app, _ := operationUUID(req.AppID)
	tenant, _ := operationUUID(req.TenantID)
	key := productOperationKey(req.AccountID, req.AppID, req.TenantID, req.Feature, req.IdempotencyKey)
	row, err := sqlc.New().LookupCustomerOperationSubmission(ctx, s.pool, sqlc.LookupCustomerOperationSubmissionParams{AccountID: a, AppID: app, TenantID: tenant, ScopeDigest: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return unresolved, nil
	}
	if err != nil {
		return unresolved, err
	}
	var op *Operation
	if len(row.Record) > 0 && string(row.Record) != "null" {
		if err := json.Unmarshal(row.Record, &op); err != nil {
			return unresolved, err
		}
	}
	return operationSubmissionObservation(op, row.ExpiresAt.Time, time.Now()), nil
}
