package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
			// ADR-20261026150000: the binding itself opens the database port.
			PrintOK(osStderr, "App %s's plan has no extra egress ports; the binding opens outbound TCP %d to this database's own addresses instead.", app.Slug, managedPostgresPort)
			return
		}
		PrintWarn(osStderr, "Could not open outbound TCP %d for app %s (%v); its connections will time out until you run: gregale app %s egress-ports add %d", managedPostgresPort, app.Slug, err, app.Slug, managedPostgresPort)
		return
	}
	PrintOK(osStderr, "Opened outbound TCP %d for app %s so it can reach the database (undo: gregale app %s egress-ports remove %d).", managedPostgresPort, app.Slug, app.Slug, managedPostgresPort)
}

type postgresBindingLister interface {
	ListManagedPostgresBindings(context.Context, string) (api.ManagedPostgresBindingList, error)
}

// postgresBindingIsNew reports whether the app has no live binding for the
// scope and key on the database yet. An unreadable list counts as new, so
// the delivery guidance errs toward being shown.
func postgresBindingIsNew(ctx context.Context, client postgresBindingLister, databaseID, appID, scope, key string) bool {
	list, err := client.ListManagedPostgresBindings(ctx, databaseID)
	if err != nil {
		return true
	}
	for _, binding := range list.Items {
		if binding.AppID == appID && binding.Scope == scope && binding.EnvironmentKey == key && binding.State != "deleted" {
			return false
		}
	}
	return true
}

// printPostgresBindingDelivery says when a new binding reaches the app. On
// production-us an attached key stayed absent from the running instance
// until `app restart`, and a migration binding never appeared there at all,
// with nothing saying either (hunt #9, H9-P111).
func printPostgresBindingDelivery(w io.Writer, appSlug, key, access string) {
	if access == "migration" {
		_, _ = fmt.Fprintf(w, "%s is delivered only to the release task (release.command); serving instances never receive migration credentials.\n", key)
		return
	}
	_, _ = fmt.Fprintf(w, "Running instances keep their current environment; %s applies when the app next starts. Run `gregale app %s restart` to apply it now.\n", key, appSlug)
}
