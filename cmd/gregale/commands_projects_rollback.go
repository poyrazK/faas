// adr: 20261103172200
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const projectsRollbackUsage = "usage: gregale project rollback <slug> (--dry-run | --yes | --resume <operation-id>) [--all] [--acknowledge-releases] [--timeout 10m]"

func cmdProjectsRollback(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes", "all", "acknowledge-releases")
	fs := newFlagSet("projects-rollback", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview the exact rollback")
	yes := fs.Bool("yes", false, "start a project rollback")
	resume := fs.String("resume", "", "resume a saved project rollback operation")
	all := fs.Bool("all", false, "select each serving service and its current predecessor")
	ack := fs.Bool("acknowledge-releases", false, "accept rolling back across release commands")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait per service")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 ||
		(*resume == "" && *dryRun == *yes) || (*resume != "" && (*dryRun || *yes || *all || *ack)) {
		PrintUsage(os.Stderr, projectsRollbackUsage, "projects")
		return 1
	}
	slug := positional[0]
	if !api.ValidProjectSlug(slug) || *timeout <= 0 {
		return printErr("Invalid project rollback", errors.New("pass a valid project slug and a positive timeout"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *resume != "" {
		return resumeProjectRollback(ctx, client, slug, *resume, *timeout)
	}

	plan, err := planProjectRollback(ctx, client, slug, *all, *ack)
	if err != nil {
		return printErr("Could not plan project rollback", err)
	}
	if *dryRun {
		return renderProjectRollback(plan, true, nil)
	}
	if !slices.ContainsFunc(plan.Steps, func(step projectRollbackStep) bool { return step.Status == "planned" }) {
		return renderProjectRollback(plan, false, nil)
	}
	plan.ID = uuid.NewString()
	path, err := projectRollbackSessionPath(client, plan.ID)
	if err != nil {
		return printErr("Could not save project rollback", err)
	}
	lock, err := lockDeploySession(path)
	if err != nil {
		return printErr("Could not lock project rollback", err)
	}
	defer func() { _ = lock.Close() }()
	if err := writePrivateDeployState(path, plan); err != nil {
		return printErr("Could not save project rollback", err)
	}
	progress := osStdout
	if jsonOutput {
		progress = osStderr
	}
	PrintProgress(progress, "Saved rollback %s. Resume with: %s", plan.ID, plan.resumeCommand())
	return executeProjectRollback(ctx, client, &plan, path, *timeout, false)
}

func planProjectRollback(ctx context.Context, client *api.Client, slug string, all, ack bool) (projectRollbackSession, error) {
	var project api.ProjectResponse
	var err error
	if all {
		project, err = client.GetProject(ctx, slug)
	} else {
		project, err = client.GetProjectWithRollback(ctx, slug)
	}
	plan := projectRollbackSession{Version: 1, Project: slug, ProjectID: project.ID, AcknowledgeReleases: ack}
	if err != nil {
		return plan, err
	}
	if project.ID == "" || project.Slug != slug {
		return plan, errors.New("project response is missing its identity")
	}
	members := make(map[string]api.ProjectRollbackMember)
	if !all {
		if project.RollbackBatch == nil || project.RollbackBatch.ID == "" || len(project.RollbackBatch.Members) == 0 {
			return plan, errors.New("no serving deploy has recorded project membership. Select individual app revisions or explicitly use --all")
		}
		plan.DeployGroupID = project.RollbackBatch.ID
		for _, member := range project.RollbackBatch.Members {
			if member.Slug == "" || members[member.Slug].DeploymentID != "" {
				return plan, errors.New("the deploy contains a detached service or multiple revisions of one service")
			}
			members[member.Slug] = member
		}
	}
	for _, workload := range projectRollbackOrder(project.Workloads) {
		step := projectRollbackStep{Service: workload.WorkloadName, Slug: workload.Slug, Status: "planned"}
		member, selected := members[workload.Slug]
		if !selected && !all {
			step.Status, step.Reason = "skipped", "outside the recorded deploy"
		} else {
			delete(members, workload.Slug)
			if err := prepareProjectRollbackStep(ctx, client, &step, member, all); err != nil {
				return plan, fmt.Errorf("%s: %w", workload.Slug, err)
			}
		}
		plan.Steps = append(plan.Steps, step)
	}
	if len(members) != 0 {
		return plan, errors.New("the recorded deploy includes a service that no longer belongs to the project")
	}
	if len(plan.Steps) == 0 {
		return plan, errors.New("the project has no services")
	}
	return plan, nil
}

func prepareProjectRollbackStep(ctx context.Context, client *api.Client, step *projectRollbackStep, member api.ProjectRollbackMember, all bool) error {
	if !all {
		switch member.Status {
		case "failed", "cancelled", "superseded":
			if !member.PreviouslyServed {
				step.Status, step.Reason = "skipped", "this revision never served"
				return nil
			}
		case "live":
		default:
			return fmt.Errorf("recorded revision v%d is still %s", member.Revision, member.Status)
		}
	}

	current, err := readProjectRollbackCurrent(ctx, client, step.Slug)
	if err != nil {
		return err
	}
	step.From, step.CurrentID, step.AppID, step.Scope = current.Revision, current.ID, current.AppID, scopeOrDefault(current.Scope)
	step.CurrentServedAt = current.RolloutCompletedAt
	if !all {
		switch {
		case sameBindingDeployment(current.ID, member.PredecessorID):
			step.Status, step.Reason = "skipped", "recorded predecessor already serves"
			return nil
		case !sameBindingDeployment(current.ID, member.DeploymentID):
			return fmt.Errorf("serving revision changed since the recorded deploy (expected v%d)", member.Revision)
		case member.PredecessorID == "" && member.Revision == 1:
			step.Status, step.Reason = "skipped", "first release has no predecessor"
			return nil
		case member.PredecessorID == "":
			return fmt.Errorf("v%d has no recorded serving predecessor", member.Revision)
		}
	} else if current.ID == "" {
		step.Status, step.Reason = "skipped", "no serving release"
		return nil
	}

	summary, err := client.GetAppDeploymentSummary(ctx, step.Slug, current.ID)
	if err != nil {
		return err
	}
	if !all && !sameBindingDeployment(summary.RollbackTargetID, member.PredecessorID) {
		return fmt.Errorf("recorded predecessor v%d is no longer the serving revision's rollback target", member.PredecessorRevision)
	}
	step.TargetID, step.To = summary.RollbackTargetID, summary.RollbackTargetRevision
	switch {
	case step.TargetID == "":
		step.Status, step.Reason = "skipped", "no rollback target"
	case all && step.To > step.From:
		step.Status, step.Reason = "skipped", "serving a revision older than its current rollback target"
	case step.AppID == "":
		return errors.New("serving revision response is missing the app identity")
	}
	return nil
}

func readProjectRollbackCurrent(ctx context.Context, client *api.Client, slug string) (api.DeploymentResponse, error) {
	page, err := client.ListAppDeployments(ctx, slug, "", 200)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	var current api.DeploymentResponse
	for _, dep := range page.Items {
		scope := scopeOrDefault(dep.Scope)
		if (scope != api.DefaultEnvScope && scope != "production") || dep.Status != "live" || dep.TrafficPercent == 0 {
			continue
		}
		if current.ID != "" || dep.TrafficPercent != 100 || dep.RolloutState != "complete" {
			return api.DeploymentResponse{}, errors.New("production does not have a single completed serving revision")
		}
		current = dep
	}
	return current, nil
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
