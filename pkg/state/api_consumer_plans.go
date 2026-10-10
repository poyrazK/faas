package state

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

// APIConsumerPlan is a named consumer plan (ADR-938): enforcement limits
// plus its own rate-card history (APIConsumerRateCard.PlanID). App-wide
// cards are the default plan. Zero limits mean unlimited.
type APIConsumerPlan struct {
	ID                   string
	AccountID            string
	AppID                string
	Name                 string
	MaxRequestsPerMinute int64
	MaxUnitsPerMonth     int64
	// AlertThresholdsPercent are ascending percentages of MaxUnitsPerMonth
	// at which a consumer.usage_threshold webhook fires (ADR-849).
	AlertThresholdsPercent []int32
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// APIConsumerPlanAssignment moves one consumer onto a plan from a UTC
// minute. Assignments are append-only; an empty PlanID returns the consumer
// to the default plan.
type APIConsumerPlanAssignment struct {
	ID            string
	AccountID     string
	AppID         string
	ConsumerID    string
	PlanID        string
	EffectiveFrom time.Time
	CreatedAt     time.Time
}

// APIConsumerPlanPolicy is what the gateway enforces for a consumer now:
// the current plan's limits and its current card's route weights, so the
// monthly cap counts the same weighted units as billing.
type APIConsumerPlanPolicy struct {
	PlanID               string
	AppID                string
	MaxRequestsPerMinute int64
	MaxUnitsPerMonth     int64
	RouteWeights         map[string]int64
	// AlertThresholdsPercent are the plan's usage alert thresholds (ADR-849).
	AlertThresholdsPercent []int32
}

// Limited reports whether admission must consult the counter.
func (p APIConsumerPlanPolicy) Limited() bool {
	return p.MaxRequestsPerMinute > 0 || p.MaxUnitsPerMonth > 0
}

// Units returns the weighted units one request on route consumes.
func (p APIConsumerPlanPolicy) Units(route string) int64 {
	if weight, ok := p.RouteWeights[route]; ok && weight > 0 {
		return weight
	}
	return 1
}

// APIConsumerPlanDecision is one admission outcome. Scope is "minute" or
// "month" when a limit denied the request.
type APIConsumerPlanDecision struct {
	Allowed           bool
	Scope             string
	Limit             int64
	Observed          int64
	RetryAfterSeconds int64
}

var (
	_ APIConsumerPlanStore = (*MemStore)(nil)
	_ APIConsumerPlanStore = (*PgStore)(nil)
)

// MaxAPIConsumerPlansPerApp bounds plans per app.
const MaxAPIConsumerPlansPerApp = 20

// APIConsumerPlanStore persists plans, assignments, and admission
// counters. Optional, like the other pricing stores.
type APIConsumerPlanStore interface {
	CreateAPIConsumerPlan(context.Context, APIConsumerPlan) (APIConsumerPlan, error)
	// UpdateAPIConsumerPlanLimits replaces a plan's limits. A nil
	// alertThresholds keeps the plan's alert thresholds; an empty one clears them.
	UpdateAPIConsumerPlanLimits(ctx context.Context, accountID, appID, planID string, perMinute, perMonth int64, alertThresholds []int32) (APIConsumerPlan, error)
	GetAPIConsumerPlan(ctx context.Context, accountID, appID, planID string) (APIConsumerPlan, error)
	ListAPIConsumerPlans(ctx context.Context, accountID, appID string) ([]APIConsumerPlan, error)
	AssignAPIConsumerPlan(context.Context, APIConsumerPlanAssignment) (APIConsumerPlanAssignment, error)
	// ListAPIConsumerPlanAssignments returns a consumer's assignments,
	// oldest first.
	ListAPIConsumerPlanAssignments(ctx context.Context, accountID, appID, consumerID string) ([]APIConsumerPlanAssignment, error)
	GetAPIConsumerPlanPolicy(ctx context.Context, accountID, appID, consumerID string, at time.Time) (APIConsumerPlanPolicy, error)
	AdmitAPIConsumerPlanRequest(ctx context.Context, accountID, consumerID string, policy APIConsumerPlanPolicy, units int64) (APIConsumerPlanDecision, error)
	// ListAPIConsumerUsageAlerts returns a consumer's recorded usage alerts,
	// newest first, at most limit.
	ListAPIConsumerUsageAlerts(ctx context.Context, accountID, appID, consumerID string, limit int) ([]APIConsumerUsageAlert, error)
}

var apiConsumerPlanNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidateAPIConsumerPlan checks a plan's name and limits.
func ValidateAPIConsumerPlan(plan APIConsumerPlan) error {
	if !apiConsumerPlanNameRE.MatchString(plan.Name) {
		return fmt.Errorf("consumer plan: name must be 1-63 lowercase letters, digits, or hyphens")
	}
	if plan.MaxRequestsPerMinute < 0 || plan.MaxUnitsPerMonth < 0 {
		return fmt.Errorf("consumer plan: limits must be non-negative")
	}
	return validateAPIConsumerPlanAlerts(plan.AlertThresholdsPercent, plan.MaxUnitsPerMonth)
}

func validateAPIConsumerPlanAssignment(a APIConsumerPlanAssignment) error {
	for name, value := range map[string]string{"account_id": a.AccountID, "app_id": a.AppID, "consumer_id": a.ConsumerID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("consumer plan assignment: %s must be a UUID: %w", name, err)
		}
	}
	if a.PlanID != "" {
		if _, err := uuid.Parse(a.PlanID); err != nil {
			return fmt.Errorf("consumer plan assignment: plan_id must be a UUID: %w", err)
		}
	}
	if a.EffectiveFrom.IsZero() || !a.EffectiveFrom.Equal(a.EffectiveFrom.UTC().Truncate(time.Minute)) {
		return fmt.Errorf("consumer plan assignment: effective_from must be a UTC minute")
	}
	return nil
}

