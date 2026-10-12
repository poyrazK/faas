package outboundai

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const userUsageInvalidDetail = "date must be a YYYY-MM-DD UTC day within the last 31 days, end_user 1 to 64 letters, digits or . _ : @ + = / | -, and limit 1 to 1,000"

// GetUserUsage lists one UTC day of AI usage by end user, most expensive
// first (ADR-992). ?end_user= reads one user; ?date= and ?limit= page.
func (s *Service) GetUserUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(r, api.OutboundAIUserUsagePageMax)
	if !ok {
		problem(w, http.StatusBadRequest, api.CodeValidation, userUsageInvalidDetail)
		return
	}
	store, ok := s.Host.Store().(state.OutboundAIUserStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
		return
	}
	query := r.URL.Query()
	usage, err := store.GetOutboundAIUserUsage(r.Context(), acct.ID, integrationID, query.Get("date"), query.Get("end_user"), limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, userUsageInvalidDetail)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, userUsageResponse(usage))
	}
}

func userUsageResponse(usage state.OutboundAIUserUsage) api.OutboundAIUserUsageResponse {
	out := api.OutboundAIUserUsageResponse{UsageDate: usage.UsageDate, TrackedUsers: usage.TrackedUsers,
		UserDailyTokenLimit: copyInt64(usage.UserDailyTokenLimit), UserDailyCostLimitMillicents: copyInt64(usage.UserDailyCostLimit),
		Items: make([]api.OutboundAIUserUsageItem, 0, len(usage.Items))}
	for _, item := range usage.Items {
		out.Items = append(out.Items, api.OutboundAIUserUsageItem{EndUser: item.EndUser, Requests: item.Requests,
			ErrorRequests: item.ErrorRequests, InputTokens: item.InputTokens, OutputTokens: item.OutputTokens,
			Tokens: item.InputTokens + item.OutputTokens, CostMillicents: item.CostMillicents})
	}
	return out
}
