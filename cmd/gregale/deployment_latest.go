package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only inspection and waiting use this selector. Mutations keep requiring an
// exact ID or revision. Resolve once so a new upload cannot move a running wait.
func resolveDeploymentReadArg(ctx context.Context, client *api.Client, explicitApp, ref string) (string, error) {
	if ref != "latest" {
		return resolveDeploymentArg(ctx, client, explicitApp, ref)
	}
	app, err := resolveAppFlagOrContext(explicitApp)
	if err != nil || strings.TrimSpace(app) == "" {
		return "", fmt.Errorf("cannot resolve latest deployment: pass --app <slug>, or run inside a linked project (gregale link --app <slug>)")
	}
	if !api.ValidAppSlug(app) {
		return "", fmt.Errorf("latest deployment requires a valid app slug")
	}
	page, err := client.ListAppDeployments(ctx, app, "", 1)
	if err != nil {
		return "", fmt.Errorf("list latest deployment for %s: %w", app, err)
	}
	if len(page.Items) == 0 {
		return "", fmt.Errorf("no deployments found for app %q; deploy the app first", app)
	}
	d := page.Items[0]
	if !deploymentIDPattern.MatchString(d.ID) {
		return "", fmt.Errorf("latest deployment response has an invalid deployment ID")
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Selected latest deployment: app=%s; id=%s; revision=%s; scope=%s; status=%s.\n", compactInspectFailureText(app), d.ID, renderRevision(d.Revision), compactInspectFailureText(scopeOrDefault(d.Scope)), compactInspectFailureText(d.Status))
		_, _ = fmt.Fprintln(osStderr, "Latest means newest in app history across scopes, not necessarily the deployment serving traffic. This ID is pinned for this command.")
	}
	return d.ID, nil
}

func validDeploymentReadRef(ref string) bool {
	return ref == "latest" || validDeploymentRef(ref)
}
