package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentRouteBudgetReceiptStore interface {
	GetEnvironmentRouteBudgetReceipt(context.Context, string, string, string, string) (api.EnvironmentRouteBudgetReceipt, error)
}

func validBudgetReceiptRequestID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func environmentBudgetReceiptSummary(requestID, hash string, saved, current api.EnvironmentRouteBudgets) (api.EnvironmentRouteBudgetReceipt, error) {
	if saved.AppID != current.AppID || saved.Scope != current.Scope || saved.ProjectID != current.ProjectID || saved.EnvironmentID != current.EnvironmentID {
		return api.EnvironmentRouteBudgetReceipt{}, ErrNotFound
	}
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != hash || saved.Revision <= 0 || saved.Revision > api.RouteRequirementsMaxRevision || saved.AppRevision < 0 || saved.AppRevision > api.RouteRequirementsMaxRevision || saved.BudgetSource != "app_policy" && saved.BudgetSource != "environment_policy" {
		return api.EnvironmentRouteBudgetReceipt{}, errors.New("invalid stored environment budget receipt")
	}
	return api.EnvironmentRouteBudgetReceipt{RequestID: requestID, RequestHash: hash, AppID: saved.AppID, Scope: saved.Scope, ProjectID: saved.ProjectID, EnvironmentID: saved.EnvironmentID, Revision: saved.Revision, AppRevision: saved.AppRevision, BudgetSource: saved.BudgetSource}, nil
}

func (m *MemStore) GetEnvironmentRouteBudgetReceipt(ctx context.Context, account, app, scope, requestID string) (api.EnvironmentRouteBudgetReceipt, error) {
	if !validBudgetReceiptRequestID(requestID) {
		return api.EnvironmentRouteBudgetReceipt{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.EnvironmentRouteBudgetReceipt{}, err
	}
	current, err := m.environmentBudgetsLocked(account, app, scope)
	if err != nil {
		return api.EnvironmentRouteBudgetReceipt{}, err
	}
	saved, ok := m.environmentBudgetReceipts[account+"\x00"+requestID]
	if !ok {
		return api.EnvironmentRouteBudgetReceipt{}, ErrNotFound
	}
	return environmentBudgetReceiptSummary(requestID, saved.Hash, saved.Response, current)
}

func (s *PgStore) GetEnvironmentRouteBudgetReceipt(ctx context.Context, account, app, scope, requestID string) (api.EnvironmentRouteBudgetReceipt, error) {
	if !validBudgetReceiptRequestID(requestID) {
		return api.EnvironmentRouteBudgetReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EnvironmentRouteBudgetReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := pgEnvironmentBudgetConfig(ctx, tx, account, app, scope, false)
	if err != nil {
		return api.EnvironmentRouteBudgetReceipt{}, err
	}
	saved, err := sqlc.New().ReadEnvironmentBudgetReceiptForApp(ctx, tx, sqlc.ReadEnvironmentBudgetReceiptForAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), RequestID: mustPgUUID(requestID)})
	if err != nil {
		return api.EnvironmentRouteBudgetReceipt{}, routePolicyReadError(err)
	}
	var response api.EnvironmentRouteBudgets
	if err := json.Unmarshal(saved.Response, &response); err != nil {
		return api.EnvironmentRouteBudgetReceipt{}, errors.New("invalid stored environment budget response")
	}
	out, err := environmentBudgetReceiptSummary(requestID, saved.RequestHash, response, current)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
