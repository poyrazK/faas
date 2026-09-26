package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PlatformTenantRequestBudget is an admission policy, not billed usage. One
// request that passes the gate consumes one unit even if the guest later fails.
// Rejected requests do not consume a unit or enter the financial usage ledger.
type PlatformTenantRequestBudget struct {
	TenantID             string
	MaxRequestsPerMinute int64
	MaxRequestsPerDay    int64
	MinuteUsed           int64
	DayUsed              int64
	MinuteResetsAt       time.Time
	DayResetsAt          time.Time
	UpdatedAt            time.Time
}

type PlatformTenantBudgetDecision struct {
	Allowed           bool
	Scope             string // minute or day when denied
	Limit             int64
	Observed          int64
	RetryAfterSeconds int64
}

// PlatformTenantRequestBudgetStore is deliberately separate from Store so
// existing narrow test doubles need no new billing or admission methods.
type PlatformTenantRequestBudgetStore interface {
	GetPlatformTenantRequestBudget(context.Context, string, string) (PlatformTenantRequestBudget, error)
	SetPlatformTenantRequestBudget(context.Context, string, string, int64, int64) (PlatformTenantRequestBudget, error)
	AdmitPlatformTenantRequest(context.Context, string, string) (PlatformTenantBudgetDecision, error)
}

var (
	_ PlatformTenantRequestBudgetStore = (*PgStore)(nil)
	_ PlatformTenantRequestBudgetStore = (*MemStore)(nil)
)

type platformTenantBudgetRow struct {
	TenantID             string
	MaxRequestsPerMinute int64
	MaxRequestsPerDay    int64
	MinuteStart          time.Time
	MinuteUsed           int64
	DayStart             time.Time
	DayUsed              int64
	UpdatedAt            time.Time
}

func validPlatformTenantBudget(accountID, tenantID string, minute, day int64) bool {
	_, accountErr := uuid.Parse(accountID)
	_, tenantErr := uuid.Parse(tenantID)
	return accountErr == nil && tenantErr == nil && minute >= 0 && day >= 0 &&
		minute <= api.MaxPlatformTenantRequestsPerMinute && day <= api.MaxPlatformTenantRequestsPerDay
}

func platformTenantBudgetWindows(at time.Time) (time.Time, time.Time) {
	at = at.UTC()
	minute := at.Truncate(time.Minute)
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	return minute, day
}

func (r platformTenantBudgetRow) snapshot(at time.Time) PlatformTenantRequestBudget {
	minute, day := platformTenantBudgetWindows(at)
	out := PlatformTenantRequestBudget{
		TenantID: r.TenantID, MaxRequestsPerMinute: r.MaxRequestsPerMinute,
		MaxRequestsPerDay: r.MaxRequestsPerDay, MinuteResetsAt: minute.Add(time.Minute),
		DayResetsAt: day.Add(24 * time.Hour), UpdatedAt: r.UpdatedAt,
	}
	if r.MinuteStart.Equal(minute) {
		out.MinuteUsed = r.MinuteUsed
	}
	if r.DayStart.Equal(day) {
		out.DayUsed = r.DayUsed
	}
	return out
}

func (r platformTenantBudgetRow) decide(at time.Time) (platformTenantBudgetRow, PlatformTenantBudgetDecision) {
	minute, day := platformTenantBudgetWindows(at)
	if !r.MinuteStart.Equal(minute) {
		r.MinuteStart, r.MinuteUsed = minute, 0
	}
	if !r.DayStart.Equal(day) {
		r.DayStart, r.DayUsed = day, 0
	}
	if r.MaxRequestsPerDay > 0 && r.DayUsed >= r.MaxRequestsPerDay {
		return r, PlatformTenantBudgetDecision{Scope: "day", Limit: r.MaxRequestsPerDay,
			Observed: r.DayUsed, RetryAfterSeconds: secondsUntil(day.Add(24*time.Hour), at)}
	}
	if r.MaxRequestsPerMinute > 0 && r.MinuteUsed >= r.MaxRequestsPerMinute {
		return r, PlatformTenantBudgetDecision{Scope: "minute", Limit: r.MaxRequestsPerMinute,
			Observed: r.MinuteUsed, RetryAfterSeconds: secondsUntil(minute.Add(time.Minute), at)}
	}
	r.MinuteUsed++
	r.DayUsed++
	return r, PlatformTenantBudgetDecision{Allowed: true}
}

func secondsUntil(resetAt, at time.Time) int64 {
	delta := resetAt.Sub(at)
	seconds := int64((delta + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (m *MemStore) GetPlatformTenantRequestBudget(_ context.Context, accountID, tenantID string) (PlatformTenantRequestBudget, error) {
	if !validPlatformTenantBudget(accountID, tenantID, 0, 0) {
		return PlatformTenantRequestBudget{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantRequestBudget{}, ErrNotFound
	}
	row := m.platformTenantBudgets[tenantID]
	row.TenantID = tenantID
	return row.snapshot(time.Now().UTC()), nil
}

func (m *MemStore) SetPlatformTenantRequestBudget(_ context.Context, accountID, tenantID string, minute, day int64) (PlatformTenantRequestBudget, error) {
	if !validPlatformTenantBudget(accountID, tenantID, minute, day) {
		return PlatformTenantRequestBudget{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantRequestBudget{}, ErrNotFound
	}
	now := time.Now().UTC()
	row := m.platformTenantBudgets[tenantID]
	row.TenantID = tenantID
	if row.MaxRequestsPerMinute != minute || row.MaxRequestsPerDay != day || row.UpdatedAt.IsZero() {
		row.MaxRequestsPerMinute, row.MaxRequestsPerDay, row.UpdatedAt = minute, day, now
		m.platformTenantBudgets[tenantID] = row
	}
	return row.snapshot(now), nil
}

func (m *MemStore) AdmitPlatformTenantRequest(_ context.Context, accountID, tenantID string) (PlatformTenantBudgetDecision, error) {
	if !validPlatformTenantBudget(accountID, tenantID, 0, 0) {
		return PlatformTenantBudgetDecision{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantBudgetDecision{}, ErrNotFound
	}
	row, configured := m.platformTenantBudgets[tenantID]
	if !configured || (row.MaxRequestsPerMinute == 0 && row.MaxRequestsPerDay == 0) {
		return PlatformTenantBudgetDecision{Allowed: true}, nil
	}
	row, decision := row.decide(time.Now().UTC())
	if decision.Allowed {
		m.platformTenantBudgets[tenantID] = row
	}
	return decision, nil
}
