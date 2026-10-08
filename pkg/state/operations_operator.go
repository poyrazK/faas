package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListAccountOperations(ctx context.Context, account string, opts api.OperationListOptions) (api.OperationListResponse, error) {
	opts, cursor, err := prepareOperationHistoryQuery(account, opts.TenantID, opts, true)
	if err != nil {
		return api.OperationListResponse{}, err
	}
	a, _ := operationUUID(account)
	app, _ := operationUUID(opts.AppID)
	params := sqlc.ListAccountCustomerOperationsParams{AccountID: a, AppID: app, TenantID: opts.TenantID, Scope: opts.Scope, OperationName: opts.Name, OperationState: string(opts.State), Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, PageLimit: int32(opts.Limit + 1)}
	if cursor.ID != "" {
		params.BeforeID, _ = operationUUID(cursor.ID)
		params.BeforeCreatedAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
	}
	var raw [][]byte
	if opts.SubjectType == "" {
		raw, err = sqlc.New().ListAccountCustomerOperations(ctx, s.pool, params)
	} else {
		raw, err = sqlc.New().ListAccountCustomerOperationsBySubject(ctx, s.pool, sqlc.ListAccountCustomerOperationsBySubjectParams{AccountID: params.AccountID, TenantID: params.TenantID, AppID: params.AppID, Scope: params.Scope, OperationName: params.OperationName, OperationState: params.OperationState, Now: params.Now, BeforeCreatedAt: params.BeforeCreatedAt, BeforeID: params.BeforeID, PageLimit: params.PageLimit, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID})
	}
	if err != nil {
		return api.OperationListResponse{}, fmt.Errorf("state: list account operations: %w", mapErr(err))
	}
	rows := make([]api.OperationSummary, 0, len(raw))
	for _, data := range raw {
		var row api.OperationSummary
		if err := json.Unmarshal(data, &row); err != nil {
			return api.OperationListResponse{}, fmt.Errorf("state: decode account operation summary: %w", err)
		}
		rows = append(rows, row)
	}
	return operationHistoryPage(rows, opts.Limit, cursor), nil
}

func operationExecutionPage(rows []api.OperationExecution, limit int) api.OperationExecutionsResponse {
	page := api.OperationExecutionsResponse{Executions: rows}
	if len(rows) > limit {
		page.Executions = rows[:limit]
		page.NextGeneration = rows[limit-1].Generation
	}
	return page
}

func operationExecutionBounds(after, limit int) (int32, int, error) {
	if limit == 0 {
		limit = api.OperationHistoryPageDefault
	}
	if after < 0 || after > math.MaxInt32 || limit < 1 || limit > api.OperationHistoryPageMax {
		return 0, 0, ErrInvalidArgument
	}
	return int32(after), limit, nil
}

func (m *MemStore) OperationExecutions(_ context.Context, account, id string, after, limit int) (api.OperationExecutionsResponse, error) {
	afterGeneration, limit, err := operationExecutionBounds(after, limit)
	if err != nil {
		return api.OperationExecutionsResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, ok := data.operations[id]
	if !ok || op.AccountID != account {
		return api.OperationExecutionsResponse{}, ErrNotFound
	}
	if !operationRetained(op, time.Now().UTC()) {
		return api.OperationExecutionsResponse{}, ErrOperationExpired
	}
	rows := []api.OperationExecution{}
	for invocation, operation := range data.executions {
		generation := data.generations[invocation]
		inv, exists := m.invocations[invocation]
		if operation != id || !exists || generation <= int(afterGeneration) {
			continue
		}
		var completed *time.Time
		if inv.CompletedAt != nil {
			v := *inv.CompletedAt
			completed = &v
		}
		rows = append(rows, api.OperationExecution{Generation: generation, InvocationID: inv.ID, State: string(inv.State), Attempts: inv.Attempts, CreatedAt: inv.CreatedAt, CompletedAt: completed})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Generation < rows[j].Generation })
	if len(rows) > limit+1 {
		rows = rows[:limit+1]
	}
	return operationExecutionPage(rows, limit), nil
}

func (s *PgStore) OperationExecutions(ctx context.Context, account, id string, after, limit int) (api.OperationExecutionsResponse, error) {
	afterGeneration, limit, err := operationExecutionBounds(after, limit)
	if err != nil {
		return api.OperationExecutionsResponse{}, err
	}
	if _, err := s.OperationByID(ctx, account, "", id); err != nil {
		return api.OperationExecutionsResponse{}, err
	}
	a, _ := operationUUID(account)
	i, _ := operationUUID(id)
	raw, err := sqlc.New().ListAccountCustomerOperationExecutions(ctx, s.pool, sqlc.ListAccountCustomerOperationExecutionsParams{AccountID: a, OperationID: i, AfterGeneration: afterGeneration, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.OperationExecutionsResponse{}, fmt.Errorf("state: list operation executions: %w", mapErr(err))
	}
	rows := make([]api.OperationExecution, 0, len(raw))
	for _, row := range raw {
		var completed *time.Time
		if row.CompletedAt.Valid {
			v := row.CompletedAt.Time
			completed = &v
		}
		rows = append(rows, api.OperationExecution{Generation: int(row.Generation), InvocationID: row.InvocationID, State: row.State, Attempts: int(row.Attempts), CreatedAt: row.CreatedAt.Time, CompletedAt: completed})
	}
	return operationExecutionPage(rows, limit), nil
}
