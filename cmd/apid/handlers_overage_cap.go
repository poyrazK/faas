package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getOverageCap reads the authenticated account's saved ceiling. Keep null
// distinct from zero: zero disallows overage, while null removes the ceiling.
func (s *server) getOverageCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	cents, found, err := s.store.GetAccountOverageCapCents(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read overage cap"))
		return
	}
	var capCents *int64
	if found {
		capCents = &cents
	}
	writeJSON(w, http.StatusOK, struct {
		OverageCapCents *int64 `json:"overage_cap_cents"`
	}{OverageCapCents: capCents})
}
