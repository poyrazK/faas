package main

// Legacy deploy compatibility helpers are exercised by tests only.
import (
	"context"
	"errors"
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

type manifestCronRollbackStep struct {
	description string
	undo        func(context.Context) error
}

// manifestCronTransaction records the inverse of each successful desired-state
// mutation. Definite rejection restores the previous cron set in reverse order;
// accepted and uncertain submissions retain the staged settings.
type manifestCronTransaction struct {
	steps []manifestCronRollbackStep
}

func (t *manifestCronTransaction) commit() {
	if t != nil {
		t.steps = nil
	}
}

func (t *manifestCronTransaction) rollback(ctx context.Context) error {
	if t == nil || len(t.steps) == 0 {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), manifestTriggerCleanupTimeout)
	defer cancel()
	var cleanupErrs []error
	for i := len(t.steps) - 1; i >= 0; i-- {
		step := t.steps[i]
		if err := step.undo(cleanupCtx); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("%s: %w", step.description, err))
		}
	}
	t.steps = nil
	return errors.Join(cleanupErrs...)
}

func cronCreateRequestFromResponse(cron api.CronResponse) api.CreateCronRequest {
	enabled, skip := cron.Enabled, cron.SkipIfRunning
	return api.CreateCronRequest{
		AppID: cron.AppID, Schedule: cron.Schedule, Path: cron.Path,
		Command: append([]string(nil), cron.Command...), CommandShell: cron.CommandShell,
		TimeoutSeconds: cron.TimeoutSeconds, MaxOutputBytes: cron.MaxOutputBytes,
		Enabled: &enabled, Timezone: cron.Timezone, SkipIfRunning: &skip,
	}
}

func cronUpdateRequestFromCreate(req api.CreateCronRequest) api.UpdateCronRequest {
	schedule, path, timezone := req.Schedule, req.Path, req.Timezone
	return api.UpdateCronRequest{
		Schedule: &schedule, Path: &path, Enabled: req.Enabled,
		Timezone: &timezone, SkipIfRunning: req.SkipIfRunning,
	}
}

func cronResponseMatchesRequest(cron api.CronResponse, req api.CreateCronRequest) bool {
	enabled, skip := true, false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.SkipIfRunning != nil {
		skip = *req.SkipIfRunning
	}
	timezone := req.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return cron.Enabled == enabled && cron.Timezone == timezone && cron.SkipIfRunning == skip
}

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

func scalingPolicyEqual(a, b *api.ScalingPolicy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.MinInstances != b.MinInstances || a.MaxInstances != b.MaxInstances ||
		a.ScaleOutCooldownS != b.ScaleOutCooldownS || a.ScaleInCooldownS != b.ScaleInCooldownS ||
		a.ConcurrencyOverflow != b.ConcurrencyOverflow || a.MaxQueueWaitMS != b.MaxQueueWaitMS ||
		a.MaxQueueDepth != b.MaxQueueDepth || a.WakeMaxQueueDepth != b.WakeMaxQueueDepth ||
		a.WakeMaxQueueWaitSeconds != b.WakeMaxQueueWaitSeconds {
		return false
	}
	if a.Target == nil || b.Target == nil {
		return a.Target == nil && b.Target == nil
	}
	return a.Target.Metric == b.Target.Metric && a.Target.Value == b.Target.Value
}

func serviceReplicasEqual(a, b *api.ServiceReplicas) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Min == b.Min && a.Max == b.Max && a.Desired == b.Desired
}

func workerReplicasEqual(a, b *api.WorkerScaling) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Min == b.Min && a.Max == b.Max && a.Metric == b.Metric && a.Name == b.Name &&
		a.Target == b.Target && a.ObserverAppSlug == b.ObserverAppSlug
}

func lifecyclePatchNeeded(current api.AppResponse, desired api.UpdateAppRequest) bool {
	manifest := current.Manifest
	if desired.Profiling != nil && (manifest.Profiling == nil || *desired.Profiling != *manifest.Profiling) {
		return true
	}
	if desired.ExecutionMode != nil && manifest.ExecutionMode != *desired.ExecutionMode {
		return true
	}
	if desired.RestartPolicy != nil && manifest.RestartPolicy != *desired.RestartPolicy {
		return true
	}
	if desired.StartupDeadlineS != nil && manifest.StartupDeadlineS != *desired.StartupDeadlineS {
		return true
	}
	if desired.MaxRetries != nil && manifest.MaxRetries != *desired.MaxRetries {
		return true
	}
	if desired.StopGracePeriodS != nil {
		currentGraceS := int(manifest.StopGracePeriod / time.Second)
		if currentGraceS != *desired.StopGracePeriodS {
			return true
		}
	}
	if desired.StopSignal != nil && manifest.StopSignal != *desired.StopSignal {
		return true
	}
	if desired.RequestTimeoutS != nil && manifest.RequestTimeoutS != *desired.RequestTimeoutS {
		return true
	}
	if desired.ServiceReplicas != nil && !serviceReplicasEqual(manifest.ServiceReplicas, desired.ServiceReplicas) {
		return true
	}
	if desired.WorkerReplicas != nil && !workerReplicasEqual(manifest.WorkerReplicas, desired.WorkerReplicas) {
		return true
	}
	return false
}

