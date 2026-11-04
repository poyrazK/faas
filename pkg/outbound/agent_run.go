package outbound

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Agent runs reach the gateway only through their VM's relay (ADR-20261027090010 §7).
// The relay names the run; every request then checks the run's lease and its
// agent version's integrations, attributes the call to the run's tenant, and
// holds the call's estimated cost against the run's model spend cap.

// AgentRunPrincipalPrefix starts an agent run's principal ID.
const AgentRunPrincipalPrefix = "agent_run:"

// Rejection reasons of agent run calls.
const (
	ReasonAgentRunBudget   = "agent_run_budget"
	ReasonAgentRunUnpriced = "agent_run_unpriced"
)

// AgentRunSpendLedger holds an agent run's model spend: settled spend plus
// one reservation per admitted call, keyed by the call's admission lease.
// Implementations must fail closed on store errors.
type AgentRunSpendLedger interface {
	// ReserveAgentRunSpend holds reserve against the run's cap unless settled
	// spend, live reservations and reserve would pass it. expires bounds the
	// reservation if the call never settles or releases.
	ReserveAgentRunSpend(ctx context.Context, runID, accountID, leaseID string, budget, reserve int64, expires time.Time) (bool, error)
	// SettleAgentRunSpend replaces the call's reservation with its cost.
	SettleAgentRunSpend(ctx context.Context, runID, leaseID string, cost int64) error
	// ReleaseAgentRunSpend drops a reservation that never settled.
	ReleaseAgentRunSpend(ctx context.Context, leaseID string) error
}

// authorizeAgentRun checks the run's lease and grant for this integration,
// then replaces the caller's end-user headers with the run's own: the tenant
// is the end user, and the run is the session (ADR-20261027090010 §7).
func (h *Handler) authorizeAgentRun(w http.ResponseWriter, r *http.Request, run AgentRunIdentity, integration Integration) (AgentRunGrant, bool) {
	if integration.OwnerKind != IntegrationOwnerCustomer || integration.ProviderAuthMode != ProviderAuthManaged ||
		integration.CredentialSource != CredentialSourceCustomerSealed || integration.AIProvider == "" {
		writeProblem(w, http.StatusForbidden, "outbound_agent_run_not_authorized", "Agent runs can use only the account's own AI integrations", "")
		return AgentRunGrant{}, false
	}
	if h.AgentRunAuthorizer == nil || h.AgentRunSpend == nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_agent_run_authorization_unavailable", "Agent run outbound authorization is unavailable", "1")
		return AgentRunGrant{}, false
	}
	grant, allowed, err := h.AgentRunAuthorizer.AuthorizeAgentRun(r.Context(), run, integration.ID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_agent_run_authorization_unavailable", "Agent run outbound authorization is unavailable", "1")
		return AgentRunGrant{}, false
	}
	if !allowed || grant.AIBudgetMillicents < 1 {
		writeProblem(w, http.StatusForbidden, "outbound_agent_run_not_authorized", "This agent run is not authorized for the outbound integration", "")
		return AgentRunGrant{}, false
	}
	r.Header.Del(api.OutboundAIUserHeader)
	if grant.TenantID != "" {
		r.Header.Set(api.OutboundAIUserHeader, grant.TenantID)
	}
	r.Header.Set(api.OutboundAISessionHeader, run.RunID)
	return grant, true
}

// reserveAgentRunSpend holds the call's estimated cost against the run's cap
// (ADR-991 hard-cap semantics). A billable call the gateway cannot price is
// refused, because the cap could not bound it. The refusals are 403s: the
// cap never resets, so SDK retries could not succeed.
func (h *Handler) reserveAgentRunSpend(ctx context.Context, w http.ResponseWriter, run AgentRunIdentity, grant AgentRunGrant,
	integration Integration, plan *aiCallPlan, leaseID string, timeout time.Duration, metricIntegrationID string) bool {
	var reserve int64
	if plan != nil {
		reserve = plan.reserve.cost
		if _, priced := aiPriceFor(integration, plan.reserve.estimate.priceModel()); plan.endpoint.billable() && !priced {
			h.Metrics.ObserveRejection(metricIntegrationID, ReasonAgentRunUnpriced)
			writeProblem(w, http.StatusForbidden, "outbound_agent_run_unpriced",
				"The gateway cannot price this model, so the agent run's spend cap cannot bound it; add a price to the integration", "")
			return false
		}
	}
	expires := time.Now().Add(max(timeout, 0) + api.AgentRunAIReservationSlackSec*time.Second)
	ok, err := h.AgentRunSpend.ReserveAgentRunSpend(ctx, run.RunID, run.AccountID, leaseID, grant.AIBudgetMillicents, reserve, expires)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_agent_run_authorization_unavailable", "Agent run outbound authorization is unavailable", "1")
		return false
	}
	if !ok {
		h.Metrics.ObserveRejection(metricIntegrationID, ReasonAgentRunBudget)
		w.Header().Set("X-Gregale-Outbound-Rejection", ReasonAgentRunBudget)
		writeProblem(w, http.StatusForbidden, "outbound_agent_run_budget_exhausted", "The agent run's model spend cap is reached", "")
		return false
	}
	return true
}

// settleAgentRunSpend records a finished call's cost against its run. A
// store error leaves the reservation to lapse, so the run is under-counted
// by at most that call; it is counted as a usage-record event.
func (h *Handler) settleAgentRunSpend(ctx context.Context, leaseID string, cost int64, metricIntegrationID string) {
	run, ok := relayAgentRunFromContext(ctx)
	if !ok || h.AgentRunSpend == nil || leaseID == "" {
		return
	}
	if err := h.AgentRunSpend.SettleAgentRunSpend(context.WithoutCancel(ctx), run.RunID, leaseID, max(cost, 0)); err != nil {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "agent_run_spend_error")
	}
}

func (h *Handler) releaseAgentRunSpend(ctx context.Context, leaseID, metricIntegrationID string) {
	if h.AgentRunSpend == nil || leaseID == "" {
		return
	}
	if err := h.AgentRunSpend.ReleaseAgentRunSpend(ctx, leaseID); err != nil {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "agent_run_spend_error")
	}
}
