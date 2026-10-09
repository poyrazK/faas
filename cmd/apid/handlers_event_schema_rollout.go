package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewEventSchemaRollout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSchemaRolloutTimeout)
	defer cancel()
	var req api.EventSchemaRolloutRequest
	if err := decodeJSONSized(r, &req, api.EventSchemaRolloutBodyMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid or oversized rollout preview body"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventSchemaRolloutStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("schema rollout preview"))
		return
	}
	out, err := store.PreviewEventSchemaRollout(ctx, acct.ID, req)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	case errors.Is(err, state.ErrEventSchemaUnknown):
		s.notFound(w, "schema version is not registered; provide a proposed schema")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Schema rollout preview timed out", "retry with a narrower range"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("schema rollout preview"))
	default:
		writeJSON(w, http.StatusOK, out)
	}
}
