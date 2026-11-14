// adr: 20261103172200
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const projectsRollbackUsage = "usage: gregale project rollback <slug> (--dry-run | --yes) [--all] [--acknowledge-releases] [--timeout 10m]"

// projectRollbackLatestWindow groups a project's releases into its latest
// deploy when they carry no commit: one project deploy creates its services'
// deployments within one request, a push within one delivery.
const projectRollbackLatestWindow = 15 * time.Minute

// projectRollbackStep is one service's rollback in a project rollback.
type projectRollbackStep struct {
	Service      string `json:"service"`
	Slug         string `json:"slug"`
	Status       string `json:"status"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Error        string `json:"error,omitempty"`
	// From and To are the revisions the step moves between; Reason says why a
	// service is skipped.
	From   int    `json:"from_revision,omitempty"`
	To     int    `json:"to_revision,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// projectRollbackService is one service with the production release it serves
// and the release a rollback would restore.
type projectRollbackService struct {
	workload api.ProjectWorkloadResponse
	current  api.DeploymentResponse
	targetID string
	target   int
	skip     string
}

// cmdProjectsRollback rolls a project's latest deploy back (ADR-20261103172200,
// amended by ADR-20261105104500). Only the services that deploy changed roll
// back, unless --all; a service added since has no earlier release and is
// left as it is; a service already on an older release than its target was
// rolled back before and is skipped, so a rerun after a partial failure
// finishes the rollback instead of undoing it. Callers roll back before the
// services they call, the reverse of a deploy. Each step is the per-app
// rollback (POST /v1/apps/{slug}/rollback) pinned to the planned current and
// target releases; the command stops at the first failure and names the
// services left on their current release.
func cmdProjectsRollback(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes", "all", "acknowledge-releases")
	fs := newFlagSet("projects-rollback", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the rollback plan without changing anything")
	yes := fs.Bool("yes", false, "roll the latest deploy back")
	all := fs.Bool("all", false, "roll back every service, not only those the latest deploy changed")
	acknowledge := fs.Bool("acknowledge-releases", false, "accept rolling back across release commands")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for each service to serve its previous release")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || *dryRun == *yes {
		PrintUsage(os.Stderr, projectsRollbackUsage, "projects")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) {
		return printErr("Invalid project slug", fmt.Errorf("%q does not match the project slug contract", positional[0]))
	}
	if *timeout <= 0 {
		return printErr("Invalid rollback", fmt.Errorf("--timeout must be positive; got %s", *timeout))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	project, err := client.GetProject(ctx, positional[0])
	if err != nil {
		return printErr("Could not read project", err)
	}
	order := projectRollbackOrder(project.Workloads)
	if len(order) == 0 {
		return printErr("Nothing to roll back", fmt.Errorf("project %s has no services", positional[0]))
	}
	services := make([]projectRollbackService, 0, len(order))
	for _, workload := range order {
		service, err := readProjectRollbackService(ctx, client, workload)
		if err != nil {
			return printErr("Could not read releases", fmt.Errorf("%s (%s): %w", workload.WorkloadName, workload.Slug, err))
		}
		services = append(services, service)
	}
	if !*all {
		markProjectRollbackUnchanged(services)
	}
	if *dryRun {
		return renderProjectRollbackPlan(positional[0], services)
	}
	steps := make([]projectRollbackStep, 0, len(services))
	rolled := 0
	for i, service := range services {
		workload := service.workload
		step := projectRollbackStep{Service: workload.WorkloadName, Slug: workload.Slug, From: service.current.Revision, To: service.target}
		if service.skip != "" {
			step.Status, step.Reason = "skipped", service.skip
			steps = append(steps, step)
			if !jsonOutput {
				PrintOK(osStdout, "%s (%s): left as is: %s", workload.WorkloadName, workload.Slug, service.skip)
			}
			continue
		}
		current, target := service.current.ID, service.targetID
		dep, err := client.RollbackWithRequest(ctx, workload.Slug, api.RollbackRequest{
			AcknowledgeReleases: *acknowledge, ExpectedCurrentDeploymentID: &current, TargetDeploymentID: &target})
		if err == nil && !jsonOutput && dep.RollbackOperation != nil && dep.RollbackOperation.ID != "" {
			PrintProgress(osStdout, "%s (%s):", workload.WorkloadName, workload.Slug)
			if code := waitPlainRollback(client, workload.Slug, *dep.RollbackOperation, *timeout, 2*time.Second); code != 0 {
				err = fmt.Errorf("its rollback did not finish serving the previous release")
			}
		}
		if err != nil {
			step.Status, step.Error = "failed", err.Error()
			steps = append(steps, step)
			for _, rest := range services[i+1:] {
				status := "not_started"
				if rest.skip != "" {
					status = "skipped"
				}
				steps = append(steps, projectRollbackStep{Service: rest.workload.WorkloadName, Slug: rest.workload.Slug, Status: status, Reason: rest.skip})
			}
			return projectRollbackStopped(positional[0], steps, workload, err)
		}
		rolled++
		step.Status, step.DeploymentID = "rolled_back", dep.ID
		steps = append(steps, step)
		if !jsonOutput && (dep.RollbackOperation == nil || dep.RollbackOperation.ID == "") {
			PrintOK(osStdout, "%s (%s): rolled back to %s (%s)", workload.WorkloadName, workload.Slug, dep.ID, dep.Status)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"project": positional[0], "steps": steps}))
	}
	if rolled == 0 {
		PrintOK(osStdout, "Project %s: nothing to roll back", positional[0])
		return 0
	}
	PrintOK(osStdout, "Project %s rolled back: %d services", positional[0], rolled)
	return 0
}

