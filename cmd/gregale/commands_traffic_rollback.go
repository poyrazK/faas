package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
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

// planTrafficRollback undoes a traffic split in one environment: env, or the
// only one with live releases.
//   - A canary rolling out there is aborted; its rollout returns traffic to
//     the release it replaced.
//   - Otherwise the release with the largest share of a manual split gets
//     100%: in a split made by moving some traffic to another release, that
//     is the one that was serving. A tie needs `to`.
//   - `to` (an exact ID first, then a vN revision) names the release instead.
//
// One release already serving everything is not a split: `gregale rollback`
// goes back to an earlier release.
func planTrafficRollback(live []api.DeploymentResponse, env, to string) (trafficRollback, error) {
	if env == "" {
		scopes := map[string]bool{}
		for _, d := range live {
			scopes[trafficScope(d)] = true
		}
		if len(scopes) > 1 {
			return trafficRollback{}, errors.New("the app is live in more than one environment; name one with --env")
		}
		for scope := range scopes {
			env = scope
		}
	}
	var inScope []api.DeploymentResponse
	canaryScopes := map[string]bool{}
	var canary *api.DeploymentResponse
	for i, d := range live {
		rolling := d.CanaryTotalSteps > 0 && (d.RolloutState == "pending" || d.RolloutState == "rolling_out")
		if rolling {
			canaryScopes[trafficScope(d)] = true
		}
		if trafficScope(d) != env {
			continue
		}
		inScope = append(inScope, d)
		if rolling {
			canary = &live[i]
		}
	}
	if canary != nil {
		if to != "" {
			return trafficRollback{}, fmt.Errorf("a canary of %s is rolling out; its abort returns traffic to the release it replaced, so --to does not apply", deploymentLabel(*canary))
		}
		// The rollout abort is per app, so it must not reach another
		// environment's canary.
		if len(canaryScopes) > 1 {
			return trafficRollback{}, errors.New("canaries are rolling out in more than one environment; abort the one you mean with gregale rollouts recover")
		}
		return trafficRollback{abort: true}, nil
	}
	if to != "" {
		target, ok := liveReleaseByRef(inScope, to)
		if !ok {
			return trafficRollback{}, fmt.Errorf("%s is not a live release of this app in %s; see gregale release traffic status", to, env)
		}
		return trafficRollback{target: target}, nil
	}
	var serving []api.DeploymentResponse
	for _, d := range inScope {
		if d.TrafficPercent > 0 {
			serving = append(serving, d)
		}
	}
	if len(serving) < 2 {
		return trafficRollback{}, errors.New("traffic is not split; to go back to an earlier release run gregale rollback <app>")
	}
	largest, tie := serving[0], false
	for _, d := range serving[1:] {
		switch {
		case d.TrafficPercent > largest.TrafficPercent:
			largest, tie = d, false
		case d.TrafficPercent == largest.TrafficPercent:
			tie = true
		}
	}
	if tie {
		return trafficRollback{}, errors.New("two releases share the largest part of the traffic; name the release to keep with --to")
	}
	return trafficRollback{target: largest}, nil
}

// liveReleaseByRef finds ref among live releases: an exact ID wins, so an
// all-digit ID is never read as a revision; otherwise a vN revision.
func liveReleaseByRef(live []api.DeploymentResponse, ref string) (api.DeploymentResponse, bool) {
	for _, d := range live {
		if d.ID == ref {
			return d, true
		}
	}
	if revision, ok := parseRevisionRef(ref); ok {
		for _, d := range live {
			if d.Revision == revision {
				return d, true
			}
		}
	}
	return api.DeploymentResponse{}, false
}

// cmdTrafficRollback implements `gregale release traffic rollback`.
func cmdTrafficRollback(args []string) int {
	const usage = "usage: gregale release traffic rollback <slug> [--env SCOPE] [--to <id|vN>] [--reason TEXT]"
	fs := newFlagSet("traffic rollback", flag.ContinueOnError)
	app := fs.String("app", "", appSlugFlagUsage)
	to := fs.String("to", "", "release to give all traffic (default: the release with the largest share)")
	env := fs.String("env", "", "environment whose split to undo (default: the only one the app is live in)")
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
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runTrafficRollback(context.Background(), client, strings.TrimSpace(positional[0]), strings.TrimSpace(*env), strings.TrimSpace(*to), strings.TrimSpace(*reason))
}

