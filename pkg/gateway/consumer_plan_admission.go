package gateway

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ConsumerPlanPolicy is what a consumer's current plan enforces (ADR-940).
// Zero limits mean unlimited; RouteWeights makes the monthly cap count the
// same weighted units as billing.
type ConsumerPlanPolicy struct {
	PlanID               string
	MaxRequestsPerMinute int64
	MaxUnitsPerMonth     int64
	RouteWeights         map[string]int64
}

func (p ConsumerPlanPolicy) limited() bool {
	return p.MaxRequestsPerMinute > 0 || p.MaxUnitsPerMonth > 0
}

func (p ConsumerPlanPolicy) units(route string) int64 {
	if weight, ok := p.RouteWeights[route]; ok && weight > 0 {
		return weight
	}
	return 1
}

// ConsumerPlanDecision is one admission outcome; Scope is "minute" or
// "month" when a limit denied the request.
type ConsumerPlanDecision struct {
	Allowed           bool
	Scope             string
	Limit             int64
	Observed          int64
	RetryAfterSeconds int64
}

// ConsumerPlanStore resolves a consumer's plan policy and admits requests
// against one cross-replica counter row. Like tenant budgets, it never falls
// back to a replica-local allowance.
type ConsumerPlanStore interface {
	ConsumerPlanPolicy(ctx context.Context, accountID, appID, consumerID string) (ConsumerPlanPolicy, error)
	AdmitConsumerPlanRequest(ctx context.Context, accountID, consumerID string, policy ConsumerPlanPolicy, units int64) (ConsumerPlanDecision, error)
}

// consumerPlanPolicyTTL bounds how stale a cached policy can be: a plan
// change or limit update takes effect at the gateway within this window.
const consumerPlanPolicyTTL = 15 * time.Second

type cachedConsumerPlanPolicy struct {
	policy  ConsumerPlanPolicy
	expires time.Time
}

type consumerPlanPolicyCache struct{ entries sync.Map } // consumerID → cachedConsumerPlanPolicy

// WithConsumerPlanStore arms plan enforcement for consumer-attributed
// traffic.
func (h *Handler) WithConsumerPlanStore(store ConsumerPlanStore) *Handler {
	h.consumerPlanStore = store
	h.consumerPlanPolicies = &consumerPlanPolicyCache{}
	return h
}

func (h *Handler) consumerPlanPolicy(ctx context.Context, app App, consumerID string) (ConsumerPlanPolicy, error) {
	now := time.Now()
	if v, ok := h.consumerPlanPolicies.entries.Load(consumerID); ok {
		if cached := v.(cachedConsumerPlanPolicy); now.Before(cached.expires) {
			return cached.policy, nil
		}
	}
	policy, err := h.consumerPlanStore.ConsumerPlanPolicy(ctx, app.AccountID, app.ID, consumerID)
	if err != nil {
		return ConsumerPlanPolicy{}, err
	}
	h.consumerPlanPolicies.entries.Store(consumerID, cachedConsumerPlanPolicy{policy: policy, expires: now.Add(consumerPlanPolicyTTL)})
	return policy, nil
}

// enforceConsumerPlan admits a consumer's request against its plan's
// per-minute request limit and monthly weighted-unit cap. Denied and
// unverifiable requests never become billable. Consumers on unlimited plans
// cost one cached lookup and no counter write.
func (h *Handler) enforceConsumerPlan(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App, deploymentSmoke bool) bool {
	consumerID := authenticatedFrom(r.Context()).ConsumerID
	if deploymentSmoke || h.consumerPlanStore == nil || consumerID == "" {
		return true
	}
	policy, err := h.consumerPlanPolicy(r.Context(), app, consumerID)
	if err == nil && !policy.limited() {
		return true
	}
	var decision ConsumerPlanDecision
	if err == nil {
		decision, err = h.consumerPlanStore.AdmitConsumerPlanRequest(r.Context(), app.AccountID, consumerID, policy, policy.units(billingRouteFrom(r)))
	}
	if err != nil {
		if h.log != nil {
			h.log.Error("consumer plan admission unavailable", "err", err, "consumer_id", consumerID)
		}
		suppressFinancialUsage(r)
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeConsumerPlanLimitUnavailable,
			"Plan limit unavailable", "request admission could not be verified"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	if decision.Allowed {
		return true
	}
	suppressFinancialUsage(r)
	w.Header().Set("Retry-After", strconv.FormatInt(max(1, decision.RetryAfterSeconds), 10))
	w.Header().Set("x-faas-rate-limit-scope", "consumer-plan-"+decision.Scope)
	detail := "the consumer's plan allows " + strconv.FormatInt(decision.Limit, 10) + " requests per minute"
	if decision.Scope == "month" {
		detail = "the consumer's plan allows " + strconv.FormatInt(decision.Limit, 10) + " units per month; " +
			strconv.FormatInt(decision.Observed, 10) + " are used"
	}
	api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeConsumerPlanLimitExceeded, "Plan limit exceeded", detail))
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
	return false
}
