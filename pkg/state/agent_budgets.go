package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AgentKeyBudget is the budget an agent key chose for itself
// (ADR-20261111114059). Set false means the plan's default; Unlimited means
// no budget of its own (the organization's ceiling still applies).
type AgentKeyBudget struct {
	Set          bool
	Unlimited    bool
	MonthlyCents int64
}

// AgentBudgetStore keeps agent keys' budgets and the committed cost each
// added in a UTC month. apid is the only writer.
type AgentBudgetStore interface {
	// AgentKeyBudget returns the key's own budget.
	AgentKeyBudget(ctx context.Context, keyID string) (AgentKeyBudget, error)
	// SetAgentKeyBudget writes the key's budget; a budget with Set false
	// returns the key to its plan's default.
	SetAgentKeyBudget(ctx context.Context, keyID string, budget AgentKeyBudget, updatedBy string) error
	// AgentBudgetSpent is what the key added in the UTC month of at.
	AgentBudgetSpent(ctx context.Context, keyID string, at time.Time) (int64, error)
	// AddAgentBudgetSpend adds millicents (> 0) to the key's month of at.
	AddAgentBudgetSpend(ctx context.Context, keyID string, at time.Time, millicents int64) error
}

// budgetMonth is the first day of at's UTC month.
func budgetMonth(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func validAgentKeyBudget(b AgentKeyBudget) bool {
	return !b.Set || b.Unlimited || (b.MonthlyCents >= 0 && b.MonthlyCents <= api.MaxAgentBudgetCents)
}

func pgInt8Ptr(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func int8Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64
	return &out
}

// budgetCostParam stores a budget request's cost; other categories store
// none.
func budgetCostParam(request AgentApproval) pgtype.Int8 {
	return pgtype.Int8{Int64: request.BudgetCostMillicents, Valid: request.Category == api.AgentApprovalBudget}
}

func cloneInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func (s *PgStore) AgentKeyBudget(ctx context.Context, keyID string) (AgentKeyBudget, error) {
	cents, err := sqlc.New().GetAgentKeyBudget(ctx, s.pool, mustPgUUID(keyID))
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return AgentKeyBudget{}, nil
		}
		return AgentKeyBudget{}, err
	}
	return AgentKeyBudget{Set: true, Unlimited: !cents.Valid, MonthlyCents: cents.Int64}, nil
}

func (s *PgStore) SetAgentKeyBudget(ctx context.Context, keyID string, budget AgentKeyBudget, updatedBy string) error {
	if !validAgentKeyBudget(budget) {
		return ErrConflict
	}
	q := sqlc.New()
	if !budget.Set {
		return mapErr(q.DeleteAgentKeyBudget(ctx, s.pool, mustPgUUID(keyID)))
	}
	var by pgtype.UUID
	if updatedBy != "" {
		by = mustPgUUID(updatedBy)
	}
	return mapErr(q.UpsertAgentKeyBudget(ctx, s.pool, sqlc.UpsertAgentKeyBudgetParams{
		KeyID: mustPgUUID(keyID), MonthlyCents: pgtype.Int8{Int64: budget.MonthlyCents, Valid: !budget.Unlimited}, UpdatedBy: by,
	}))
}

func (s *PgStore) AgentBudgetSpent(ctx context.Context, keyID string, at time.Time) (int64, error) {
	spent, err := sqlc.New().GetAgentBudgetSpend(ctx, s.pool, sqlc.GetAgentBudgetSpendParams{
		KeyID: mustPgUUID(keyID), Month: pgtype.Date{Time: budgetMonth(at), Valid: true},
	})
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return spent, nil
}

func (s *PgStore) AddAgentBudgetSpend(ctx context.Context, keyID string, at time.Time, millicents int64) error {
	if millicents <= 0 {
		return nil
	}
	return mapErr(sqlc.New().AddAgentBudgetSpend(ctx, s.pool, sqlc.AddAgentBudgetSpendParams{
		KeyID: mustPgUUID(keyID), Month: pgtype.Date{Time: budgetMonth(at), Valid: true}, Millicents: millicents,
	}))
}

// --- MemStore ---------------------------------------------------------------

type agentBudgetSpendKey struct {
	keyID string
	month time.Time
}

func (m *MemStore) AgentKeyBudget(_ context.Context, keyID string) (AgentKeyBudget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agentKeyBudgets[keyID], nil
}

func (m *MemStore) SetAgentKeyBudget(_ context.Context, keyID string, budget AgentKeyBudget, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validAgentKeyBudget(budget) {
		return ErrConflict
	}
	if !m.hasKeyLocked(keyID) {
		return ErrNotFound
	}
	if !budget.Set {
		delete(m.agentKeyBudgets, keyID)
		return nil
	}
	if budget.Unlimited {
		budget.MonthlyCents = 0
	}
	if m.agentKeyBudgets == nil {
		m.agentKeyBudgets = map[string]AgentKeyBudget{}
	}
	m.agentKeyBudgets[keyID] = budget
	return nil
}

func (m *MemStore) AgentBudgetSpent(_ context.Context, keyID string, at time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agentBudgetSpend[agentBudgetSpendKey{keyID, budgetMonth(at)}], nil
}

func (m *MemStore) AddAgentBudgetSpend(_ context.Context, keyID string, at time.Time, millicents int64) error {
	if millicents <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasKeyLocked(keyID) {
		return ErrNotFound
	}
	if m.agentBudgetSpend == nil {
		m.agentBudgetSpend = map[agentBudgetSpendKey]int64{}
	}
	m.agentBudgetSpend[agentBudgetSpendKey{keyID, budgetMonth(at)}] += millicents
	return nil
}
