// adr: 644
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type OperationSubmissionLookupStore interface {
	LookupPlatformTenantOperationSubmission(context.Context, string, string, api.OperationSubmissionLookupRequest) (api.OperationSubmissionLookupResponse, error)
}

func validateOperationExpectedIdentity(expected *api.OperationTenantIdentity, account, tenant string) error {
	if expected == nil {
		return nil
	}
	a, e := uuid.Parse(expected.AccountID)
	t, f := uuid.Parse(expected.PlatformTenantID)
	if e != nil || f != nil || a == uuid.Nil || t == uuid.Nil {
		return ErrInvalidArgument
	}
	if !sameOperationHistoryIdentity(account, expected.AccountID) || !sameOperationHistoryIdentity(tenant, expected.PlatformTenantID) {
		return ErrOperationIdentityConflict
	}
	return nil
}

func prepareOperationSubmissionLookup(account, tenant string, req api.OperationSubmissionLookupRequest) (api.OperationSubmissionLookupRequest, error) {
	opts, _, err := prepareOperationHistory(account, tenant, api.OperationListOptions{AppID: req.AppID, Scope: req.Scope, Name: req.Name})
	if err != nil {
		return req, err
	}
	if req.Name == "" || len(req.IdempotencyKey) < 1 || len(req.IdempotencyKey) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(req.IdempotencyKey, "\r\n\x00") {
		return req, ErrInvalidArgument
	}
	if err = validateOperationExpectedIdentity(req.ExpectedIdentity, account, tenant); err != nil {
		return req, err
	}
	req.AppID = opts.AppID
	return req, nil
}

func operationSubmissionObservation(op *Operation, expiry time.Time, now time.Time) api.OperationSubmissionLookupResponse {
	result := api.OperationSubmissionLookupResponse{State: "expired", IdempotencyExpiresAt: &expiry}
	if op == nil || !operationRetained(*op, now) || (expiry.Before(now) || expiry.Equal(now)) && op.State != api.OperationAccepted && op.State != api.OperationRunning {
		return result
	}
	result.State = "accepted"
	result.AcceptedAt = &op.CreatedAt
	result.Receipt = &api.OperationAcceptedResponse{ID: op.ID, StatusURL: "/v1/platform-tenant-self/customer-operations/" + op.ID, EventsURL: "/v1/platform-tenant-self/customer-operations/" + op.ID + "/events"}
	return result
}

func (m *MemStore) LookupPlatformTenantOperationSubmission(_ context.Context, account, tenant string, req api.OperationSubmissionLookupRequest) (api.OperationSubmissionLookupResponse, error) {
	unresolved := api.OperationSubmissionLookupResponse{State: "unresolved"}
	req, err := prepareOperationSubmissionLookup(account, tenant, req)
	if err != nil {
		return unresolved, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Use stored identity spellings, matching admission's digest in MemStore.
	var app App
	var owner PlatformTenant
	for _, candidate := range m.apps {
		if sameOperationHistoryIdentity(candidate.ID, req.AppID) && sameOperationHistoryIdentity(candidate.AccountID, account) {
			app = candidate
			break
		}
	}
	for _, candidate := range m.platformTenants {
		if sameOperationHistoryIdentity(candidate.ID, tenant) && sameOperationHistoryIdentity(candidate.AccountID, account) {
			owner = candidate
			break
		}
	}
	if app.ID == "" || owner.ID == "" || m.operationData == nil {
		return unresolved, nil
	}
	key := operations.IdentityScope(app.AccountID, app.ID, req.Scope, owner.ID, req.Name, req.IdempotencyKey)
	receipt, ok := m.operationData.receipts[key]
	if !ok {
		return unresolved, nil
	}
	op, ok := m.operationData.operations[receipt.OperationID]
	if !ok {
		return operationSubmissionObservation(nil, receipt.ExpiresAt, time.Now().UTC()), nil
	}
	return operationSubmissionObservation(&op, receipt.ExpiresAt, time.Now().UTC()), nil
}

func (s *PgStore) LookupPlatformTenantOperationSubmission(ctx context.Context, account, tenant string, req api.OperationSubmissionLookupRequest) (api.OperationSubmissionLookupResponse, error) {
	unresolved := api.OperationSubmissionLookupResponse{State: "unresolved"}
	req, err := prepareOperationSubmissionLookup(account, tenant, req)
	if err != nil {
		return unresolved, err
	}
	a, _ := operationUUID(account)
	app, _ := operationUUID(req.AppID)
	owner, _ := operationUUID(tenant)
	key := operations.IdentityScope(uuid.MustParse(account).String(), req.AppID, req.Scope, uuid.MustParse(tenant).String(), req.Name, req.IdempotencyKey)
	row, err := sqlc.New().LookupCustomerOperationSubmission(ctx, s.pool, sqlc.LookupCustomerOperationSubmissionParams{AccountID: a, AppID: app, TenantID: owner, ScopeDigest: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return unresolved, nil
	}
	if err != nil {
		return unresolved, err
	}
	var op *Operation
	if len(row.Record) > 0 && string(row.Record) != "null" {
		if err = json.Unmarshal(row.Record, &op); err != nil {
			return unresolved, err
		}
	}
	return operationSubmissionObservation(op, row.ExpiresAt.Time.UTC(), time.Now().UTC()), nil
}
