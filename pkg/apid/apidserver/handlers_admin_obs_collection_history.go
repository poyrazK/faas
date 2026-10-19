package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsAlertCollectionHistory(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	rawSince, details, err := operatorobs.CollectionHistoryQuery(r.URL.RawQuery, r.URL.ForceQuery)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid collection history query", "only one nonempty since timestamp and one details=true or details=false are accepted"))
		return
	}
	now := time.Now().UTC()
	since, err := operatorobs.CollectionHistorySince(rawSince, now)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid collection history window", "since must precede now and be within 30 days"))
		return
	}
	enabled := s.promqlClient != nil && s.promqlClient.BaseURL() != ""
	out, err := operatorobs.AlertCollectionHistory(r.Context(), s.store, enabled, since, now, details)
	if err != nil {
		var bound *state.OperatorAlertCollectionHistoryLimitError
		if errors.As(err, &bound) {
			api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Collection history cohort exceeds read bounds", "narrow since to read a complete cohort").WithLimit(int64(bound.Limit), int64(bound.Limit+1)))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read alert collection history"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
