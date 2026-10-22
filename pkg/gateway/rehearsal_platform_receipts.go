// adr: 20261022191742
package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// A captured event is accepted and never routed, so it has no recipients:
// a recipient's replay or attempt history is the public not-found, and a
// recipient blocker reports the recipient unavailable.

func eventReceiptNotFound() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event receipt not found", "No retained receipt exists for this source and id")
}

// eventRecipientHistoryRoute serves GET /v1/events/receipt/replays and
// /attempts with apid's validation; a run's receipts have no recipients.
func eventRecipientHistoryRoute(label, cursor string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, _ rehearsalPlatformCall) {
		query := r.URL.Query()
		for _, value := range []string{query.Get("source"), query.Get("id"), query.Get("subscription_id")} {
			if value = strings.TrimSpace(value); value == "" || len(value) > events.EnvelopeStringMax {
				api.WriteProblem(w, api.ErrValidation("source, id and subscription_id are required and must be at most 256 bytes"))
				return
			}
		}
		if problem, _ := api.ParseLimit(query.Get("limit"), 100, api.EventReceiptPageMax, label); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		// No page of a recipient-less receipt exists to continue from.
		if query.Get("after") != "" {
			api.WriteProblem(w, api.ErrValidation("invalid or mismatched event receipt "+cursor+" cursor"))
			return
		}
		api.WriteProblem(w, eventReceiptNotFound())
	}
}

// serveRehearsalRecipientBlocker serves GET /v1/events/recipient-blocker.
func serveRehearsalRecipientBlocker(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
	values := r.URL.Query()
	guard, err := api.ParseAppEventAcceptanceGuard(values.Get("expected_accepted_at"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	query := api.EventRecipientBlockerQuery{Source: values.Get("source"), ID: values.Get("id"), SubscriptionID: values.Get("subscription_id")}
	if guard.ExpectedAcceptedAt != nil {
		query.ExpectedAcceptedAt = *guard.ExpectedAcceptedAt
	}
	if err := query.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := rehearsalRecipientBlocker(r, call, query)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read recipient blocker"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeRehearsalCaptured(w, http.StatusOK, out)
}

// serveRehearsalRecipientBlockers serves POST /v1/events/recipient-blockers,
// a read-only batch of blocker queries.
func serveRehearsalRecipientBlockers(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
	body, problem := readRehearsalPlatformBody(w, r, api.EventRecipientBlockerBatchBodyMaxBytes, call.now)
	var request api.EventRecipientBlockerBatchRequest
	if problem == nil {
		problem = decodeRehearsalStrictJSON(body, &request)
	}
	if problem != nil {
		api.WriteProblem(w, api.ErrValidation(problem.Detail))
		return
	}
	if err := request.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out := api.EventRecipientBlockerBatchResponse{Items: make([]api.EventRecipientBlockerBatchItem, len(request.Queries))}
	for i, query := range request.Queries {
		out.Items[i] = api.EventRecipientBlockerBatchItem{Query: query}
		result, err := rehearsalRecipientBlocker(r, call, query)
		if err != nil {
			out.Items[i].Error = "read_error"
			continue
		}
		out.Items[i].Result = &result
	}
	w.Header().Set("Cache-Control", "no-store")
	writeRehearsalCaptured(w, http.StatusOK, out)
}

// rehearsalRecipientBlocker mirrors the store's answer for a captured event:
// a replaced acceptance, an unavailable recipient, or no receipt at all.
func rehearsalRecipientBlocker(r *http.Request, call rehearsalPlatformCall, query api.EventRecipientBlockerQuery) (api.EventRecipientBlockerResponse, error) {
	out := api.EventRecipientBlockerResponse{Query: query, ObservedAt: call.now, Status: "unavailable", Reason: "receipt_unavailable"}
	record, err := call.store.ScenarioCapturedEvent(r.Context(), call.policy.AccountID, call.policy.RunID, query.Source, query.ID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return out, nil
	case err != nil:
		return out, err
	case !record.CreatedAt.Equal(query.ExpectedAcceptedAt):
		out.Status, out.Reason = "acceptance_replaced", "acceptance_replaced"
	default:
		out.Reason = "recipient_unavailable"
	}
	return out, nil
}
