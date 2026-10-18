// Package repairhealth serves the admin-authorized, read-only repair projection.
package repairhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Handler struct{ Store state.DataRepairHealthStore }

func (h Handler) Load(r *http.Request) (api.DataRepairHealth, *api.Problem) {
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.DataRepairRehearsalPageSize, api.ObsAdminPaginationMax, "repair health")
	if prob != nil {
		return api.DataRepairHealth{}, prob
	}
	cursor := r.URL.Query().Get("cursor")
	if !state.ValidateDataRepairHealthCursor(cursor) {
		return api.DataRepairHealth{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid cursor", "Use next_cursor from a repair health response.")
	}
	if h.Store == nil {
		return api.DataRepairHealth{}, api.ErrCapacity("Repair health is unavailable.")
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairHealthQuerySeconds)*time.Second)
	defer cancel()
	snapshot, err := h.Store.DataRepairHealth(ctx, time.Now().UTC(), cursor, limit)
	if err != nil {
		return api.DataRepairHealth{}, api.ErrCapacity("Could not observe repair health; no healthy result has been inferred.")
	}
	return snapshot, nil
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, prob := h.Load(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}
