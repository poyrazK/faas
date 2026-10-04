package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialBudgetStore = (*PgStore)(nil)

func (s *PgStore) CreateFinancialBudget(ctx context.Context, account, id, actor string, spec financial.BudgetSpec) (FinancialBudget, error) {
	return s.mutateFinancialBudget(ctx, account, id, 0, actor, "created", spec)
}

func (s *PgStore) UpdateFinancialBudget(ctx context.Context, account, id string, expected int64, actor string, spec financial.BudgetSpec) (FinancialBudget, error) {
	if expected < 1 {
		return FinancialBudget{}, ErrInvalidArgument
	}
	return s.mutateFinancialBudget(ctx, account, id, expected, actor, "updated", spec)
}

func (s *PgStore) DeleteFinancialBudget(ctx context.Context, account, id string, expected int64, actor string) (FinancialBudget, error) {
	if expected < 1 {
		return FinancialBudget{}, ErrInvalidArgument
	}
	return s.mutateFinancialBudget(ctx, account, id, expected, actor, "deleted", financial.BudgetSpec{})
}

func (s *PgStore) mutateFinancialBudget(ctx context.Context, account, id string, expected int64, actor, mutation string, spec financial.BudgetSpec) (FinancialBudget, error) {
	if !validFinancialBudgetMutation(account, id, actor, expected) || (mutation != "deleted" && ValidateFinancialBudgetSpec(spec) != nil) {
		return FinancialBudget{}, ErrInvalidArgument
	}
	accountID, _ := parsePgUUID(account)
	policyID, _ := parsePgUUID(id)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FinancialBudget{}, fmt.Errorf("financial budget begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.FinancialBudgetAccountLock(ctx, tx, accountID); err != nil {
		return FinancialBudget{}, financialBudgetStoreError(err)
	}
	if mutation != "created" {
		old, err := q.FinancialBudgetGet(ctx, tx, sqlc.FinancialBudgetGetParams{AccountID: accountID, ID: policyID})
		if err != nil {
			return FinancialBudget{}, financialBudgetStoreError(err)
		}
		if old.Revision != expected || old.DeletedAt.Valid {
			return FinancialBudget{}, ErrConflict
		}
	}
	if mutation != "deleted" {
		if err := financialBudgetScopeOwned(ctx, tx, q, accountID, spec); err != nil {
			return FinancialBudget{}, err
		}
	}
	data, err := json.Marshal(cloneFinancialBudgetSpec(spec))
	if err != nil {
		return FinancialBudget{}, err
	}
	var row sqlc.FinancialBudgetPolicy
	switch mutation {
	case "created":
		// An existing operation identity is a replay, even when its original
		// creation filled the account's last slot. Let the handler recover it.
		if _, err := q.FinancialBudgetGet(ctx, tx, sqlc.FinancialBudgetGetParams{AccountID: accountID, ID: policyID}); err == nil {
			return FinancialBudget{}, ErrConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return FinancialBudget{}, financialBudgetStoreError(err)
		}
		count, countErr := q.FinancialBudgetCount(ctx, tx, accountID)
		if countErr != nil {
			return FinancialBudget{}, countErr
		}
		if count >= api.FinancialBudgetsPerAccount {
			return FinancialBudget{}, ErrFinancialBudgetLimit
		}
		row, err = q.FinancialBudgetInsert(ctx, tx, sqlc.FinancialBudgetInsertParams{ID: policyID, AccountID: accountID, Spec: data})
	case "updated":
		row, err = q.FinancialBudgetUpdate(ctx, tx, sqlc.FinancialBudgetUpdateParams{ID: policyID, AccountID: accountID, ExpectedRevision: expected, Spec: data})
	case "deleted":
		row, err = q.FinancialBudgetDelete(ctx, tx, sqlc.FinancialBudgetDeleteParams{ID: policyID, AccountID: accountID, ExpectedRevision: expected})
	default:
		return FinancialBudget{}, ErrInvalidArgument
	}
	if err != nil {
		return FinancialBudget{}, financialBudgetStoreError(err)
	}
	if err := q.FinancialBudgetRevisionInsert(ctx, tx, sqlc.FinancialBudgetRevisionInsertParams{AccountID: accountID, PolicyID: policyID, Revision: row.Revision, Actor: actor, Mutation: mutation, Spec: row.Spec}); err != nil {
		return FinancialBudget{}, fmt.Errorf("financial budget audit: %w", err)
	}
	out, err := financialBudgetFromRow(row)
	if err != nil {
		return FinancialBudget{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FinancialBudget{}, fmt.Errorf("financial budget commit: %w", err)
	}
	return out, nil
}

func financialBudgetScopeOwned(ctx context.Context, tx sqlc.DBTX, q *sqlc.Queries, account pgtype.UUID, spec financial.BudgetSpec) error {
	var scopeID pgtype.UUID
	if spec.Scope.Kind != "account" {
		scopeID, _ = parsePgUUID(spec.Scope.ID)
	}
	owned, err := q.FinancialBudgetScopeOwned(ctx, tx, sqlc.FinancialBudgetScopeOwnedParams{AccountID: account, ScopeKind: spec.Scope.Kind, ScopeID: scopeID})
	if err != nil {
		return fmt.Errorf("financial budget scope: %w", err)
	}
	if !owned {
		return ErrNotFound
	}
	if spec.Scope.Kind == "app" {
		eligible, err := q.FinancialBudgetAppActionEligible(ctx, tx, sqlc.FinancialBudgetAppActionEligibleParams{AccountID: account, AppID: scopeID, Action: spec.Action})
		if err != nil {
			return fmt.Errorf("financial budget action: %w", err)
		}
		if !eligible {
			return ErrInvalidArgument
		}
	}
	return nil
}

func (s *PgStore) ValidateFinancialBudgetScope(ctx context.Context, account string, spec financial.BudgetSpec) error {
	if uuid.Validate(account) != nil || ValidateFinancialBudgetSpec(spec) != nil {
		return ErrInvalidArgument
	}
	accountID, _ := parsePgUUID(account)
	return financialBudgetScopeOwned(ctx, s.pool, sqlc.New(), accountID, spec)
}

func financialBudgetFromRow(row sqlc.FinancialBudgetPolicy) (FinancialBudget, error) {
	out := FinancialBudget{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), Revision: row.Revision, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
	if err := json.Unmarshal(row.Spec, &out.Spec); err != nil {
		return out, fmt.Errorf("financial budget decode: %w", err)
	}
	if err := ValidateFinancialBudgetSpec(out.Spec); err != nil {
		return out, fmt.Errorf("financial budget contract: %w", err)
	}
	if row.DeletedAt.Valid {
		at := row.DeletedAt.Time.UTC()
		out.DeletedAt = &at
	}
	return out, nil
}

func financialBudgetStoreError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("financial budget state: %w", err)
}

