// adr: 20261103172200
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const projectsRollbackUsage = "usage: gregale projects rollback <slug> (--dry-run | --yes) [--acknowledge-releases] [--timeout 10m]"

// projectRollbackStep is one service's rollback in a project rollback.
type projectRollbackStep struct {
	Service      string `json:"service"`
	Slug         string `json:"slug"`
	Status       string `json:"status"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

// cmdProjectsRollback rolls every service of a project back to its previous
// live deployment, one at a time, in dependency order: a service rolls back
// after the services it depends on, as a project deploy brings them up
// (ADR-20261103172200). Each step is the per-app rollback (POST
// /v1/apps/{slug}/rollback); the command stops at the first failure and names
// the services left on their current release.
func cmdProjectsRollback(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes", "acknowledge-releases")
	fs := newFlagSet("projects-rollback", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the rollback order without changing anything")
	yes := fs.Bool("yes", false, "roll every service back")
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
	project, err := client.GetProject(context.Background(), positional[0])
	if err != nil {
		return printErr("Could not read project", err)
	}
	order := projectRollbackOrder(project.Workloads)
	if len(order) == 0 {
		return printErr("Nothing to roll back", fmt.Errorf("project %s has no services", positional[0]))
	}
	if *dryRun {
		return renderProjectRollbackPlan(positional[0], order)
	}
	steps := make([]projectRollbackStep, 0, len(order))
	for i, workload := range order {
		step := projectRollbackStep{Service: workload.WorkloadName, Slug: workload.Slug}
		dep, err := client.RollbackWithRequest(context.Background(), workload.Slug, api.RollbackRequest{AcknowledgeReleases: *acknowledge})
		if err == nil && !jsonOutput && dep.RollbackOperation != nil && dep.RollbackOperation.ID != "" {
			PrintProgress(osStdout, "%s (%s):", workload.WorkloadName, workload.Slug)
			if code := waitPlainRollback(client, workload.Slug, *dep.RollbackOperation, *timeout, 2*time.Second); code != 0 {
				err = fmt.Errorf("its rollback did not finish serving the previous release")
			}
		}
		if err != nil {
			step.Status, step.Error = "failed", err.Error()
			steps = append(steps, step)
			for _, rest := range order[i+1:] {
				steps = append(steps, projectRollbackStep{Service: rest.WorkloadName, Slug: rest.Slug, Status: "not_started"})
			}
			return projectRollbackStopped(positional[0], steps, workload, err)
		}
		step.Status, step.DeploymentID = "rolled_back", dep.ID
		steps = append(steps, step)
		if !jsonOutput && (dep.RollbackOperation == nil || dep.RollbackOperation.ID == "") {
			PrintOK(osStdout, "%s (%s): rolled back to %s (%s)", workload.WorkloadName, workload.Slug, dep.ID, dep.Status)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"project": positional[0], "steps": steps}))
	}
	PrintOK(osStdout, "Project %s rolled back: %d services", positional[0], len(steps))
	return 0
}

func renderProjectRollbackPlan(project string, order []api.ProjectWorkloadResponse) int {
	if jsonOutput {
		steps := make([]projectRollbackStep, 0, len(order))
		for _, workload := range order {
			steps = append(steps, projectRollbackStep{Service: workload.WorkloadName, Slug: workload.Slug, Status: "planned"})
		}
		return jsonOut(writeJSON(map[string]any{"project": project, "dry_run": true, "steps": steps}))
	}
	_, _ = fmt.Fprintf(osStdout, "Would roll back %d services of %s to their previous live deployment, in this order:\n", len(order), project)
	for i, workload := range order {
		after := ""
		if len(workload.DependsOn) > 0 {
			after = " (after " + strings.Join(workload.DependsOn, ", ") + ")"
		}
		_, _ = fmt.Fprintf(osStdout, "  %d. %s (%s)%s\n", i+1, workload.WorkloadName, workload.Slug, after)
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
	return printErr("Project rollback stopped", fmt.Errorf("%s. Fix it and roll the rest back with gregale rollback <slug>", detail))
}

// projectRollbackOrder sorts a project's services so each comes after the
// services it depends on. Names tie-break, and a dependency cycle (which a
// project deploy refuses) falls back to name order for the services in it.
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
	ready := func(workload api.ProjectWorkloadResponse) bool {
		for _, dependency := range workload.DependsOn {
			for j, other := range live {
				if !placed[j] && api.SameServiceName(other.WorkloadName, dependency) {
					return false
				}
			}
		}
		return true
	}
	for len(out) < len(live) {
		progressed := false
		for i, workload := range live {
			if !placed[i] && ready(workload) {
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
