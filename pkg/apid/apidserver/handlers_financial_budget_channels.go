package apidserver

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// financialBudgetChannelStore resolves the live policy and the channel store
// for the budget channel endpoints (ADR-20261019193500).
func (s *server) financialBudgetChannelStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.FinancialBudgetChannelStore, bool) {
	budgets, ok := s.financialBudgetStore(w, r, true)
	if !ok {
		return nil, false
	}
	channels, ok := s.store.(state.FinancialBudgetChannelStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("budget alert channels are unavailable on this deployment"))
		return nil, false
	}
	p, err := budgets.GetFinancialBudget(r.Context(), acct.ID, r.PathValue("id"))
	if err == nil && p.DeletedAt != nil {
		err = state.ErrNotFound
	}
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return nil, false
	}
	return channels, true
}

func (s *server) getFinancialBudgetChannels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if store, ok := s.financialBudgetChannelStore(w, r, acct); ok {
		writeFinancialBudgetChannels(w, r, acct, store)
	}
}

func (s *server) setFinancialBudgetChannels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetChannelStore(w, r, acct)
	if !ok {
		return
	}
	var req api.SetFinancialBudgetChannelsRequest
	if err := decodeJSONSized(r, &req, api.FinancialBudgetSpecBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("expected channel_ids"))
		return
	}
	if problem := s.validateFinancialBudgetChannels(r, acct.ID, req.ChannelIDs); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err := store.SetFinancialBudgetChannels(r.Context(), acct.ID, r.PathValue("id"), req.ChannelIDs); err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	s.audit.Emit(r.Context(), "financial_budget.channels_set", &acct.ID, map[string]any{"policy_id": r.PathValue("id"), "channel_ids": req.ChannelIDs, "actor": financialBudgetActor(r)})
	writeFinancialBudgetChannels(w, r, acct, store)
}

// validateFinancialBudgetChannels checks channel_ids the way alert rules do
// (ADR-749): bounded, no duplicates, every id one of the account's channels.
func (s *server) validateFinancialBudgetChannels(r *http.Request, accountID string, ids []string) *api.Problem {
	if len(ids) > api.MaxChannelsPerFinancialBudget {
		return api.ErrValidation(fmt.Sprintf("a budget can deliver to at most %d channels", api.MaxChannelsPerFinancialBudget)).WithLimit(api.MaxChannelsPerFinancialBudget, int64(len(ids)))
	}
	channels, ok := s.store.(state.NotificationChannelStore)
	if !ok {
		return api.ErrCapacity("notification channels are unavailable on this deployment")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return api.ErrValidation("channel_ids contains " + id + " twice")
		}
		seen[id] = true
		if _, err := channels.GetNotificationChannel(r.Context(), accountID, id); err != nil {
			if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) {
				return api.ErrValidation("channel_ids: " + id + " is not one of this account's notification channels")
			}
			return api.ErrCapacity("could not read notification channels")
		}
	}
	return nil
}

func writeFinancialBudgetChannels(w http.ResponseWriter, r *http.Request, acct state.Account, store state.FinancialBudgetChannelStore) {
	rows, err := store.ListFinancialBudgetChannels(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	out := api.FinancialBudgetChannelsResponse{PolicyID: r.PathValue("id"), Channels: make([]api.NotificationChannelResponse, 0, len(rows))}
	for _, row := range rows {
		out.Channels = append(out.Channels, channelResponse(row))
	}
	writeFinancialBudgetJSON(w, http.StatusOK, out)
}
