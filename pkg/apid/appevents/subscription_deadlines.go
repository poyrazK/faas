package appevents

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListEventSubscriptionDeadlines(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := parseSubscriptionDeadlineQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid deadline bounds, status, page size or cursor"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionDeadlineReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionDeadlineStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("subscription deadline listing"))
		return
	}
	out, err := store.ListEventSubscriptionDeadlines(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), query)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid subscription, deadline bounds, status or cursor; keep scope, filters and limit unchanged across pages"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Deadline listing timed out", "no partial page was returned; narrow deadline bounds or retry"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("subscription deadline listing"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func parseSubscriptionDeadlineQuery(rawQuery string) (api.EventSubscriptionDeadlineQuery, error) {
	var q api.EventSubscriptionDeadlineQuery
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return q, state.ErrInvalidArgument
	}
	for key, raw := range values {
		if len(raw) != 1 {
			return q, state.ErrInvalidArgument
		}
		switch key {
		case "deadline_from", "deadline_before":
			at, err := time.Parse(time.RFC3339Nano, raw[0])
			if err != nil {
				return q, state.ErrInvalidArgument
			}
			if key == "deadline_from" {
				q.DeadlineFrom = &at
			} else {
				q.DeadlineBefore = &at
			}
		case "within_millis":
			n, err := strconv.ParseInt(raw[0], 10, 64)
			if err != nil || !api.ValidEventSubscriptionDeadlineWindow(n) {
				return q, state.ErrInvalidArgument
			}
			q.WithinMillis = n
		case "waiting_reason":
			q.WaitingReason = raw[0]
		case "status":
			q.Status = raw[0]
		case "cursor":
			q.Cursor = raw[0]
		case "limit":
			n, err := strconv.Atoi(raw[0])
			if err != nil || n < 1 {
				return q, state.ErrInvalidArgument
			}
			q.Limit = n
		default:
			return q, state.ErrInvalidArgument
		}
	}
	return q, q.Validate()
}
