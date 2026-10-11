package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const rolloutsStatusUsage = "usage: gregale rollouts status <slug> --deployment ID|vN [--wait] [--timeout 10m] [--poll-interval 2s]"

type rolloutStatusClient interface {
	GetDeployment(context.Context, string) (api.DeploymentResponse, error)
}

func pollRolloutStatus(ctx context.Context, client rolloutStatusClient, appID, deploymentID string, wait bool, interval time.Duration) (api.DeploymentResponse, error) {
	var last api.DeploymentResponse
	for {
		d, err := client.GetDeployment(ctx, deploymentID)
		if err != nil {
			return last, err
		}
		if !sameBindingDeployment(d.ID, deploymentID) || d.AppID != appID {
			return last, fmt.Errorf("server returned a different deployment or app")
		}
		last = d
		if !wait || rolloutStatusComplete(d) {
			return last, nil
		}
		if d.Status == "failed" || d.Status == "superseded" || d.RolloutState == "complete" || d.RolloutState == "aborted" {
			return last, fmt.Errorf("deployment became terminal before its handoff completed")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func rolloutStatusComplete(d api.DeploymentResponse) bool {
	if d.RolloutState != "complete" && d.RolloutState != "aborted" {
		return false
	}
	h := d.ServiceRolloutHandoff
	return h == nil || h.Phase == "complete" && (h.Action == "promote" && d.RolloutState == "complete" || h.Action == "abort" && d.RolloutState == "aborted")
}

func cmdRolloutsStatus(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, rolloutsStatusUsage, "rollouts")
		return 0
	}
	fs := newFlagSet("rollouts status", flag.ContinueOnError)
	selection := fs.String("deployment", "", "exact deployment ID or vN")
	wait := fs.Bool("wait", false, "wait for the selected rollout to finish")
	timeout := fs.Duration("timeout", 10*time.Minute, "wait deadline")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval")
	slug := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}
	if fs.Parse(args) != nil {
		return 1
	}
	if slug == "" && fs.NArg() == 1 {
		slug = fs.Arg(0)
	} else if fs.NArg() != 0 {
		PrintUsage(os.Stderr, rolloutsStatusUsage, "rollouts")
		return 1
	}
	if slug == "" || *selection == "" || *timeout <= 0 || *interval <= 0 {
		PrintUsage(os.Stderr, rolloutsStatusUsage, "rollouts")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		if signalCtx.Err() != nil {
			return 130
		}
		return printErr("Read app failed", err)
	}
	deploymentID, err := resolveBindingDeployment(ctx, client, slug, *selection)
	if err != nil {
		if signalCtx.Err() != nil {
			return 130
		}
		return printErr("Resolve deployment failed", err)
	}
	d, err := pollRolloutStatus(ctx, client, app.ID, deploymentID, *wait, *interval)
	if signalCtx.Err() != nil {
		return 130
	}
	if d.ID != "" {
		if jsonOutput {
			if code := jsonOut(writeJSON(d)); code != 0 {
				return code
			}
		} else {
			printRolloutStatus(d)
		}
	}
	if err != nil {
		return printErr("Rollout wait failed", err)
	}
	return 0
}

func printRolloutStatus(d api.DeploymentResponse) {
	_, _ = fmt.Fprintf(osStdout, "Deployment %s: %s (%d%% traffic)\n", d.ID, d.RolloutState, d.TrafficPercent)
	if h := d.ServiceRolloutHandoff; h != nil {
		_, _ = fmt.Fprintf(osStdout, "  handoff: %s / %s\n  predecessor: %s\n", h.Action, h.Phase, h.PredecessorDeploymentID)
		if h.LastError != "" {
			_, _ = fmt.Fprintf(osStdout, "  blocked: %s\n", h.LastError)
		}
		if len(h.MissingGateways) > 0 {
			_, _ = fmt.Fprintf(osStdout, "  missing gateways: %s\n", strings.Join(h.MissingGateways, ", "))
		}
		if gate := h.BindingsCheck; gate != nil {
			_, _ = fmt.Fprintf(osStdout, "  bindings: %s for %s\n", gate.Status, gate.DeploymentID)
			for _, blocker := range gate.Blockers {
				_, _ = fmt.Fprintf(osStdout, "    %s: %s\n", blocker.Code, blocker.Message)
			}
			if gate.AuditID != "" {
				_, _ = fmt.Fprintf(osStdout, "  routing audit: %s\n", gate.AuditID)
			}
		}
	}
}
