package apidserver

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsAlertRecurrence(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.URL.ForceQuery || len(q) > 1 || len(q) > 0 && (len(q["since"]) != 1 || q.Get("since") == "") {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid recurrence query", "only one nonempty since timestamp is accepted"))
		return
	}
	now := time.Now().UTC()
	since, err := operatorobs.RecurrenceSince(q.Get("since"), now)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid recurrence window", "since must precede now and be within 30 days"))
		return
	}
	enabled := s.promqlClient != nil && s.promqlClient.BaseURL() != ""
	out, err := operatorobs.AlertRecurrence(r.Context(), s.store, enabled, since, now)
	if err != nil {
		var bound *state.OperatorAlertRecurrenceLimitError
		if errors.As(err, &bound) {
			api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Recurrence cohort exceeds read bounds", "narrow since to read a complete cohort").WithLimit(int64(bound.Limit), int64(bound.Limit+1)))
			return
		}
		var coverageBound *state.OperatorAlertCollectionHistoryLimitError
		if errors.As(err, &coverageBound) {
			api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Collection coverage exceeds read bounds", "narrow since to read complete coverage").WithLimit(int64(coverageBound.Limit), int64(coverageBound.Limit+1)))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read alert recurrence"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
