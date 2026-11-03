package main

// Legacy deploy compatibility helpers are exercised by tests only.
import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"time"
)

// cliScalingPolicyPatch builds a complete replacement policy for the public
// PATCH shape. The API intentionally treats scaling_policy as a replacement,
// so the CLI must read and preserve existing fields before changing only the
// concurrency admission knobs.
func cliScalingPolicyPatch(ctx context.Context, client interface {
	GetApp(context.Context, string) (api.AppResponse, error)
}, slug, overflow string, maxQueueWaitMS int, setOverflow, setMaxQueueWait bool) (*api.ScalingPolicy, error) {
	return cliScalingPolicyPatchWithWake(ctx, client, slug, overflow, maxQueueWaitMS, setOverflow, setMaxQueueWait, 0, 0, false, false)
}

func cliScalingPolicyPatchWithWake(ctx context.Context, client interface {
	GetApp(context.Context, string) (api.AppResponse, error)
}, slug, overflow string, maxQueueWaitMS int, setOverflow, setMaxQueueWait bool, wakeMaxQueueDepth, wakeMaxQueueWaitSeconds int, setWakeDepth, setWakeWait bool) (*api.ScalingPolicy, error) {
	return cliScalingPolicyPatchWithQueues(ctx, client, slug, overflow, maxQueueWaitMS, 0, setOverflow, setMaxQueueWait, false, wakeMaxQueueDepth, wakeMaxQueueWaitSeconds, setWakeDepth, setWakeWait)
}

// manifestCronTransaction records the inverse of each successful desired-state
// mutation. Definite rejection restores the previous cron set in reverse order;
// accepted and uncertain submissions retain the staged settings.

// deployManifestTriggers applies the manifest as desired state and commits the
// result immediately. The deployment path uses the transaction-returning
// helper below so a later upload/build-submission failure can restore the
// complete previous set.
func deployManifestTriggers(ctx context.Context, client manifestCronClient, slug, cwd string) error {
	txn, err := deployManifestTriggersWithRollback(ctx, client, slug, cwd)
	if err == nil {
		txn.commit()
	}
	return err
}

// deployManifestTriggersWithRollback reconciles creates, mutable scheduling
// options, and removals. Presence of a cron for slug opts that app into
// replacement semantics; an explicit `triggers: []` clears the target app.
// A non-empty manifest containing only other app slugs leaves this app alone.
// The stable identity is (schedule,path), so changing either is a remove+add.
func deployManifestTriggersWithRollback(ctx context.Context, client manifestCronClient, slug, cwd string) (*manifestCronTransaction, error) {
	txn := &manifestCronTransaction{}
	if cwd == "" {
		return txn, nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return txn, err
	}
	if !ok {
		return txn, nil
	}
	if err := m.Validate(); err != nil {
		return txn, err
	}
	return reconcileManifestCronsWithRollback(ctx, client, slug, m)
}

func pollBuildStatus(c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.BuildResponse, bool) {
	return pollBuildStatusContext(context.Background(), c, dep, deadline)
}

// terminalExitForBuild maps a polled terminal BuildResponse to a CLI
// exit code. Mirrors terminalExitForDeployment but reads from the
// build row (DEPLOY-PROV-6 / ADR-089, issue #741) — the polled
// build row lacks the rich Error string from the deployment row,
// so on failure we render a compact "BuildStatus=failed
// failure_class=…" block and exit 2 (same exit-code convention as
// terminalExitForDeployment's renderDeployFailure path).
func terminalExitForBuild(b api.BuildResponse, appSlug string) int {
	return terminalExitForBuildContext(context.Background(), nil, b, appSlug)
}

func terminalExitForBuildContext(ctx context.Context, c *Client, b api.BuildResponse, appSlug string) int {
	return terminalExitForBuildWithFailureContext(ctx, c, b, appSlug, nil)
}