// planAdmissionCounter is one consumer's admission window state.
type planAdmissionCounter struct {
	MinuteStart time.Time
	MinuteUsed  int64
	MonthStart  time.Time
	MonthUsed   int64
}

// decidePlanAdmission applies one request of units at now. It resets
// expired windows, denies a request that would exceed a limit without
// consuming anything, and otherwise consumes one request and the units.
//
// crossed lists the alert thresholds this request crossed (ADR-849): those
// the admitted units reached, or 100 when the monthly limit denied it.
func decidePlanAdmission(counter planAdmissionCounter, policy APIConsumerPlanPolicy, units int64, now time.Time) (next planAdmissionCounter, decision APIConsumerPlanDecision, crossed []int32) {
	now = now.UTC()
	minute := now.Truncate(time.Minute)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !counter.MinuteStart.Equal(minute) {
		counter.MinuteStart, counter.MinuteUsed = minute, 0
	}
	if !counter.MonthStart.Equal(month) {
		counter.MonthStart, counter.MonthUsed = month, 0
	}
	if policy.MaxRequestsPerMinute > 0 && counter.MinuteUsed+1 > policy.MaxRequestsPerMinute {
		return counter, APIConsumerPlanDecision{Scope: "minute", Limit: policy.MaxRequestsPerMinute, Observed: counter.MinuteUsed,
			RetryAfterSeconds: max(1, int64(minute.Add(time.Minute).Sub(now).Seconds()))}, nil
	}
	if policy.MaxUnitsPerMonth > 0 && counter.MonthUsed+units > policy.MaxUnitsPerMonth {
		if slices.Contains(policy.AlertThresholdsPercent, 100) {
			crossed = []int32{100}
		}
		return counter, APIConsumerPlanDecision{Scope: "month", Limit: policy.MaxUnitsPerMonth, Observed: counter.MonthUsed,
			RetryAfterSeconds: max(1, int64(month.AddDate(0, 1, 0).Sub(now).Seconds()))}, crossed
	}
	before := counter.MonthUsed
	counter.MinuteUsed++
	counter.MonthUsed += units
	return counter, APIConsumerPlanDecision{Allowed: true},
		crossedUsageAlertThresholds(policy.AlertThresholdsPercent, policy.MaxUnitsPerMonth, before, counter.MonthUsed)
}

// --- MemStore ---