// readProjectRollbackService reads the production release a service serves
// and the release its rollback restores, and says why it is skipped when it
// has nothing to roll back to.
func readProjectRollbackService(ctx context.Context, client *api.Client, workload api.ProjectWorkloadResponse) (projectRollbackService, error) {
	service := projectRollbackService{workload: workload}
	page, err := client.ListAppDeployments(ctx, workload.Slug, "", 200)
	if err != nil {
		return service, err
	}
	for _, dep := range page.Items {
		if dep.Status == "live" && dep.TrafficPercent > 0 && (dep.Scope == "" || dep.Scope == api.DefaultEnvScope) &&
			dep.Revision >= service.current.Revision {
			service.current = dep
		}
	}
	if service.current.ID == "" {
		service.skip = "not serving a release"
		return service, nil
	}
	summary, err := client.GetAppDeploymentSummary(ctx, workload.Slug, service.current.ID)
	if err != nil {
		return service, err
	}
	service.targetID, service.target = summary.RollbackTargetID, summary.RollbackTargetRevision
	switch {
	case service.targetID == "":
		service.skip = "no earlier release (added since)"
	case service.target > 0 && service.current.Revision > 0 && service.target > service.current.Revision:
		// The target is newer than what serves: this service was rolled back
		// already, and rolling it "back" again would undo that.
		service.skip = fmt.Sprintf("already rolled back (serving v%d)", service.current.Revision)
	}
	return service, nil
}

// markProjectRollbackUnchanged skips the services the project's latest deploy
// did not change. That deploy is the newest release a service serves; a
// service belongs to it when it serves the same commit, or, for releases
// without a commit, one created within projectRollbackLatestWindow of it.
func markProjectRollbackUnchanged(services []projectRollbackService) {
	var newest api.DeploymentResponse
	var newestAt time.Time
	for _, service := range services {
		if service.skip != "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, service.current.CreatedAt); err == nil && at.After(newestAt) {
			newest, newestAt = service.current, at
		}
	}
	if newestAt.IsZero() {
		return
	}
	for i := range services {
		service := &services[i]
		if service.skip != "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, service.current.CreatedAt)
		if err != nil {
			continue
		}
		sameCommit := newest.CommitSHA != "" && service.current.CommitSHA == newest.CommitSHA
		if !sameCommit && newestAt.Sub(at) > projectRollbackLatestWindow {
			service.skip = fmt.Sprintf("not changed by the latest deploy (serving v%d from %s); --all includes it",
				service.current.Revision, at.UTC().Format("2006-01-02 15:04 MST"))
		}
	}
}

