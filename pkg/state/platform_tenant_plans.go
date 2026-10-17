package state

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlatformTenantPlan is a reusable bundle of tenant card terms and an
// optional request budget (ADR-1307). Assigning it to a tenant creates the
// tenant's rate card from the next minute and sets its budget; later plan
// edits apply to tenants when the plan is assigned again.
type PlatformTenantPlan struct {
	ID                     string
	AccountID              string
	Name                   string
	Currency               string
	PriceMillicentsPerUnit int64
	IncludedUnitsPerMonth  int64
	Tiers                  []APIConsumerRateCardTier
	RouteWeights           map[string]int64
	// Unit is what the plan's card prices (ADR-1310) and TierMode how its
	// tiers price a month (ADR-1309); empty means request and graduated.
	Unit     string
	TierMode string
	// MaxRequestsPerMinute and MaxRequestsPerDay are set together, or
	// both nil when the plan leaves the tenant's budget alone (ADR-240).
	MaxRequestsPerMinute *int64
	MaxRequestsPerDay    *int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// PlatformTenantPlanAssignment records one assignment and the card it
// created.
type PlatformTenantPlanAssignment struct {
	ID            string
	AccountID     string
	TenantID      string
	PlanID        string
	RateCardID    string
	EffectiveFrom time.Time
	CreatedAt     time.Time
}

// PlatformTenantPlanStore persists tenant plans and assignments.
type PlatformTenantPlanStore interface {
	// CreatePlatformTenantPlan is ErrConflict for a taken name and
	// ErrPlatformTenantPlanLimit past api.PlatformTenantPlansMax plans.
	CreatePlatformTenantPlan(ctx context.Context, p PlatformTenantPlan) (PlatformTenantPlan, error)
	ListPlatformTenantPlans(ctx context.Context, accountID string) ([]PlatformTenantPlan, error)
	GetPlatformTenantPlan(ctx context.Context, accountID, planID string) (PlatformTenantPlan, error)
	// UpdatePlatformTenantPlan replaces a plan's name and terms.
	UpdatePlatformTenantPlan(ctx context.Context, p PlatformTenantPlan) (PlatformTenantPlan, error)
	RecordPlatformTenantPlanAssignment(ctx context.Context, a PlatformTenantPlanAssignment) (PlatformTenantPlanAssignment, error)
	// ListPlatformTenantPlanAssignments returns a tenant's assignments,
	// newest first.
	ListPlatformTenantPlanAssignments(ctx context.Context, accountID, tenantID string) ([]PlatformTenantPlanAssignment, error)
}

var (
	_ PlatformTenantPlanStore = (*MemStore)(nil)
	_ PlatformTenantPlanStore = (*PgStore)(nil)
)

// ErrPlatformTenantPlanLimit reports an account at api.PlatformTenantPlansMax.
var ErrPlatformTenantPlanLimit = errors.New("state: platform tenant plan limit reached")

var platformTenantPlanNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidatePlatformTenantPlan checks a plan's name and terms like a tenant
// rate card.
func ValidatePlatformTenantPlan(p PlatformTenantPlan) error {
	if _, err := uuid.Parse(p.AccountID); err != nil {
		return fmt.Errorf("%w: account_id must be a UUID", ErrInvalidArgument)
	}
	if !platformTenantPlanNameRE.MatchString(p.Name) {
		return fmt.Errorf("%w: plan name must be 1-63 lowercase letters, digits or hyphens", ErrInvalidArgument)
	}
	if !isUpperASCIICurrency(p.Currency) || p.PriceMillicentsPerUnit < 0 || p.IncludedUnitsPerMonth < 0 {
		return fmt.Errorf("%w: invalid plan price", ErrInvalidArgument)
	}
	if len(p.Tiers) > 0 && p.IncludedUnitsPerMonth != 0 {
		return fmt.Errorf("%w: tiers replace included_units_per_month", ErrInvalidArgument)
	}
	if err := ValidateAPIConsumerRateCardTiers(p.Tiers); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if err := ValidateAPIConsumerRouteWeights(p.RouteWeights); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if p.Unit != "" && (!ValidAPIConsumerRateCardUnit(p.Unit) || (p.Unit != PlatformTenantRateCardUnitRequest && len(p.RouteWeights) > 0)) {
		return fmt.Errorf("%w: unit must be request, compute_second or response_megabyte, and route weights apply to request plans only", ErrInvalidArgument)
	}
	if _, err := normalizeRateCardTierMode(p.TierMode, p.Tiers); err != nil {
		return err
	}
	if (p.MaxRequestsPerMinute == nil) != (p.MaxRequestsPerDay == nil) ||
		(p.MaxRequestsPerMinute != nil && (*p.MaxRequestsPerMinute < 0 || *p.MaxRequestsPerDay < 0)) {
		return fmt.Errorf("%w: a plan budget needs both non-negative ceilings", ErrInvalidArgument)
	}
	return nil
}

func normalizePlatformTenantPlan(p PlatformTenantPlan) PlatformTenantPlan {
	if p.Unit == "" {
		p.Unit = PlatformTenantRateCardUnitRequest
	}
	p.TierMode, _ = normalizeRateCardTierMode(p.TierMode, p.Tiers)
	p.Tiers = cloneRateCardTiers(p.Tiers)
	if len(p.Tiers) > 0 {
		p.PriceMillicentsPerUnit = p.Tiers[len(p.Tiers)-1].PriceMillicentsPerUnit
	}
	p.RouteWeights = maps.Clone(p.RouteWeights)
	if len(p.RouteWeights) == 0 {
		p.RouteWeights = nil
	}
	return p
}

// --- MemStore ---

func (m *MemStore) CreatePlatformTenantPlan(_ context.Context, p PlatformTenantPlan) (PlatformTenantPlan, error) {
	if err := ValidatePlatformTenantPlan(p); err != nil {
		return PlatformTenantPlan{}, err
	}
	p = normalizePlatformTenantPlan(p)
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, existing := range m.platformTenantPlans {
		if existing.AccountID == p.AccountID {
			count++
			if existing.Name == p.Name {
				return PlatformTenantPlan{}, ErrConflict
			}
		}
	}
	if count >= api.PlatformTenantPlansMax {
		return PlatformTenantPlan{}, ErrPlatformTenantPlanLimit
	}
	if m.platformTenantPlans == nil {
		m.platformTenantPlans = map[string]PlatformTenantPlan{}
	}
	now := time.Now().UTC()
	p.ID, p.CreatedAt, p.UpdatedAt = uuid.NewString(), now, now
	m.platformTenantPlans[p.ID] = p
	return p, nil
}

func (m *MemStore) ListPlatformTenantPlans(_ context.Context, accountID string) ([]PlatformTenantPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PlatformTenantPlan{}
	for _, p := range m.platformTenantPlans {
		if p.AccountID == accountID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) GetPlatformTenantPlan(_ context.Context, accountID, planID string) (PlatformTenantPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.platformTenantPlans[planID]
	if !ok || p.AccountID != accountID {
		return PlatformTenantPlan{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) UpdatePlatformTenantPlan(_ context.Context, p PlatformTenantPlan) (PlatformTenantPlan, error) {
	if err := ValidatePlatformTenantPlan(p); err != nil {
		return PlatformTenantPlan{}, err
	}
	p = normalizePlatformTenantPlan(p)
	m.mu.Lock()
	defer m.mu.Unlock()
	prior, ok := m.platformTenantPlans[p.ID]
	if !ok || prior.AccountID != p.AccountID {
		return PlatformTenantPlan{}, ErrNotFound
	}
	for _, existing := range m.platformTenantPlans {
		if existing.AccountID == p.AccountID && existing.ID != p.ID && existing.Name == p.Name {
			return PlatformTenantPlan{}, ErrConflict
		}
	}
	p.CreatedAt, p.UpdatedAt = prior.CreatedAt, time.Now().UTC()
	m.platformTenantPlans[p.ID] = p
	return p, nil
}

func (m *MemStore) RecordPlatformTenantPlanAssignment(_ context.Context, a PlatformTenantPlanAssignment) (PlatformTenantPlanAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.platformTenantPlans[a.PlanID]; !ok || p.AccountID != a.AccountID {
		return PlatformTenantPlanAssignment{}, ErrNotFound
	}
	a.ID, a.CreatedAt, a.EffectiveFrom = uuid.NewString(), time.Now().UTC(), a.EffectiveFrom.UTC()
	m.platformTenantPlanAssignments = append(m.platformTenantPlanAssignments, a)
	return a, nil
}

func (m *MemStore) ListPlatformTenantPlanAssignments(_ context.Context, accountID, tenantID string) ([]PlatformTenantPlanAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PlatformTenantPlanAssignment{}
	for _, a := range m.platformTenantPlanAssignments {
		if a.AccountID == accountID && a.TenantID == tenantID {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
