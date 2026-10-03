package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) registerEventSchema(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.RegisterEventSchemaRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	req.Type = strings.TrimSpace(req.Type)
	req.Version = strings.TrimSpace(req.Version)
	if strings.HasPrefix(req.Source, "gregale.") {
		api.WriteProblem(w, api.ErrValidation("gregale.* sources are reserved for platform events"))
		return
	}
	registry, ok := s.store.(state.EventSchemaStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event schema registry"))
		return
	}
	def := state.EventSchema{
		AccountID: acct.ID, Source: req.Source, Type: req.Type, Version: req.Version, Schema: req.Schema,
	}
	if err := state.ValidateEventSchemaDefinition(def); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	created, err := registry.PutEventSchema(r.Context(), def)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Schema version conflict", "register a new version to change the schema"))
		return
	}
	if err != nil {
		s.log.Error("register event schema failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to register event schema"))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, api.RegisterEventSchemaResponse{Source: req.Source, Type: req.Type, Version: req.Version, Created: created})
}

func (s *server) listEventSchemas(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, typ := r.URL.Query().Get("source"), r.URL.Query().Get("type")
	if source == "" || typ == "" || len(source) > 256 || len(typ) > 256 {
		api.WriteProblem(w, api.ErrValidation("source and type query parameters are required and must be at most 256 characters"))
		return
	}
	registry, ok := s.store.(state.EventSchemaStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event schema registry"))
		return
	}
	definitions, err := registry.ListEventSchemasForSourceType(r.Context(), acct.ID, source, typ)
	if err != nil {
		s.log.Error("list event schemas failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to list event schemas"))
		return
	}
	response := make([]api.EventSchema, 0, len(definitions))
	for _, def := range definitions {
		response = append(response, api.EventSchema{
			AccountID: def.AccountID, Source: def.Source, Type: def.Type,
			Version: def.Version, Schema: def.Schema, CreatedAt: def.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, response)
}