func renderProjectRollbackPlan(project string, services []projectRollbackService) int {
	if jsonOutput {
		steps := make([]projectRollbackStep, 0, len(services))
		for _, service := range services {
			step := projectRollbackStep{Service: service.workload.WorkloadName, Slug: service.workload.Slug, Status: "planned",
				From: service.current.Revision, To: service.target}
			if service.skip != "" {
				step.Status, step.Reason = "skipped", service.skip
			}
			steps = append(steps, step)
		}
		return jsonOut(writeJSON(map[string]any{"project": project, "dry_run": true, "steps": steps}))
	}
	planned := 0
	for _, service := range services {
		if service.skip == "" {
			planned++
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Would roll back %d services of %s, callers first:\n", planned, project)
	n := 0
	for _, service := range services {
		workload := service.workload
		if service.skip != "" {
			continue
		}
		n++
		before := ""
		if len(workload.DependsOn) > 0 {
			before = " (before " + strings.Join(workload.DependsOn, ", ") + ")"
		}
		to := service.targetID
		if service.target > 0 {
			to = fmt.Sprintf("v%d", service.target)
		}
		_, _ = fmt.Fprintf(osStdout, "  %d. %s (%s): v%d -> %s%s\n", n, workload.WorkloadName, workload.Slug, service.current.Revision, to, before)
	}
	for _, service := range services {
		if service.skip != "" {
			_, _ = fmt.Fprintf(osStdout, "  - %s (%s): left as is: %s\n", service.workload.WorkloadName, service.workload.Slug, service.skip)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Run again with --yes to roll back.\n")
	return 0
}

func projectRollbackStopped(project string, steps []projectRollbackStep, failed api.ProjectWorkloadResponse, err error) int {
	var done, left []string
	for _, step := range steps {
		switch step.Status {
		case "rolled_back":
			done = append(done, step.Slug)
		case "not_started":
			left = append(left, step.Slug)
		}
	}
	if jsonOutput {
		_ = jsonOut(writeJSON(map[string]any{"project": project, "steps": steps}))
	}
	detail := fmt.Sprintf("%s (%s): %v", failed.WorkloadName, failed.Slug, err)
	if len(done) > 0 {
		detail += "; already rolled back: " + strings.Join(done, ", ")
	}
	if len(left) > 0 {
		detail += "; still on their current release: " + strings.Join(left, ", ")
	}
	return printErr("Project rollback stopped", fmt.Errorf("%s. Fix it and run gregale project rollback %s --yes again; services already rolled back are skipped", detail, project))
}

// projectRollbackOrder sorts a project's services so each rolls back before
// the services it depends on: callers return to the release that called the
// older API before that API goes back, the reverse of a deploy. Names
// tie-break, and a dependency cycle (which a project deploy refuses) falls
// back to name order for the services in it.
func projectRollbackOrder(workloads []api.ProjectWorkloadResponse) []api.ProjectWorkloadResponse {
	var live []api.ProjectWorkloadResponse
	for _, workload := range workloads {
		if workload.Slug != "" && workload.Status != "deleted" {
			live = append(live, workload)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].WorkloadName < live[j].WorkloadName })
	placed := make([]bool, len(live))
	out := make([]api.ProjectWorkloadResponse, 0, len(live))
	// ready: no unplaced service still depends on this one.
	ready := func(index int) bool {
		for j, other := range live {
			if placed[j] || j == index {
				continue
			}
			if slices.ContainsFunc(other.DependsOn, func(dependency string) bool {
				return api.SameServiceName(live[index].WorkloadName, dependency)
			}) {
				return false
			}
		}
		return true
	}
	for len(out) < len(live) {
		progressed := false
		for i, workload := range live {
			if !placed[i] && ready(i) {
				placed[i], progressed = true, true
				out = append(out, workload)
			}
		}
		if !progressed {
			for i, workload := range live {
				if !placed[i] {
					placed[i] = true
					out = append(out, workload)
				}
			}
		}
	}
	return out
}
