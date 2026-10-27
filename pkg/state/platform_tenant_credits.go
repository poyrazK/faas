package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlatformTenantCreditGrant is prepaid credit an account grants one
// platform tenant (ADR-20261019100331). Finalized tenant statements in its
// currency draw RemainingMillicents down, oldest-expiring grant first.
type PlatformTenantCreditGrant struct {
	ID                  string
	AccountID           string
	TenantID            string
	Currency            string
	AmountMillicents    int64
	RemainingMillicents int64
	Note                string
	ExpiresAt           *time.Time
	VoidedAt            *time.Time
	CreatedAt           time.Time
}

// Usable reports whether the grant can still pay for a statement at t.
func (g PlatformTenantCreditGrant) Usable(t time.Time) bool {
	return g.VoidedAt == nil && g.RemainingMillicents > 0 && (g.ExpiresAt == nil || g.ExpiresAt.After(t))
}

// PlatformTenantSpendCap is a tenant's monthly spend cap and its last check
// (ADR-20261019100331). Capped holds for MonthStart's month only.
type PlatformTenantSpendCap struct {
	TenantID             string
	AccountID            string
	Currency             string
	MonthlyCapMillicents int64
	Capped               bool
	MonthStart           *time.Time
	AmountDueMillicents  *int64
	CheckedAt            *time.Time
	NextCheckAt          time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// CappedIn reports whether the last check capped the tenant in the month
// starting at monthStart.
func (c PlatformTenantSpendCap) CappedIn(monthStart time.Time) bool {
	return c.Capped && c.MonthStart != nil && c.MonthStart.Equal(monthStart)
}

// PlatformTenantCreditStore persists tenant credit grants. Credits are
// applied inside FinalizePlatformTenantStatement.
type PlatformTenantCreditStore interface {
	// CreatePlatformTenantCreditGrant records a grant; ErrConflict when the
	// tenant already holds api.ConsumerCreditOpenGrantsMax grants with
	// credit left.
	CreatePlatformTenantCreditGrant(ctx context.Context, g PlatformTenantCreditGrant) (PlatformTenantCreditGrant, error)
	// ListPlatformTenantCreditGrants returns the tenant's grants, newest
	// first.
	ListPlatformTenantCreditGrants(ctx context.Context, accountID, tenantID string) ([]PlatformTenantCreditGrant, error)
	// VoidPlatformTenantCreditGrant zeroes what is left of a grant.
	VoidPlatformTenantCreditGrant(ctx context.Context, accountID, tenantID, grantID string) (PlatformTenantCreditGrant, error)
	// PlatformTenantCreditPosition sums the tenant's credit in currency for
	// the UTC month containing at.
	PlatformTenantCreditPosition(ctx context.Context, accountID, tenantID, currency string, at time.Time) (APIConsumerCreditPosition, error)
}

// PlatformTenantSpendCapStore persists tenant spend caps and their checks.
type PlatformTenantSpendCapStore interface {
	PutPlatformTenantSpendCap(ctx context.Context, c PlatformTenantSpendCap) (PlatformTenantSpendCap, error)
	GetPlatformTenantSpendCap(ctx context.Context, accountID, tenantID string) (PlatformTenantSpendCap, error)
	DeletePlatformTenantSpendCap(ctx context.Context, accountID, tenantID string) error
	ClaimPlatformTenantSpendCapChecks(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]PlatformTenantSpendCap, error)
	// RecordPlatformTenantSpendCheck stores a check and returns whether the
	// tenant was capped in that month before it.
	RecordPlatformTenantSpendCheck(ctx context.Context, tenantID string, monthStart time.Time, amountDue int64, capped bool,
		checkedAt, next time.Time) (wasCapped bool, err error)
}

var (
	_ PlatformTenantCreditStore   = (*MemStore)(nil)
	_ PlatformTenantCreditStore   = (*PgStore)(nil)
	_ PlatformTenantSpendCapStore = (*MemStore)(nil)
	_ PlatformTenantSpendCapStore = (*PgStore)(nil)
)

func validatePlatformTenantCreditGrant(g PlatformTenantCreditGrant) error {
	for name, value := range map[string]string{"account_id": g.AccountID, "platform_tenant_id": g.TenantID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: %s must be a UUID", ErrInvalidArgument, name)
		}
	}
	if !isUpperASCIICurrency(g.Currency) || g.AmountMillicents <= 0 || g.AmountMillicents > api.ConsumerCreditGrantMaxMillicents ||
		len(g.Note) > api.ConsumerCreditNoteMaxBytes {
		return fmt.Errorf("%w: invalid credit grant", ErrInvalidArgument)
	}
	return nil
}

