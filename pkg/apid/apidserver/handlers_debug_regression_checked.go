package apidserver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *server) applyCheckedRegressionAction(w http.ResponseWriter, r *http.Request, acct state.Account, appID string, arg sqlc.ApplyRegressionActionParams, req api.GuardedDebugRegressionActionRequest) {
	store, ok := s.store.(state.CheckedRegressionActionStore)
	if !ok {
		api.WriteProblem(w, api.ErrDebugRegressionUnavailable("checked regression persistence is unavailable"))
		return
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid checked regression body"))
		return
	}
	var item api.DebugRegressionItem
	render := func(row sqlc.DebugRegressionObservation) ([]byte, error) {
		item = debugRegressionObservationToItem(row)
		review := debugRegressionReview(appID, row)
		return json.Marshal(api.DebugRegressionActionResponse{Regression: item, Review: &review})
	}
	body, replayed, err := store.ApplyCheckedRegressionAction(r.Context(), acct.ID, sha256.Sum256(encoded), req.Reason, arg, render)
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("checked action arguments or dismissal expiry are invalid"))
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRegressionChanged, "Regression changed", "observation changed or disappeared; review again"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrDebugRegressionUnavailable("checked action and receipt could not be committed"))
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	} else {
		s.emitRegressionAction(r, acct, appID, item, req)
	}
	writeJSON(w, http.StatusOK, json.RawMessage(body))
}
