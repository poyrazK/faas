package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// trafficRollback is what `release traffic rollback` does for one app.
type trafficRollback struct {
	// abort is set while a canary rollout runs: the rollout's own abort
	// returns its traffic to the release it replaced.
	abort bool
	// target takes 100% of the traffic of a manual split.
	target api.DeploymentResponse
}

// planTrafficRollback undoes a traffic split. A running canary is aborted.
// Otherwise every live release with traffic is a candidate, and the one that
// served before the split is the oldest revision among them; `to` (an ID or
// vN) picks another. One release already serving everything is not a split:
// `gregale rollback` goes back to an earlier release.
func planTrafficRollback(live []api.DeploymentResponse, to string) (trafficRollback, error) {
	for _, d := range live {
		if d.CanaryTotalSteps > 0 && (d.RolloutState == "pending" || d.RolloutState == "rolling_out") {
			if to != "" {
				return trafficRollback{}, fmt.Errorf("a canary of %s is rolling out; its abort returns traffic to the release it replaced, so --to does not apply", deploymentLabel(d))
			}
			return trafficRollback{abort: true}, nil
		}
	}
	var serving []api.DeploymentResponse
	scopes := map[string]bool{}
	for _, d := range live {
		if d.TrafficPercent > 0 {
			serving = append(serving, d)
			scopes[trafficScope(d)] = true
		}
	}
	if to != "" {
		revision, isRevision := parseRevisionRef(to)
		for _, d := range live {
			if d.ID == to || isRevision && d.Revision == revision {
				return trafficRollback{target: d}, nil
			}
		}
		return trafficRollback{}, fmt.Errorf("%s is not a live release of this app; see gregale release traffic status", to)
	}
	if len(scopes) > 1 {
		return trafficRollback{}, errors.New("traffic is split in more than one environment; name the release to keep with --to")
	}
	if len(serving) < 2 {
		return trafficRollback{}, errors.New("traffic is not split; to go back to an earlier release run gregale rollback <app>")
	}
	oldest := serving[0]
	for _, d := range serving {
		if d.Revision == 0 {
			return trafficRollback{}, errors.New("a serving release has no revision number; name the release to keep with --to")
		}
		if d.Revision < oldest.Revision {
			oldest = d
		}
	}
	return trafficRollback{target: oldest}, nil
}

// cmdTrafficRollback implements `gregale release traffic rollback`.
func cmdTrafficRollback(args []string) int {
	const usage = "usage: gregale release traffic rollback <slug> [--to <id|vN>] [--reason TEXT]"
	fs := newFlagSet("traffic rollback", flag.ContinueOnError)
	app := fs.String("app", "", appSlugFlagUsage)
	to := fs.String("to", "", "release to give all traffic (default: the oldest release in the split)")
	reason := fs.String("reason", "", "reason recorded when a canary rollout is aborted")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	positional, err := mergeAppFlag(positional, strings.TrimSpace(*app), 1)
	if err != nil {
		return printErr("Invalid arguments", err)
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		PrintUsage(os.Stderr, usage, "traffic")
		return 1
	}
	slug := strings.TrimSpace(positional[0])
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	deployments, err := client.ListAppDeploymentsAll(ctx, slug)
	if err != nil {
		return printErr("Traffic rollback failed", err)
	}
	live := make([]api.DeploymentResponse, 0, len(deployments))
	for _, d := range deployments {
		if d.Status == statusLive {
			live = append(live, d)
		}
	}
	plan, err := planTrafficRollback(live, strings.TrimSpace(*to))
	if err != nil {
		return printErr("Traffic rollback refused", err)
	}
	if plan.abort {
		why := strings.TrimSpace(*reason)
		if why == "" {
			why = "traffic rollback"
		}
		resp, err := client.RecoverRollout(ctx, slug, "abort", why)
		if err != nil {
			return printErr("Traffic rollback failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		_, _ = fmt.Fprintf(osStdout, "Aborted the canary rollout of %s; its traffic returns to the release it replaced.\n", slug)
		return 0
	}
	updated, err := client.PatchDeploymentsIdTraffic(ctx, plan.target.ID, 100)
	if err != nil {
		return printErr("Traffic rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	_, _ = fmt.Fprintf(osStdout, "All traffic of %s is on %s again.\n", slug, deploymentLabel(updated))
	return 0
}
