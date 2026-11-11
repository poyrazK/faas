package githubd

import (
	"context"
	"io/fs"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// pushSiblingAddresses returns, by lower-cased service name, the Compose
// environment values of each service that call another service of the
// project, rewritten to that service's Gregale address
// (ADR-20261105104500). apid sets the ones an app has no value for before
// the build, as a reviewed deploy does; without them a service added by a
// push started with BILLING_URL unset while its pull request preview, which
// imports the PR's values, had worked. A push imports no other value. A
// Compose file that cannot be read, or an unknown plan, imports nothing.
func (s *Service) pushSiblingAddresses(ctx context.Context, fsys fs.FS, project state.Project, scan reposcan.Result) map[string]map[string]string {
	if len(scan.Workloads) < 2 || s.Reconcile == nil {
		return nil
	}
	opts := s.scanOptions(project.AccountID)
	env, err := reposcan.ReadProjectEnv(fsys, opts, scan.Workloads)
	if err != nil {
		return nil
	}
	acct, err := state.ProjectResourceAccount(ctx, s.Reconcile.Store, project)
	if err != nil {
		s.Log.Warn("githubd: sibling addresses: project owner", "project_id", project.ID, "err", err)
		return nil
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return nil
	}
	callable := make([]string, 0, len(scan.Workloads))
	for _, workload := range scan.Workloads {
		callable = append(callable, workload.Name)
	}
	out := map[string]map[string]string{}
	for _, workload := range scan.Workloads {
		if values := projectsetup.SiblingAddresses(env, workload.Name, callable, limits, !opts.HTTPSTransportUnavailable); len(values) > 0 {
			out[strings.ToLower(workload.Name)] = values
		}
	}
	return out
}

// projectSiblingAddresses is one app's share of pushSiblingAddresses; an
// app-bound app has no project siblings.
func projectSiblingAddresses(all map[string]map[string]string, app state.App, bound bool) map[string]string {
	if bound {
		return nil
	}
	return all[strings.ToLower(app.WorkloadName)]
}
