package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderManagedPostgres renders the read-only customer overview. Mutations
// remain on the authenticated API/CLI surface so this page cannot accidentally
// create a database without a dedicated form and CSRF contract.
func (s *server) renderManagedPostgres(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	data := dashboard.ManagedPostgresData{Available: s.managedPostgres != nil}
	if s.managedPostgres == nil {
		data.Error = "Managed PostgreSQL is not enabled for this environment yet."
	} else {
		databases, err := s.managedPostgres.List(r.Context(), acct.ID)
		if err != nil {
			log.Warn("dashboard renderManagedPostgres: list databases", "account_id", acct.ID, "err", err)
			data.Error = "Managed PostgreSQL status is temporarily unavailable."
		} else {
			data.Databases = projectManagedPostgresDashboard(databases)
			s.attachManagedPostgresBindings(r, log, acct.ID, &data)
		}
	}
	view, _ := AccountFrom(r.Context())
	appCount, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	page := dashboard.Page{Title: "Managed PostgreSQL", Body: "postgres", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func projectManagedPostgresDashboard(databases []managedpostgres.Database) []dashboard.ManagedPostgresDatabaseItem {
	items := make([]dashboard.ManagedPostgresDatabaseItem, 0, len(databases))
	for _, database := range databases {
		item := dashboard.ManagedPostgresDatabaseItem{
			ID: database.ID, Name: database.Name, Region: database.Spec.Region,
			PostgresMajor: database.Spec.PostgresMajor, ServiceClass: string(database.Spec.Class),
			Availability: string(database.Spec.Availability), ScaleToZero: database.Spec.ScaleToZero,
			StorageLimitBytes: database.Spec.StorageLimitBytes, RestoreWindowSeconds: database.Spec.RestoreWindowSeconds,
			StorageLimitLabel: formatManagedPostgresStorage(database.Spec.StorageLimitBytes),
			State:             string(database.State), LastErrorCode: database.LastErrorCode,
			CreatedAt: database.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: database.UpdatedAt.UTC().Format(time.RFC3339),
		}
		items = append(items, item)
	}
	return items
}

func formatManagedPostgresStorage(bytes int64) string {
	const gib = int64(1 << 30)
	if bytes > 0 && bytes%gib == 0 {
		return fmt.Sprintf("%d GiB", bytes/gib)
	}
	return fmt.Sprintf("%d bytes", bytes)
}

func (s *server) attachManagedPostgresBindings(r *http.Request, log *slog.Logger, accountID string, data *dashboard.ManagedPostgresData) {
	if s.managedPostgresBindings == nil {
		return
	}
	for i := range data.Databases {
		bindings, err := s.managedPostgresBindings.List(r.Context(), accountID, data.Databases[i].ID)
		if err != nil {
			log.Warn("dashboard renderManagedPostgres: list bindings", "account_id", accountID, "database_id", data.Databases[i].ID, "err", err)
			data.Databases[i].BindingsError = "Binding status is temporarily unavailable."
			continue
		}
		for _, binding := range bindings {
			data.Databases[i].Bindings = append(data.Databases[i].Bindings, dashboard.ManagedPostgresBindingItem{
				ID: binding.ID, AppID: binding.AppID, Scope: binding.Scope, EnvironmentKey: binding.EnvironmentKey,
				Access: string(binding.Access), CredentialGeneration: binding.CredentialGeneration,
				State: string(binding.State), LastErrorCode: binding.LastErrorCode,
			})
		}
	}
}