// applyManifestLifecycle reads and applies the optional lifecycle block. It
// runs after the app exists, allowing OCI image deploys to use worker mode
// without requiring an HTTP listener or an image-derived app.json first.
func applyManifestLifecycle(ctx context.Context, client manifestScalingClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok || m == nil || ((m.Lifecycle == nil || m.Lifecycle.Empty()) && m.Worker == nil && m.Profiling == nil) {
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	desired := api.UpdateAppRequest{}
	if m.Lifecycle != nil && !m.Lifecycle.Empty() {
		desired = m.Lifecycle.ToAPI()
	}
	if m.Worker != nil {
		workerMode := api.ExecutionModeWorker
		desired.ExecutionMode = &workerMode
		desired.WorkerReplicas = m.Worker.Scale.ToAPI()
		if drainTimeout := m.Worker.DrainTimeoutSeconds(); drainTimeout > 0 {
			desired.StopGracePeriodS = &drainTimeout
		}
		if m.Worker.StopSignal != "" {
			sig := m.Worker.StopSignal
			desired.StopSignal = &sig
		}
	}
	current, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("read app before applying lifecycle policy: %w", err)
	}
	if !lifecyclePatchNeeded(current, desired) {
		return nil
	}
	if _, err := client.UpdateApp(ctx, slug, desired); err != nil {
		return fmt.Errorf("apply lifecycle policy: %w", err)
	}
	return nil
}

// applyManifestScalingPolicy reads and applies optional app defaults from the
// manifest. The API remains authoritative for plan gates and validation.
// It runs after the deployment has been accepted so a failed build/upload
// cannot leave app configuration changed. The API remains authoritative for
// plan gates and workload-class compatibility.
func applyManifestScalingPolicy(ctx context.Context, client manifestScalingClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok || m == nil || (m.Scaling == nil && m.RetryPolicy == nil && (m.Worker == nil || m.Worker.Scale.Metric == "")) {
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	current, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("read app before applying scaling policy: %w", err)
	}
	update := api.UpdateAppRequest{}
	changed := false
	if m.Worker != nil && m.Worker.Scale.Metric != "" {
		policy := &api.ScalingPolicy{
			MinInstances: m.Worker.Scale.Min,
			MaxInstances: m.Worker.Scale.Max,
			Target: &api.ScalingTarget{
				Metric: m.Worker.Scale.Metric,
				Name:   m.Worker.Scale.Name,
				Value:  m.Worker.Scale.Target,
			},
		}
		if !scalingPolicyEqual(current.ScalingPolicy, policy) {
			update.ScalingPolicy = policy
			changed = true
		}
	} else if m.Scaling != nil {
		desired := m.Scaling.ToAPI()
		if !scalingPolicyEqual(current.ScalingPolicy, desired) {
			update.ScalingPolicy = desired
			changed = true
		}
	}
	if m.RetryPolicy != nil {
		desired := &api.RetryPolicyDTO{
			MaxAttempts: m.RetryPolicy.MaxAttempts, BaseSeconds: m.RetryPolicy.BaseSeconds,
			MaxSeconds: m.RetryPolicy.MaxSeconds, JitterSeconds: m.RetryPolicy.JitterSeconds,
		}
		if !retryPolicyDTOEqual(current.RetryPolicy, desired) {
			update.RetryPolicy = desired
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if _, err := client.UpdateApp(ctx, slug, update); err != nil {
		return fmt.Errorf("apply manifest app defaults: %w", err)
	}
	return nil
}

func retryPolicyDTOEqual(left, right *api.RetryPolicyDTO) bool {
	zero := func(p *api.RetryPolicyDTO) bool {
		return p == nil || (p.MaxAttempts == 0 && p.BaseSeconds == 0 && p.MaxSeconds == 0 && p.JitterSeconds == 0)
	}
	if zero(left) || zero(right) {
		return zero(left) && zero(right)
	}
	return left.MaxAttempts == right.MaxAttempts && left.BaseSeconds == right.BaseSeconds &&
		left.MaxSeconds == right.MaxSeconds && left.JitterSeconds == right.JitterSeconds
}