func runTrafficRollback(ctx context.Context, client *api.Client, slug, env, to, reason string) int {
	live, err := liveDeployments(ctx, client, slug)
	if err != nil {
		return printErr("Traffic rollback failed", err)
	}
	plan, err := planTrafficRollback(live, env, to)
	if err != nil {
		return printErr("Traffic rollback refused", err)
	}
	if plan.abort {
		return abortRolloutForTrafficRollback(ctx, client, slug, live, reason)
	}
	// apid refuses the write if the split it was planned from changed in
	// between (expected_traffic).
	updated, err := client.PatchDeploymentTrafficIfSplit(ctx, plan.target.ID, 100, splitWeights(live, trafficScope(plan.target)))
	if unknownExpectedTrafficField(err) {
		// An apid released before expected_traffic: fall back to reading
		// the split again just before an unconditional write.
		if err = refuseChangedTraffic(ctx, client, slug, live); err == nil {
			updated, err = client.PatchDeploymentsIdTraffic(ctx, plan.target.ID, 100)
		}
	}
	if err != nil {
		return printErr("Traffic rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	_, _ = fmt.Fprintf(osStdout, "All traffic of %s is on %s again.\n", slug, deploymentLabel(updated))
	return 0
}

// abortRolloutForTrafficRollback aborts the canary a rollback found rolling
// out, if the rollout state is still what the plan was made from.
func abortRolloutForTrafficRollback(ctx context.Context, client *api.Client, slug string, live []api.DeploymentResponse, reason string) int {
	if err := refuseChangedTraffic(ctx, client, slug, live); err != nil {
		return printErr("Traffic rollback refused", err)
	}
	if reason == "" {
		reason = "traffic rollback"
	}
	resp, err := client.RecoverRollout(ctx, slug, "abort", reason)
	if err != nil {
		return printErr("Traffic rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "Aborted the canary rollout of %s; its traffic returns to the release it replaced.\n", slug)
	return 0
}

// refuseChangedTraffic reads the live releases again and refuses when their
// weights or rollout states moved since planned.
func refuseChangedTraffic(ctx context.Context, client *api.Client, slug string, planned []api.DeploymentResponse) error {
	again, err := liveDeployments(ctx, client, slug)
	if err != nil {
		return err
	}
	if !sameTrafficWeights(planned, again) {
		return errors.New("traffic changed while planning the rollback; check gregale release traffic status and run it again")
	}
	return nil
}

// splitWeights is the expected_traffic fence: every live release in scope
// with positive traffic and its percent.
func splitWeights(live []api.DeploymentResponse, scope string) map[string]int {
	out := map[string]int{}
	for _, d := range live {
		if trafficScope(d) == scope && d.TrafficPercent > 0 {
			out[d.ID] = d.TrafficPercent
		}
	}
	return out
}

func unknownExpectedTrafficField(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusBadRequest && strings.Contains(apiErr.Problem.Detail, `unknown field "expected_traffic"`)
}

func liveDeployments(ctx context.Context, client *api.Client, slug string) ([]api.DeploymentResponse, error) {
	deployments, err := client.ListAppDeploymentsAll(ctx, slug)
	if err != nil {
		return nil, err
	}
	live := make([]api.DeploymentResponse, 0, len(deployments))
	for _, d := range deployments {
		if d.Status == statusLive {
			live = append(live, d)
		}
	}
	return live, nil
}

// sameTrafficWeights reports whether two reads show the same live releases
// with the same weights and rollout states.
func sameTrafficWeights(a, b []api.DeploymentResponse) bool {
	type weight struct {
		percent int
		state   string
		step    int
	}
	seen := func(list []api.DeploymentResponse) map[string]weight {
		out := make(map[string]weight, len(list))
		for _, d := range list {
			out[d.ID] = weight{d.TrafficPercent, d.RolloutState, d.CanaryStep}
		}
		return out
	}
	x, y := seen(a), seen(b)
	if len(x) != len(y) {
		return false
	}
	for id, w := range x {
		if y[id] != w {
			return false
		}
	}
	return true
}
