package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type manifestCronRollbackStep struct {
	description string
	undo        func(context.Context) error
}

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

func reconcileManifestCronsWithRollback(ctx context.Context, client manifestCronClient, slug string, m *gregalemanifest.Manifest) (*manifestCronTransaction, error) {
	txn := &manifestCronTransaction{}
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
