package appevents

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListEventSubscriptionCapacityCommands(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionCapacityCommandListStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("capacity command history"))
		return
	}
	query, err := capacityCommandHistoryQuery(r)
	var out api.EventSubscriptionCapacityCommands
	if err == nil {
		out, err = store.ListEventSubscriptionCapacityCommands(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), query)
	}
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid subscription UUID, outcome filter, page limit or scoped cursor"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no accessible event subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Capacity command history timed out", "retry the read-only listing"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("capacity command history"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func capacityCommandHistoryQuery(r *http.Request) (api.EventSubscriptionCapacityCommandsQuery, error) {
	values := r.URL.Query()
	query := api.EventSubscriptionCapacityCommandsQuery{Outcome: values.Get("outcome"), After: values.Get("after")}
	for _, name := range []string{"outcome", "after", "limit"} {
		if len(values[name]) > 1 {
			return query, state.ErrInvalidArgument
		}
	}
	if raw, provided := values["limit"]; provided {
		if len(raw) != 1 {
			return query, state.ErrInvalidArgument
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > api.EventSubscriptionCapacityCommandPageMax {
			return query, state.ErrInvalidArgument
		}
		query.Limit = n
	}
	return query, nil
}
