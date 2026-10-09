package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) eventSubscriptionSchemaVersions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventSubscriptionSchemaVersionsStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event schema versions"))
		return
	}
	out, err := applyEventSubscriptionSchemaVersions(ctx, store, r, acct.ID, app.ID)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("provide a subscription UUID and an array of at most 16 unique schema version identifiers"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no matching app subscription")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Schema version request timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event schema versions"))
	default:
		if r.Method != http.MethodGet {
			s.audit.Emit(ctx, "event.subscription.schema_versions.updated", &acct.ID, map[string]any{"app_id": app.ID, "subscription_id": out.SubscriptionID, "schema_versions": out.SchemaVersions})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
func applyEventSubscriptionSchemaVersions(ctx context.Context, store state.EventSubscriptionSchemaVersionsStore, r *http.Request, account, app string) (api.EventSubscriptionSchemaVersionsResponse, error) {
	id := r.PathValue("subscriptionID")
	if r.Method == http.MethodGet {
		return store.GetEventSubscriptionSchemaVersions(ctx, account, app, id)
	}
	var versions []string
	if r.Method == http.MethodPut {
		var body api.EventSubscriptionSchemaVersionsRequest
		if err := decodeJSONSized(r, &body, 4<<10); err != nil || body.SchemaVersions == nil {
			return api.EventSubscriptionSchemaVersionsResponse{}, state.ErrInvalidArgument
		}
		versions = body.SchemaVersions
	}
	normalized, err := api.NormalizeEventSchemaVersions(versions)
	if err != nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, state.ErrInvalidArgument
	}
	if _, err = store.SetEventSubscriptionSchemaVersions(ctx, account, app, id, normalized); err != nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, err
	}
	return api.EventSubscriptionSchemaVersionsResponse{SubscriptionID: canonicalEventPreviewUUID(id), SchemaVersions: append([]string{}, normalized...)}, nil
}
