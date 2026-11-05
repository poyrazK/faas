package mcpmanagement

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// A hosted connection acts in one organization (ADR-20261101225122). Its key
// reaches any app of a shared organization within the person's role there,
// but GET /v1/apps lists only the apps the person created, so a connection
// bound to a shared organization lists the organization's apps instead.

// listApps lists the apps a connection can use: its shared organization's
// (org is its slug), else the account's own.
func listApps(ctx context.Context, client *api.Client, org string) ([]api.AppResponse, error) {
	if org == "" {
		return client.ListApps(ctx)
	}
	out, err := client.ListOrgApps(ctx, org)
	if err != nil {
		return nil, err
	}
	apps := make([]api.AppResponse, 0, len(out.Apps))
	for _, app := range out.Apps {
		apps = append(apps, api.AppResponse{ID: app.ID, Slug: app.Slug, Type: app.Type, Runtime: app.Runtime, ProjectID: app.ProjectID})
	}
	return apps, nil
}

// keyCapabilities keeps the capabilities the connection's key carries. A
// shared organization's agent policy can leave a key fewer scopes than the
// client asked for; consent then offers only what the key can do. A key
// whose scopes apid doesn't report keeps the request.
func keyCapabilities(requested, keyScopes []string) []string {
	if keyScopes == nil {
		return requested
	}
	var out []string
	for _, capability := range requested {
		if slices.Contains(keyScopes, capability) {
			out = append(out, capability)
		}
	}
	return out
}

// grantScopes are the OAuth scopes a grant keeps: the requested ones, less
// writes when the key can't deploy.
func grantScopes(requested, keyScopes []string) []string {
	out := slices.Clone(requested)
	if keyScopes != nil && !slices.Contains(keyScopes, api.ScopeDeployWrite) && !slices.Contains(keyScopes, api.ScopeAdmin) {
		out = slices.DeleteFunc(out, func(scope string) bool { return scope == scopeWrite })
	}
	return out
}
