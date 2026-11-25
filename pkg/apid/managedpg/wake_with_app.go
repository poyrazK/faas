package managedpg

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// WakeWithAppService is the managed PostgreSQL service's wake-with-app
// surface; *managedpostgres.Service satisfies it.
type WakeWithAppService interface {
	SetWakeWithApp(ctx context.Context, accountID, databaseID string, enabled bool) error
}

// WakeWithApp serves PUT /v1/postgres/databases/{id}/wake-with-app: whether
// the database starts when an app bound to it wakes (ADR-20261125105038).
type WakeWithApp struct {
	// Service is nil when managed PostgreSQL is not configured.
	Service       WakeWithAppService
	Problem       func(http.ResponseWriter, error)
	NotConfigured func(http.ResponseWriter)
	Audit         interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
}

// Put turns the setting on or off; it is idempotent by value.
func (h WakeWithApp) Put(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Service == nil {
		h.NotConfigured(w)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpkit.DecodeJSON(r, &body); err != nil || body.Enabled == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "invalid_wake_with_app", "Invalid wake-with-app setting",
			"the body must be {\"enabled\": true} or {\"enabled\": false}").WithDocs(api.ManagedPostgresWakeWithAppDocsURL))
		return
	}
	if limits, ok := api.ManagedPostgresLimitsFor(acct.Plan); !ok || limits.DatabasesMax == 0 {
		h.Problem(w, managedpostgres.ErrNotInPlan)
		return
	}
	databaseID := r.PathValue("id")
	err := h.Service.SetWakeWithApp(r.Context(), acct.ID, databaseID, *body.Enabled)
	if errors.Is(err, managedpostgres.ErrWakeWithAppUnsupported) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "wake_with_app_unsupported", "Database cannot start with its app",
			"Only a scale-to-zero database on a backend that can start its compute can start with its app; an always-on database is always awake.").
			WithDocs(api.ManagedPostgresWakeWithAppDocsURL))
		return
	}
	if err != nil {
		h.Problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit.Emit(r.Context(), "managed_postgres.database.wake_with_app_set", &acct.ID,
			map[string]any{"database_id": databaseID, "enabled": *body.Enabled})
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ManagedPostgresWakeWithApp{DatabaseID: databaseID, Enabled: *body.Enabled})
}

// ApplyWakeWithApp adds the setting to a database's view.
func ApplyWakeWithApp(view *api.ManagedPostgresDatabase, enabled map[string]bool) {
	view.WakeWithApp = enabled[view.ID]
}
