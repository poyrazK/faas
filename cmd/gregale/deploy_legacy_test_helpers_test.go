package main

// Legacy deploy compatibility helpers are exercised by tests only.
import (
	"context"
	"fmt"
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
	manage := m.Triggers != nil && len(m.Triggers) == 0
	var matching []gregalemanifest.Trigger
	for _, t := range m.Triggers {
		if t.Kind == gregalemanifest.TriggerKindCron && t.App == slug {
			manage = true
			matching = append(matching, t)
		}
	}
	if !manage {
		return txn, nil
	}

	existing, err := client.ListCrons(ctx, slug)
	if err != nil {
		return txn, fmt.Errorf("list existing crons: %w", err)
	}
	if acct, err := client.Whoami(ctx); err == nil {
		if l, ok := api.LimitsFor(api.Plan(acct.Plan)); ok {
			if len(matching) > l.CronLimitPerApp {
				return txn, fmt.Errorf("cron quota exceeded: %d triggers in manifest, plan allows %d; raise plan or drop triggers",
					len(matching), l.CronLimitPerApp)
			}
		}
	}

	desiredByKey := make(map[string]api.CreateCronRequest, len(matching))
	desiredOrder := make([]string, 0, len(matching))
	for _, trigger := range matching {
		key := manifestCronKey(trigger.Schedule, trigger.Path)
		desiredByKey[key] = manifestCronRequest(slug, trigger)
		desiredOrder = append(desiredOrder, key)
	}
	existingByKey := make(map[string]api.CronResponse, len(existing))
	for _, cron := range existing {
		if cron.Kind == "command" {
			continue // deployment manifests own HTTP path crons only
		}
		existingByKey[manifestCronKey(cron.Schedule, cron.Path)] = cron
	}

	fail := func(operation string, err error) (*manifestCronTransaction, error) {
		rollbackErr := txn.rollback(ctx)
		if rollbackErr != nil {
			return txn, fmt.Errorf("%s: %w; trigger rollback incomplete: %w", operation, err, rollbackErr)
		}
		return txn, fmt.Errorf("%s: %w; previous trigger state restored", operation, err)
	}

	applied := 0
	// Update same-identity rows first. These operations do not consume quota.
	for _, key := range desiredOrder {
		current, exists := existingByKey[key]
		if !exists {
			continue
		}
		desired := desiredByKey[key]
		if cronResponseMatchesRequest(current, desired) {
			continue
		}
		previous := cronCreateRequestFromResponse(current)
		if _, err := client.UpdateCron(ctx, current.ID, cronUpdateRequestFromCreate(desired)); err != nil {
			return fail("update manifest cron "+current.ID, err)
		}
		cronID := current.ID
		txn.steps = append(txn.steps, manifestCronRollbackStep{
			description: "restore trigger " + cronID,
			undo: func(rollbackCtx context.Context) error {
				_, err := client.UpdateCron(rollbackCtx, cronID, cronUpdateRequestFromCreate(previous))
				return err
			},
		})
		applied++
	}

	// Remove stale rows before creates so replacement at the exact cap has
	// headroom. Each delete records enough data to recreate the prior row.
	for _, cron := range existing {
		if cron.Kind == "command" {
			continue // command schedules are managed independently via `crons`
		}
		if _, keep := desiredByKey[manifestCronKey(cron.Schedule, cron.Path)]; keep {
			continue
		}
		if err := client.DeleteCron(ctx, cron.ID); err != nil {
			return fail("remove stale manifest cron "+cron.ID, err)
		}
		previous := cronCreateRequestFromResponse(cron)
		txn.steps = append(txn.steps, manifestCronRollbackStep{
			description: "recreate trigger " + cron.ID,
			undo: func(rollbackCtx context.Context) error {
				_, err := client.CreateCron(rollbackCtx, slug, previous)
				return err
			},
		})
		applied++
	}

	for i, key := range desiredOrder {
		if _, exists := existingByKey[key]; exists {
			continue
		}
		created, err := client.CreateCron(ctx, slug, desiredByKey[key])
		if err != nil {
			req := desiredByKey[key]
			return fail(fmt.Sprintf("trigger %d/%d (%s %q %s) rejected after %d trigger change(s)",
				i+1, len(desiredOrder), slug, req.Schedule, req.Path, applied), err)
		}
		createdID := created.ID
		if createdID != "" {
			txn.steps = append(txn.steps, manifestCronRollbackStep{
				description: "delete staged trigger " + createdID,
				undo:        func(rollbackCtx context.Context) error { return client.DeleteCron(rollbackCtx, createdID) },
			})
		}
		applied++
	}
	if !jsonOutput && applied > 0 {
		_, _ = fmt.Fprintf(osStdout, "  ✓ %s: %d trigger(s) applied\n", slug, applied)
	}
	return txn, nil
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
