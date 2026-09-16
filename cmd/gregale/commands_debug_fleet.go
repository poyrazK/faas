package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// debugFleetRegressionsResponse is the account-wide CLI envelope. The API is
// intentionally still app-scoped; the CLI composes the existing app endpoint
// so operators can inspect a fleet without losing the per-app contract.
type debugFleetRegressionsResponse struct {
	Since string                    `json:"since"`
	Apps  []debugFleetRegressionApp `json:"apps"`
}

type debugFleetRegressionApp struct {
	Slug        string                    `json:"slug"`
	Regressions []api.DebugRegressionItem `json:"regressions,omitempty"`
	Error       string                    `json:"error,omitempty"`
}

func cmdDebugRegressionsAll(ctx context.Context, client *api.Client, since string) int {
	apps, err := client.ListApps(ctx)
	if err != nil {
		return printErr("Could not list apps for debugger regressions", err)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })

	response := debugFleetRegressionsResponse{
		Since: since,
		Apps:  make([]debugFleetRegressionApp, 0, len(apps)),
	}
	partial := false
	for _, app := range apps {
		regressions, err := client.ListAppDebugRegressions(ctx, app.Slug, since)
		item := debugFleetRegressionApp{Slug: app.Slug}
		if err != nil {
			item.Error = err.Error()
			partial = true
		} else {
			item.Regressions = regressions.Regressions
			if response.Since == "" {
				response.Since = regressions.Since
			}
		}
		response.Apps = append(response.Apps, item)
	}

	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"since":   response.Since,
			"partial": partial,
			"apps":    response.Apps,
		}))
	}
	if response.Since != "" {
		_, _ = fmt.Fprintf(osStdout, "Debugger regressions · since %s\n", response.Since)
	}
	printed := false
	for _, app := range response.Apps {
		if app.Error != "" {
			_, _ = fmt.Fprintf(osStderr, "debug regressions %s: %s\n", app.Slug, app.Error)
			continue
		}
		if len(app.Regressions) == 0 {
			continue
		}
		printed = true
		_, _ = fmt.Fprintf(osStdout, "\nAPP %s\n", app.Slug)
		renderDebugRegressionsTable(osStdout, api.DebugRegressionsResponse{
			Since: response.Since, Regressions: app.Regressions,
		})
	}
	if !printed && !partial {
		_, _ = fmt.Fprintln(osStdout, "No active debugger regressions.")
	}
	if partial {
		return 3
	}
	return 0
}
