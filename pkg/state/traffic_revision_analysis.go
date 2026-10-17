// adr: 570
package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

func servingTrafficRevisionHosts(candidates []string) []string {
	var hosts []string
	for _, host := range candidates {
		revision, slug, ok := hostidentity.DeploymentScopeFromHost(hostidentity.DeployWildcardSuffix, host)
		if ok && hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, revision, slug) == host {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func (m *MemStore) appendMemTrafficRevisionHostsLocked(ctx context.Context, account string, change memTrafficPolicyChange, view *trafficHostAnalysis, otherInputs int) error {
	if change.GlobalRoutes || hostidentity.DeployWildcardSuffix == "" {
		return ctx.Err()
	}
	return visitMemTrafficRows(ctx, m.deployments, change.Deployments, func(deployment Deployment) error {
		if deployment.ID == "" || deployment.DeletedAt != nil || !deployment.DeploymentPreviewActive() {
			return nil
		}
		app, found := change.Apps[deployment.AppID]
		if !found {
			app, found = m.apps[deployment.AppID]
		}
		if !found || app.AccountID != account || app.Status == AppDeleted || app.DeletedAt != nil || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
			return nil
		}
		host := hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, deployment.Revision, app.Slug)
		if host == "" {
			return nil
		}
		view.RevisionHosts = append(view.RevisionHosts, host)
		return checkMemTrafficAnalysisInputs(otherInputs + len(view.RevisionHosts))
	})
}
