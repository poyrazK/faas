package appevents

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetEventSubscriptionDeadlineWindow(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	within, err := parseSubscriptionDeadlineWindow(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("supply exactly one within_millis integer from 100 through 86400000"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionDeadlineReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionDeadlineWindowStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("subscription deadline window"))
		return
	}
	out, err := store.GetEventSubscriptionDeadlineWindow(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), within)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid subscription or deadline window"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Deadline window timed out", "coverage is unknown; no partial counts were returned"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("subscription deadline window"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func parseSubscriptionDeadlineWindow(raw string) (int64, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values["within_millis"]) != 1 {
		return 0, state.ErrInvalidArgument
	}
	within, err := strconv.ParseInt(values.Get("within_millis"), 10, 64)
	if err != nil || !api.ValidEventSubscriptionDeadlineWindow(within) {
		return 0, state.ErrInvalidArgument
	}
	return within, nil
}
