package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// postgresConnectionsTimeout bounds the reads the connection total adds to a
// canary that already passed; a slow read skips the comparison, never the
// verification.
const postgresConnectionsTimeout = 10 * time.Second

// postgresConnectionBudgetClient is what the comparison reads beyond the
// canary. *api.Client implements it; a client that does not skips it.
type postgresConnectionBudgetClient interface {
	managedPostgresCatalogClient
	GetManagedPostgresConnectionBudget(context.Context, string) (api.ManagedPostgresConnectionBudget, error)
}

// postgresBindingConnections totals the binding's pools across the app's
// instance ceiling and compares the total with the database's qualified
// connection allocation where one exists (ADR-20261104141122 step 4).
func postgresBindingConnections(ctx context.Context, client serviceBindingProbeClient, slug, database string, poolMax int) *api.PostgresBindingConnections {
	ctx, cancel := context.WithTimeout(ctx, postgresConnectionsTimeout)
	defer cancel()
	if poolMax < 1 {
		out := api.AssessPostgresBindingConnections(poolMax, 0, nil)
		return &out
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		out := api.AssessPostgresBindingConnections(poolMax, 0, nil)
		out.Detail = "the app's instance limit could not be read"
		return &out
	}
	catalog, ok := client.(postgresConnectionBudgetClient)
	if !ok || database == "" {
		out := api.AssessPostgresBindingConnections(poolMax, app.MaxConcurrency, nil)
		return &out
	}
	usable, err := postgresUsableConnections(ctx, catalog, database)
	out := api.AssessPostgresBindingConnections(poolMax, app.MaxConcurrency, usable)
	if err != nil && out.Status == "not_checked" && out.Total > 0 {
		out.Detail = "the database's connection allocation could not be read"
	}
	return &out
}

// postgresUsableConnections is the database's qualified allocation, nil when
// it has none. A budget the CLI cannot trust counts as none.
func postgresUsableConnections(ctx context.Context, client postgresConnectionBudgetClient, database string) (*int64, error) {
	resolved, err := resolveManagedPostgresDatabase(ctx, client, database)
	if err != nil {
		return nil, err
	}
	budget, err := client.GetManagedPostgresConnectionBudget(ctx, resolved.ID)
	if err != nil {
		return nil, err
	}
	if budget.DatabaseID != resolved.ID || !validDataAPIConnectionBudget(budget, time.Now()) || budget.Capacity.State != "verified" {
		return nil, nil
	}
	return budget.Capacity.UsableConnections, nil
}

// postgresProbeRow is one line of the rendered PostgreSQL canary report.
type postgresProbeRow struct {
	name  string
	check api.PostgresBindingProbeCheck
}