func (s *PgStore) GetFinancialBudget(ctx context.Context, account, id string) (FinancialBudget, error) {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil {
		return FinancialBudget{}, ErrInvalidArgument
	}
	accountID, _ := parsePgUUID(account)
	policyID, _ := parsePgUUID(id)
	row, err := sqlc.New().FinancialBudgetGet(ctx, s.pool, sqlc.FinancialBudgetGetParams{AccountID: accountID, ID: policyID})
	if err != nil {
		return FinancialBudget{}, financialBudgetStoreError(err)
	}
	return financialBudgetFromRow(row)
}

func (s *PgStore) ListFinancialBudgets(ctx context.Context, account string) ([]FinancialBudget, error) {
	if uuid.Validate(account) != nil {
		return nil, ErrInvalidArgument
	}
	accountID, _ := parsePgUUID(account)
	rows, err := sqlc.New().FinancialBudgetList(ctx, s.pool, sqlc.FinancialBudgetListParams{AccountID: accountID, PageSize: api.FinancialBudgetsPerAccount + 1})
	if err != nil {
		return nil, fmt.Errorf("financial budget list: %w", err)
	}
	if len(rows) > api.FinancialBudgetsPerAccount {
		return nil, ErrFinancialBudgetLimit
	}
	out := make([]FinancialBudget, 0, len(rows))
	for _, row := range rows {
		p, err := financialBudgetFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *PgStore) ListFinancialBudgetRevisions(ctx context.Context, account, id string, after int64, limit int) ([]FinancialBudgetRevision, error) {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil || after < 0 || after > api.FinancialBudgetRevisionMax || limit < 1 || limit > api.FinancialBudgetHistoryMax {
		return nil, ErrInvalidArgument
	}
	accountID, _ := parsePgUUID(account)
	policyID, _ := parsePgUUID(id)
	rows, err := sqlc.New().FinancialBudgetRevisionList(ctx, s.pool, sqlc.FinancialBudgetRevisionListParams{AccountID: accountID, PolicyID: policyID, AfterRevision: after, PageSize: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("financial budget history: %w", err)
	}
	out := make([]FinancialBudgetRevision, 0, len(rows))
	for _, row := range rows {
		p := FinancialBudgetRevision{AccountID: account, PolicyID: id, Revision: row.Revision, Actor: row.Actor, Mutation: row.Mutation, RecordedAt: row.RecordedAt.Time.UTC()}
		if err := json.Unmarshal(row.Spec, &p.Spec); err != nil {
			return nil, fmt.Errorf("financial budget history decode: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}
