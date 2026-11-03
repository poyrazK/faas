package appevents

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// appSourceSchemaProblem refuses a schema for an app.<app id> source unless
// the caller's account owns the app and the caller may still deploy it. The
// app's own publishes are validated against its account's schemas, and a
// registered schema makes schema_version mandatory, so without this a
// creator who left the company could make the company's publishes fail.
func (s *Service) appSourceSchemaProblem(r *http.Request, acct state.Account, appID string) *api.Problem {
	store, ok := s.Host.Store().(appaccess.Store)
	if !ok {
		return api.ErrInternal("event schema access")
	}
	app, err := store.AppByID(r.Context(), appID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return api.ErrInternal("event schema access")
	}
	if err != nil || app.AccountID != acct.ID {
		return api.ErrValidation("app.<app id> schemas are registered by the account that owns the app")
	}
	deploys, err := appaccess.Standing(r.Context(), store, acct.ID, app, authz.OrgActionDeployApp)
	if err != nil {
		return api.ErrInternal("event schema access")
	}
	if !deploys {
		return api.ErrOrgRoleForbidden("registering schemas for this app's events")
	}
	return nil
}

func (s *Service) RegisterEventSchema(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.RegisterEventSchemaRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
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
	if appID, reserved := eventcontract.AppSource(req.Source); reserved {
		if problem := s.appSourceSchemaProblem(r, acct, appID); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	registry, ok := s.Host.Store().(state.EventSchemaStore)
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
		s.Host.Logger().Error("register event schema failed", "err", logsanitize.FieldAny(err))
		api.WriteProblem(w, api.ErrCapacity("failed to register event schema"))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpkit.WriteJSON(w, status, api.RegisterEventSchemaResponse{Source: req.Source, Type: req.Type, Version: req.Version, Created: created})
}

func (s *Service) ListEventSchemas(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, typ := r.URL.Query().Get("source"), r.URL.Query().Get("type")
	if source == "" || typ == "" || len(source) > 256 || len(typ) > 256 {
		api.WriteProblem(w, api.ErrValidation("source and type query parameters are required and must be at most 256 characters"))
		return
	}
	registry, ok := s.Host.Store().(state.EventSchemaStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event schema registry"))
		return
	}
	definitions, err := registry.ListEventSchemasForSourceType(r.Context(), acct.ID, source, typ)
	if err != nil {
		s.Host.Logger().Error("list event schemas failed", "err", err)
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
	httpkit.WriteJSON(w, http.StatusOK, response)
}
