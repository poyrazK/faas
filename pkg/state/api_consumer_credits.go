package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// APIConsumerCreditGrant is prepaid credit an app grants one consumer
// (ADR-1306). Finalized statements in its currency draw RemainingMillicents
// down, oldest-expiring grant first; a voided or expired grant draws nothing.
type APIConsumerCreditGrant struct {
	ID                  string
	AccountID           string
	AppID               string
	ConsumerID          string
	Currency            string
	AmountMillicents    int64
	RemainingMillicents int64
	Note                string
	ExpiresAt           *time.Time
	VoidedAt            *time.Time
	CreatedAt           time.Time
}

// Usable reports whether the grant can still pay for a statement at t.
func (g APIConsumerCreditGrant) Usable(t time.Time) bool {
	return g.VoidedAt == nil && g.RemainingMillicents > 0 && (g.ExpiresAt == nil || g.ExpiresAt.After(t))
}

// APIConsumerCreditPosition is a consumer's credit in one currency for a
// spend cap check: Available can pay future statements, and
// AppliedToUsageInMonth already paid the usage of statements whose period
// starts in the checked month. Credit pays a statement's fees first, so
// credit that paid fees is not counted against usage (ADR-20261020211500).
type APIConsumerCreditPosition struct {
	AvailableMillicents             int64
	AppliedToUsageInMonthMillicents int64
}

// creditAppliedToUsage is the part of a statement's credit left after its
// fees. A revision whose minimum top-up fell has negative fees, which free
// no credit for usage.
func creditAppliedToUsage(credit int64, fees []APIConsumerStatementFee) int64 {
	var total int64
	for _, f := range fees {
		total += f.AmountMillicents
	}
	return max(0, credit-max(0, total))
}

// creditAppliedToUsageSQL mirrors creditAppliedToUsage for a statement row
// st.
const creditAppliedToUsageSQL = `greatest(st.credit_applied_millicents
	- greatest(coalesce((select sum((f->>'amount_millicents')::bigint) from jsonb_array_elements(st.fees) f), 0), 0), 0)`

// SpendCapUsageChangeStore tells a spend cap check whether usage was
// recorded since its last pricing (ADR-20261020211500). Request, route and
// meter units are recorded together, so the usage minutes show any change.
// Only minutes starting at or after windowFrom are looked at.
type SpendCapUsageChangeStore interface {
	APIConsumerUsageChangedSince(ctx context.Context, accountID, appID, consumerID string, windowFrom, since time.Time) (bool, error)
	PlatformTenantUsageChangedSince(ctx context.Context, accountID, tenantID string, windowFrom, since time.Time) (bool, error)
}

// APIConsumerSpendCap is a consumer's monthly spend cap and its last check
// (ADR-1306). Capped holds for MonthStart's month only.
type APIConsumerSpendCap struct {
	ConsumerID           string
	AccountID            string
	AppID                string
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

// CappedIn reports whether the last check capped the consumer in the month
// starting at monthStart; a flag from an earlier month no longer applies.
func (c APIConsumerSpendCap) CappedIn(monthStart time.Time) bool {
	return c.Capped && c.MonthStart != nil && c.MonthStart.Equal(monthStart)
}

// APIConsumerCreditStore persists prepaid credit grants. Credits are applied
// inside FinalizeAPIConsumerUsageStatement.
type APIConsumerCreditStore interface {
	// CreateAPIConsumerCreditGrant records a grant; ErrConflict when the
	// consumer already holds api.ConsumerCreditOpenGrantsMax grants with
	// credit left.
	CreateAPIConsumerCreditGrant(ctx context.Context, g APIConsumerCreditGrant) (APIConsumerCreditGrant, error)
	// ListAPIConsumerCreditGrants returns the consumer's grants, newest first.
	ListAPIConsumerCreditGrants(ctx context.Context, accountID, appID, consumerID string) ([]APIConsumerCreditGrant, error)
	// VoidAPIConsumerCreditGrant zeroes what is left of a grant; voiding
	// again returns it unchanged.
	VoidAPIConsumerCreditGrant(ctx context.Context, accountID, appID, consumerID, grantID string) (APIConsumerCreditGrant, error)
	// APIConsumerCreditPosition sums the consumer's credit in currency for
	// the UTC month containing at.
	APIConsumerCreditPosition(ctx context.Context, accountID, appID, consumerID, currency string, at time.Time) (APIConsumerCreditPosition, error)
}

// APIConsumerSpendCapStore persists spend caps and their checks.
type APIConsumerSpendCapStore interface {
	// PutAPIConsumerSpendCap creates or replaces a cap and schedules an
	// immediate check; a changed cap keeps the last check until then.
	PutAPIConsumerSpendCap(ctx context.Context, c APIConsumerSpendCap) (APIConsumerSpendCap, error)
	GetAPIConsumerSpendCap(ctx context.Context, accountID, consumerID string) (APIConsumerSpendCap, error)
	DeleteAPIConsumerSpendCap(ctx context.Context, accountID, consumerID string) error
	// ClaimAPIConsumerSpendCapChecks leases up to limit caps whose next
	// check is due.
	ClaimAPIConsumerSpendCapChecks(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]APIConsumerSpendCap, error)
	// RecordAPIConsumerSpendCheck stores a check and returns whether the
	// consumer was capped in that month before it.
	RecordAPIConsumerSpendCheck(ctx context.Context, consumerID string, monthStart time.Time, amountDue int64, capped bool,
		checkedAt, next time.Time) (wasCapped bool, err error)
}

