package mcpmanagement

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

const scheduleUpgradeGuidance = "Upgrade the serving apid and state backend with schedule activity, checkpoint and retention support; apply the documented migrations and upgrade all schedule writers before relying on the feed. Run gregale_preflight again."

func scheduleWorkflowError(err error) error {
	var upstream *api.APIError
	if errors.As(err, &upstream) && (upstream.Problem.Status == 404 || upstream.Problem.Status == 405 || upstream.Problem.Status == 501) {
		copy := *upstream
		copy.Problem.Hint = "The endpoint or selected target is unavailable. Verify the target still exists and check responding API versions. " + scheduleUpgradeGuidance
		return &copy
	}
	return err
}

// The catalog remains static and performs no native I/O. Compatibility is read
// explicitly, never cached as permission or a guarantee about another replica.
func (m *management) requireScheduleRecoverySupport(ctx context.Context) error {
	out, err := m.client.GetCapabilities(ctx)
	if err != nil {
		var upstream *api.APIError
		if errors.As(err, &upstream) && (upstream.Problem.Status == 404 || upstream.Problem.Status == 405 || upstream.Problem.Status == 501) {
			return invalid("backend_capabilities_unavailable", "Backend compatibility could not be verified. "+scheduleUpgradeGuidance)
		}
		return err
	}
	if !out.EnvironmentCacheSchedules.SupportsRecovery() {
		return invalid("backend_upgrade_required", "Schedule recovery support is not advertised by this backend. "+scheduleUpgradeGuidance)
	}
	return nil
}

func (m *management) scheduleActivityPreflight(ctx context.Context, args appArgs) map[string]preflightCheck {
	keys := []string{"schedule_listing", "schedule_activity", "schedule_checkpoint", "schedule_retention", "schedule_recovery"}
	checks := make(map[string]preflightCheck, len(keys))
	setAll := func(check preflightCheck) {
		for _, key := range keys {
			checks[key] = check
		}
	}
	if args.Environment == "" {
		setAll(preflightCheck{"unknown", "select_named_environment"})
		return checks
	}
	caps, err := m.client.GetCapabilities(ctx)
	if err != nil {
		setAll(readPreflightCheck(err))
		return checks
	}
	c := caps.EnvironmentCacheSchedules
	if c == nil {
		setAll(preflightCheck{"unknown", "capability_not_advertised"})
		return checks
	}
	for key, supported := range map[string]bool{
		"schedule_listing": c.Listing, "schedule_activity": c.Activity,
		"schedule_checkpoint": c.Checkpoint, "schedule_retention": c.RetentionSeconds == api.EnvironmentCacheActivityRetentionSeconds,
	} {
		checks[key] = preflightCheck{"unknown", "requires_validated_scoped_read"}
		if !supported {
			checks[key] = preflightCheck{"unsupported", "backend_feature_unavailable"}
		}
	}
	if c.Listing {
		_, err = m.listEnvironmentCacheSchedules(ctx, environmentScheduleListArgs{App: args.App, Project: args.Project, Environment: args.Environment, Limit: 1})
		checks["schedule_listing"] = readPreflightCheck(err)
	}
	if c.Checkpoint {
		point, pointErr := m.scheduleActivityCheckpoint(ctx, args)
		checks["schedule_checkpoint"] = readPreflightCheck(pointErr)
		if pointErr == nil {
			if c.RetentionSeconds == api.EnvironmentCacheActivityRetentionSeconds {
				checks["schedule_retention"] = preflightCheck{"passed", "validated_checkpoint_retention"}
			}
			if c.Activity {
				_, err = m.getEnvironmentCacheScheduleActivity(ctx, environmentScheduleActivityArgs{App: args.App, Project: args.Project, Environment: args.Environment, After: point.RestartCursor, Limit: 1})
				checks["schedule_activity"] = readPreflightCheck(err)
			}
		}
	}
	checks["schedule_recovery"] = preflightCheck{"passed", "validated_recovery_prerequisites"}
	for _, status := range []string{"unknown", "unsupported", "blocked"} {
		for _, key := range keys[:4] {
			if checks[key].Status == status {
				checks["schedule_recovery"] = preflightCheck{status, "recovery_prerequisite_not_verified"}
			}
		}
	}
	return checks
}

// Use the same checkpoint validation for recovery and preflight. A capability
// flag cannot replace scoped identity, cursor, retention or permission checks.
func (m *management) scheduleActivityCheckpoint(ctx context.Context, args appArgs) (api.EnvironmentCacheActivityRecovery, error) {
	var zero api.EnvironmentCacheActivityRecovery
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return zero, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return zero, err
	}
	point, err := m.client.GetEnvironmentCacheScheduleActivityCheckpoint(ctx, args.App, args.Environment)
	if err != nil {
		return zero, err
	}
	if err = validateScheduleActivityCheckpoint(point, account.ID, app.ID, env.ProjectID, env.ID, args.Environment); err != nil {
		return zero, err
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return zero, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return zero, invalid("plan_stale", "environment changed during checkpoint inspection")
	}
	return point, nil
}

func validateScheduleActivityCheckpoint(point api.EnvironmentCacheActivityRecovery, account, app, project, environment, scope string) error {
	floor, e1 := api.ParseEnvironmentCacheActivityCursor(point.OldestCursor, account, app, project, environment)
	head, e2 := api.ParseEnvironmentCacheActivityCursor(point.RestartCursor, account, app, project, environment)
	if account == "" || point.AppID != app || point.ProjectID != project || point.EnvironmentID != environment || point.Scope != scope || point.OldestCursor == "" || point.RestartCursor == "" || e1 != nil || e2 != nil || floor.Sequence > head.Sequence || point.RetentionSeconds != api.EnvironmentCacheActivityRetentionSeconds {
		return invalid("invalid_response", "invalid activity checkpoint scope or bounds")
	}
	return nil
}
