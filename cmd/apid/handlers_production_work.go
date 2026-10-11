package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func productionWorkRequest(w http.ResponseWriter, r *http.Request, detail string) bool {
	query := r.URL.Query()
	values, selected := query["environment"]
	if query.Has("scope") || (selected && (len(values) != 1 || values[0] != "production")) {
		api.WriteProblem(w, api.ErrValidation(detail))
		return false
	}
	return true
}

// This wrapper precedes idempotency so an old production receipt cannot
// satisfy a new request that explicitly selects a stage.
func productionDeadLetterHandler(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		if productionWorkRequest(w, r, "this endpoint manages production dead-letter events; stage dead-letter APIs are unavailable") {
			next(w, r, account)
		}
	}
}