func (m *MemStore) CreateAPIConsumerPlan(_ context.Context, plan APIConsumerPlan) (APIConsumerPlan, error) {
	if err := ValidateAPIConsumerPlan(plan); err != nil {
		return APIConsumerPlan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, existing := range m.apiConsumerPlans {
		if existing.AppID == plan.AppID {
			count++
			if existing.Name == plan.Name {
				return APIConsumerPlan{}, ErrConflict
			}
		}
	}
	if count >= MaxAPIConsumerPlansPerApp {
		return APIConsumerPlan{}, ErrConflict
	}
	now := time.Now().UTC()
	plan.AlertThresholdsPercent = normalizeAPIConsumerPlanAlerts(plan.AlertThresholdsPercent)
	plan.ID, plan.CreatedAt, plan.UpdatedAt = uuid.NewString(), now, now
	m.apiConsumerPlans[plan.ID] = plan
	return plan, nil
}

func (m *MemStore) UpdateAPIConsumerPlanLimits(_ context.Context, accountID, appID, planID string, perMinute, perMonth int64, alertThresholds []int32) (APIConsumerPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, ok := m.apiConsumerPlans[planID]
	if !ok || plan.AccountID != accountID || plan.AppID != appID {
		return APIConsumerPlan{}, ErrNotFound
	}
	plan.MaxRequestsPerMinute, plan.MaxUnitsPerMonth = perMinute, perMonth
	if alertThresholds != nil {
		plan.AlertThresholdsPercent = normalizeAPIConsumerPlanAlerts(alertThresholds)
	}
	if err := ValidateAPIConsumerPlan(plan); err != nil {
		return APIConsumerPlan{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	plan.UpdatedAt = time.Now().UTC()
	m.apiConsumerPlans[planID] = plan
	return plan, nil
}

func (m *MemStore) GetAPIConsumerPlan(_ context.Context, accountID, appID, planID string) (APIConsumerPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, ok := m.apiConsumerPlans[planID]
	if !ok || plan.AccountID != accountID || plan.AppID != appID {
		return APIConsumerPlan{}, ErrNotFound
	}
	return plan, nil
}

func (m *MemStore) ListAPIConsumerPlans(_ context.Context, accountID, appID string) ([]APIConsumerPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerPlan{}
	for _, plan := range m.apiConsumerPlans {
		if plan.AccountID == accountID && plan.AppID == appID {
			out = append(out, plan)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) AssignAPIConsumerPlan(_ context.Context, a APIConsumerPlanAssignment) (APIConsumerPlanAssignment, error) {
	if err := validateAPIConsumerPlanAssignment(a); err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.PlanID != "" {
		plan, ok := m.apiConsumerPlans[a.PlanID]
		if !ok || plan.AccountID != a.AccountID || plan.AppID != a.AppID {
			return APIConsumerPlanAssignment{}, ErrNotFound
		}
	}
	for _, existing := range m.apiConsumerPlanAssignments {
		if existing.ConsumerID == a.ConsumerID && existing.EffectiveFrom.Equal(a.EffectiveFrom) {
			return APIConsumerPlanAssignment{}, ErrConflict
		}
	}
	a.ID, a.CreatedAt = uuid.NewString(), time.Now().UTC()
	m.apiConsumerPlanAssignments[a.ID] = a
	return a, nil
}

func (m *MemStore) ListAPIConsumerPlanAssignments(_ context.Context, accountID, appID, consumerID string) ([]APIConsumerPlanAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.planAssignmentsLocked(accountID, appID, consumerID), nil
}

func (m *MemStore) planAssignmentsLocked(accountID, appID, consumerID string) []APIConsumerPlanAssignment {
	out := []APIConsumerPlanAssignment{}
	for _, a := range m.apiConsumerPlanAssignments {
		if a.AccountID == accountID && a.AppID == appID && a.ConsumerID == consumerID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EffectiveFrom.Before(out[j].EffectiveFrom) })
	return out
}

func (m *MemStore) GetAPIConsumerPlanPolicy(_ context.Context, accountID, appID, consumerID string, at time.Time) (APIConsumerPlanPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	planID := ""
	for _, a := range m.planAssignmentsLocked(accountID, appID, consumerID) {
		if !a.EffectiveFrom.After(at) {
			planID = a.PlanID
		}
	}
	plan, ok := m.apiConsumerPlans[planID]
	if planID == "" || !ok {
		return APIConsumerPlanPolicy{}, nil
	}
	policy := APIConsumerPlanPolicy{PlanID: plan.ID, AppID: plan.AppID, MaxRequestsPerMinute: plan.MaxRequestsPerMinute,
		MaxUnitsPerMonth: plan.MaxUnitsPerMonth, AlertThresholdsPercent: slices.Clone(plan.AlertThresholdsPercent)}
	var current APIConsumerRateCard
	for _, card := range m.apiConsumerRateCards {
		if card.PlanID == plan.ID && !card.EffectiveFrom.After(at) && card.EffectiveFrom.After(current.EffectiveFrom) {
			current = card
		}
	}
	policy.RouteWeights = maps.Clone(current.RouteWeights)
	return policy, nil
}

func (m *MemStore) AdmitAPIConsumerPlanRequest(_ context.Context, accountID, consumerID string, policy APIConsumerPlanPolicy, units int64) (APIConsumerPlanDecision, error) {
	if !policy.Limited() {
		return APIConsumerPlanDecision{Allowed: true}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	counter, decision, crossed := decidePlanAdmission(m.apiConsumerPlanAdmissions[consumerID], policy, units, now)
	if decision.Allowed {
		m.apiConsumerPlanAdmissions[consumerID] = counter
	}
	if len(crossed) > 0 {
		if err := m.recordAPIConsumerUsageAlertsLocked(accountID, consumerID, policy, counter, crossed, now); err != nil {
			return APIConsumerPlanDecision{}, err
		}
	}
	return decision, nil
}