func validatePlatformTenantSpendCap(c PlatformTenantSpendCap) error {
	for name, value := range map[string]string{"account_id": c.AccountID, "platform_tenant_id": c.TenantID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: %s must be a UUID", ErrInvalidArgument, name)
		}
	}
	if !isUpperASCIICurrency(c.Currency) || c.MonthlyCapMillicents < 0 || c.MonthlyCapMillicents > api.ConsumerSpendCapMaxMillicents {
		return fmt.Errorf("%w: invalid spend cap", ErrInvalidArgument)
	}
	return nil
}

// sortTenantCreditGrantsForUse orders grants by expiry (never-expiring
// last), then age: the order statements draw them down.
func sortTenantCreditGrantsForUse(grants []PlatformTenantCreditGrant) {
	sort.Slice(grants, func(i, j int) bool {
		a, b := grants[i], grants[j]
		switch {
		case a.ExpiresAt != nil && b.ExpiresAt == nil:
			return true
		case a.ExpiresAt == nil && b.ExpiresAt != nil:
			return false
		case a.ExpiresAt != nil && !a.ExpiresAt.Equal(*b.ExpiresAt):
			return a.ExpiresAt.Before(*b.ExpiresAt)
		case !a.CreatedAt.Equal(b.CreatedAt):
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
}

// --- MemStore ---

type platformTenantCreditApplication struct {
	GrantID          string
	StatementID      string
	AmountMillicents int64
}

func (m *MemStore) CreatePlatformTenantCreditGrant(_ context.Context, g PlatformTenantCreditGrant) (PlatformTenantCreditGrant, error) {
	if err := validatePlatformTenantCreditGrant(g); err != nil {
		return PlatformTenantCreditGrant{}, err
	}
	now := time.Now().UTC()
	if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
		return PlatformTenantCreditGrant{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidArgument)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[g.TenantID]; !ok || tenant.AccountID != g.AccountID {
		return PlatformTenantCreditGrant{}, ErrNotFound
	}
	open := 0
	for _, existing := range m.platformTenantCreditGrants {
		if existing.TenantID == g.TenantID && existing.RemainingMillicents > 0 {
			open++
		}
	}
	if open >= api.ConsumerCreditOpenGrantsMax {
		return PlatformTenantCreditGrant{}, ErrConflict
	}
	if m.platformTenantCreditGrants == nil {
		m.platformTenantCreditGrants = map[string]PlatformTenantCreditGrant{}
	}
	g.ID, g.RemainingMillicents, g.VoidedAt, g.CreatedAt = uuid.NewString(), g.AmountMillicents, nil, now
	m.platformTenantCreditGrants[g.ID] = g
	return g, nil
}

func (m *MemStore) ListPlatformTenantCreditGrants(_ context.Context, accountID, tenantID string) ([]PlatformTenantCreditGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PlatformTenantCreditGrant{}
	for _, g := range m.platformTenantCreditGrants {
		if g.AccountID == accountID && g.TenantID == tenantID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (m *MemStore) VoidPlatformTenantCreditGrant(_ context.Context, accountID, tenantID, grantID string) (PlatformTenantCreditGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.platformTenantCreditGrants[grantID]
	if !ok || g.AccountID != accountID || g.TenantID != tenantID {
		return PlatformTenantCreditGrant{}, ErrNotFound
	}
	if g.VoidedAt == nil {
		now := time.Now().UTC()
		g.VoidedAt, g.RemainingMillicents = &now, 0
		m.platformTenantCreditGrants[grantID] = g
	}
	return g, nil
}

func (m *MemStore) PlatformTenantCreditPosition(_ context.Context, accountID, tenantID, currency string, at time.Time) (APIConsumerCreditPosition, error) {
	monthStart, monthEnd := creditMonth(at)
	m.mu.Lock()
	defer m.mu.Unlock()
	var pos APIConsumerCreditPosition
	for _, g := range m.platformTenantCreditGrants {
		if g.AccountID == accountID && g.TenantID == tenantID && g.Currency == currency && g.Usable(at) {
			pos.AvailableMillicents += g.RemainingMillicents
		}
	}
	for _, st := range m.platformTenantStatements {
		if st.AccountID == accountID && st.TenantID == tenantID && st.Currency == currency &&
			!st.PeriodStart.Before(monthStart) && st.PeriodStart.Before(monthEnd) {
			pos.AppliedToUsageInMonthMillicents += creditAppliedToUsage(st.CreditAppliedMillicents, st.Fees)
		}
	}
	return pos, nil
}

// applyPlatformTenantCreditsLocked draws the tenant's usable grants down
// against a statement being finalized and returns the credit applied. The
// caller holds m.mu.
func (m *MemStore) applyPlatformTenantCreditsLocked(statement PlatformTenantStatement, now time.Time) int64 {
	if statement.AmountMillicents <= 0 || statement.Currency == "" {
		return 0
	}
	var grants []PlatformTenantCreditGrant
	for _, g := range m.platformTenantCreditGrants {
		if g.AccountID == statement.AccountID && g.TenantID == statement.TenantID && g.Currency == statement.Currency && g.Usable(now) {
			grants = append(grants, g)
		}
	}
	sortTenantCreditGrantsForUse(grants)
	left := statement.AmountMillicents
	for _, g := range grants {
		if left == 0 {
			break
		}
		take := min(left, g.RemainingMillicents)
		g.RemainingMillicents -= take
		m.platformTenantCreditGrants[g.ID] = g
		m.platformTenantCreditApplications = append(m.platformTenantCreditApplications,
			platformTenantCreditApplication{GrantID: g.ID, StatementID: statement.ID, AmountMillicents: take})
		left -= take
	}
	return statement.AmountMillicents - left
}

func (m *MemStore) PutPlatformTenantSpendCap(_ context.Context, c PlatformTenantSpendCap) (PlatformTenantSpendCap, error) {
	if err := validatePlatformTenantSpendCap(c); err != nil {
		return PlatformTenantSpendCap{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[c.TenantID]; !ok || tenant.AccountID != c.AccountID {
		return PlatformTenantSpendCap{}, ErrNotFound
	}
	if m.platformTenantSpendCaps == nil {
		m.platformTenantSpendCaps = map[string]PlatformTenantSpendCap{}
	}
	now := time.Now().UTC()
	if prior, ok := m.platformTenantSpendCaps[c.TenantID]; ok {
		prior.Currency, prior.MonthlyCapMillicents, prior.NextCheckAt, prior.UpdatedAt = c.Currency, c.MonthlyCapMillicents, now, now
		m.platformTenantSpendCaps[c.TenantID] = prior
		return prior, nil
	}
	c.Capped, c.MonthStart, c.AmountDueMillicents, c.CheckedAt = false, nil, nil, nil
	c.NextCheckAt, c.CreatedAt, c.UpdatedAt = now, now, now
	m.platformTenantSpendCaps[c.TenantID] = c
	return c, nil
}

func (m *MemStore) GetPlatformTenantSpendCap(_ context.Context, accountID, tenantID string) (PlatformTenantSpendCap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.platformTenantSpendCaps[tenantID]
	if !ok || c.AccountID != accountID {
		return PlatformTenantSpendCap{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) DeletePlatformTenantSpendCap(_ context.Context, accountID, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.platformTenantSpendCaps[tenantID]
	if !ok || c.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.platformTenantSpendCaps, tenantID)
	return nil
}

func (m *MemStore) ClaimPlatformTenantSpendCapChecks(_ context.Context, now time.Time, lease time.Duration, limit int) ([]PlatformTenantSpendCap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []PlatformTenantSpendCap
	for _, c := range m.platformTenantSpendCaps {
		if !c.NextCheckAt.After(now) {
			due = append(due, c)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].NextCheckAt.Before(due[j].NextCheckAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	for _, c := range due {
		c.NextCheckAt = now.Add(lease)
		m.platformTenantSpendCaps[c.TenantID] = c
	}
	return due, nil
}

func (m *MemStore) RecordPlatformTenantSpendCheck(_ context.Context, tenantID string, monthStart time.Time, amountDue int64, capped bool,
	checkedAt, next time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.platformTenantSpendCaps[tenantID]
	if !ok {
		return false, ErrNotFound
	}
	was := c.CappedIn(monthStart.UTC())
	month, due, at := monthStart.UTC(), amountDue, checkedAt.UTC()
	c.Capped, c.MonthStart, c.AmountDueMillicents, c.CheckedAt, c.NextCheckAt = capped, &month, &due, &at, next.UTC()
	m.platformTenantSpendCaps[tenantID] = c
	return was, nil
}