var (
	_ APIConsumerCreditStore   = (*MemStore)(nil)
	_ APIConsumerCreditStore   = (*PgStore)(nil)
	_ APIConsumerSpendCapStore = (*MemStore)(nil)
	_ APIConsumerSpendCapStore = (*PgStore)(nil)
)

func validateAPIConsumerCreditGrant(g APIConsumerCreditGrant) error {
	for name, value := range map[string]string{"account_id": g.AccountID, "app_id": g.AppID, "consumer_id": g.ConsumerID} {
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

func validateAPIConsumerSpendCap(c APIConsumerSpendCap) error {
	for name, value := range map[string]string{"account_id": c.AccountID, "app_id": c.AppID, "consumer_id": c.ConsumerID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: %s must be a UUID", ErrInvalidArgument, name)
		}
	}
	if !isUpperASCIICurrency(c.Currency) || c.MonthlyCapMillicents < 0 || c.MonthlyCapMillicents > api.ConsumerSpendCapMaxMillicents {
		return fmt.Errorf("%w: invalid spend cap", ErrInvalidArgument)
	}
	return nil
}

// creditMonth is the UTC calendar month containing t.
func creditMonth(t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// --- MemStore ---

type apiConsumerCreditApplication struct {
	GrantID          string
	StatementID      string
	AmountMillicents int64
}

func (m *MemStore) CreateAPIConsumerCreditGrant(_ context.Context, g APIConsumerCreditGrant) (APIConsumerCreditGrant, error) {
	if err := validateAPIConsumerCreditGrant(g); err != nil {
		return APIConsumerCreditGrant{}, err
	}
	now := time.Now().UTC()
	if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
		return APIConsumerCreditGrant{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidArgument)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	open := 0
	for _, existing := range m.apiConsumerCreditGrants {
		if existing.ConsumerID == g.ConsumerID && existing.RemainingMillicents > 0 {
			open++
		}
	}
	if open >= api.ConsumerCreditOpenGrantsMax {
		return APIConsumerCreditGrant{}, ErrConflict
	}
	if m.apiConsumerCreditGrants == nil {
		m.apiConsumerCreditGrants = map[string]APIConsumerCreditGrant{}
	}
	g.ID, g.RemainingMillicents, g.VoidedAt, g.CreatedAt = uuid.NewString(), g.AmountMillicents, nil, now
	m.apiConsumerCreditGrants[g.ID] = g
	return g, nil
}

func (m *MemStore) ListAPIConsumerCreditGrants(_ context.Context, accountID, appID, consumerID string) ([]APIConsumerCreditGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerCreditGrant{}
	for _, g := range m.apiConsumerCreditGrants {
		if g.AccountID == accountID && g.AppID == appID && g.ConsumerID == consumerID {
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

func (m *MemStore) VoidAPIConsumerCreditGrant(_ context.Context, accountID, appID, consumerID, grantID string) (APIConsumerCreditGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.apiConsumerCreditGrants[grantID]
	if !ok || g.AccountID != accountID || g.AppID != appID || g.ConsumerID != consumerID {
		return APIConsumerCreditGrant{}, ErrNotFound
	}
	if g.VoidedAt == nil {
		now := time.Now().UTC()
		g.VoidedAt, g.RemainingMillicents = &now, 0
		m.apiConsumerCreditGrants[grantID] = g
	}
	return g, nil
}

func (m *MemStore) APIConsumerUsageChangedSince(_ context.Context, accountID, appID, consumerID string, _, since time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.apiConsumerUsageRecordedAt[accountID+"\x00"+appID+"\x00"+consumerID].After(since), nil
}

func (m *MemStore) PlatformTenantUsageChangedSince(_ context.Context, accountID, tenantID string, _, since time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.platformTenantUsageRecordedAt[accountID+"\x00"+tenantID].After(since), nil
}

func (m *MemStore) APIConsumerCreditPosition(_ context.Context, accountID, appID, consumerID, currency string, at time.Time) (APIConsumerCreditPosition, error) {
	monthStart, monthEnd := creditMonth(at)
	m.mu.Lock()
	defer m.mu.Unlock()
	var pos APIConsumerCreditPosition
	for _, g := range m.apiConsumerCreditGrants {
		if g.AccountID == accountID && g.AppID == appID && g.ConsumerID == consumerID && g.Currency == currency && g.Usable(at) {
			pos.AvailableMillicents += g.RemainingMillicents
		}
	}
	for _, st := range m.apiConsumerUsageStatements {
		if st.AccountID == accountID && st.AppID == appID && st.ConsumerID == consumerID && st.Currency == currency &&
			!st.PeriodStart.Before(monthStart) && st.PeriodStart.Before(monthEnd) {
			pos.AppliedToUsageInMonthMillicents += creditAppliedToUsage(st.CreditAppliedMillicents, st.Fees)
		}
	}
	return pos, nil
}

// applyAPIConsumerCreditsLocked draws the consumer's usable grants down
// against a statement being finalized, oldest-expiring first, and returns
// the credit applied. The caller holds m.mu.
func (m *MemStore) applyAPIConsumerCreditsLocked(statement APIConsumerUsageStatement, now time.Time) int64 {
	if statement.AmountMillicents <= 0 || statement.Currency == "" {
		return 0
	}
	var grants []APIConsumerCreditGrant
	for _, g := range m.apiConsumerCreditGrants {
		if g.AccountID == statement.AccountID && g.AppID == statement.AppID && g.ConsumerID == statement.ConsumerID &&
			g.Currency == statement.Currency && g.Usable(now) {
			grants = append(grants, g)
		}
	}
	sortCreditGrantsForUse(grants)
	left := statement.AmountMillicents
	for _, g := range grants {
		if left == 0 {
			break
		}
		take := min(left, g.RemainingMillicents)
		g.RemainingMillicents -= take
		m.apiConsumerCreditGrants[g.ID] = g
		m.apiConsumerCreditApplications = append(m.apiConsumerCreditApplications,
			apiConsumerCreditApplication{GrantID: g.ID, StatementID: statement.ID, AmountMillicents: take})
		left -= take
	}
	return statement.AmountMillicents - left
}

// sortCreditGrantsForUse orders grants by expiry (never-expiring last), then
// age: the order statements draw them down.
func sortCreditGrantsForUse(grants []APIConsumerCreditGrant) {
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

func (m *MemStore) PutAPIConsumerSpendCap(_ context.Context, c APIConsumerSpendCap) (APIConsumerSpendCap, error) {
	if err := validateAPIConsumerSpendCap(c); err != nil {
		return APIConsumerSpendCap{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiConsumerSpendCaps == nil {
		m.apiConsumerSpendCaps = map[string]APIConsumerSpendCap{}
	}
	now := time.Now().UTC()
	if prior, ok := m.apiConsumerSpendCaps[c.ConsumerID]; ok {
		prior.Currency, prior.MonthlyCapMillicents, prior.NextCheckAt, prior.UpdatedAt = c.Currency, c.MonthlyCapMillicents, now, now
		m.apiConsumerSpendCaps[c.ConsumerID] = prior
		return prior, nil
	}
	c.Capped, c.MonthStart, c.AmountDueMillicents, c.CheckedAt = false, nil, nil, nil
	c.NextCheckAt, c.CreatedAt, c.UpdatedAt = now, now, now
	m.apiConsumerSpendCaps[c.ConsumerID] = c
	return c, nil
}

func (m *MemStore) GetAPIConsumerSpendCap(_ context.Context, accountID, consumerID string) (APIConsumerSpendCap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumerSpendCaps[consumerID]
	if !ok || c.AccountID != accountID {
		return APIConsumerSpendCap{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) DeleteAPIConsumerSpendCap(_ context.Context, accountID, consumerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumerSpendCaps[consumerID]
	if !ok || c.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.apiConsumerSpendCaps, consumerID)
	return nil
}

func (m *MemStore) ClaimAPIConsumerSpendCapChecks(_ context.Context, now time.Time, lease time.Duration, limit int) ([]APIConsumerSpendCap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []APIConsumerSpendCap
	for _, c := range m.apiConsumerSpendCaps {
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
		m.apiConsumerSpendCaps[c.ConsumerID] = c
	}
	return due, nil
}

func (m *MemStore) RecordAPIConsumerSpendCheck(_ context.Context, consumerID string, monthStart time.Time, amountDue int64, capped bool,
	checkedAt, next time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumerSpendCaps[consumerID]
	if !ok {
		return false, ErrNotFound
	}
	was := c.CappedIn(monthStart.UTC())
	month, due, at := monthStart.UTC(), amountDue, checkedAt.UTC()
	c.Capped, c.MonthStart, c.AmountDueMillicents, c.CheckedAt, c.NextCheckAt = capped, &month, &due, &at, next.UTC()
	m.apiConsumerSpendCaps[consumerID] = c
	return was, nil
}
