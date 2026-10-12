package main

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// managedPostgresPort is the TCP port managed PostgreSQL bindings connect to.
const managedPostgresPort = 5432

type postgresEgressClient interface {
	UpdateApp(context.Context, string, api.UpdateAppRequest) (api.AppResponse, error)
}

// ensurePostgresEgress lets a newly bound app reach its database. Tenant
// egress allows only TCP 80/443 by default (ADR-361), so a ready binding
// produced an app whose every connection timed out (production-us hunt #9,
// H9-P110). `data-api create` already opened the port; this does the same
// for ordinary bindings, merging with the app's existing extra ports and
// staying subject to the plan gate. It never fails the attach: the binding
// is already durable, so problems are reported as warnings with the fix.
func ensurePostgresEgress(ctx context.Context, client postgresEgressClient, app api.AppResponse) {
	if slices.Contains(app.EgressPorts, managedPostgresPort) {
		return
	}
	ports := append(append([]int(nil), app.EgressPorts...), managedPostgresPort)
	if _, err := client.UpdateApp(ctx, app.Slug, api.UpdateAppRequest{EgressPorts: &ports}); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodePlanEgressPortsNotAllowed {
			PrintWarn(osStderr, "App %s cannot open outbound TCP %d on its plan, so it cannot connect to this database; upgrade to Pro or Scale to allow it.", app.Slug, managedPostgresPort)
			return
		}
		PrintWarn(osStderr, "Could not open outbound TCP %d for app %s (%v); its connections will time out until you run: gregale app %s egress-ports add %d", managedPostgresPort, app.Slug, err, app.Slug, managedPostgresPort)
		return
	}
	PrintOK(osStderr, "Opened outbound TCP %d for app %s so it can reach the database (undo: gregale app %s egress-ports remove %d).", managedPostgresPort, app.Slug, app.Slug, managedPostgresPort)
}
