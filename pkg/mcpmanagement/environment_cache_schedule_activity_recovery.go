package mcpmanagement

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only metadata validated against the selected account and immutable environment
// may leave MCP. Other API problems do not acquire this extension implicitly.
type activityCursorExpiredError struct {
	recovery api.EnvironmentCacheActivityRecovery
}

func (*activityCursorExpiredError) Error() string { return "cache schedule activity cursor expired" }

func (m *management) validateActivityExpiry(ctx context.Context, err error, args environmentScheduleActivityArgs, account, app, project, env string, before int64) error {
	var native *api.APIError
	if !errors.As(err, &native) || native.Problem.Code != api.CodeActivityCursorExpired {
		return err
	}
	recovery := native.Problem.ActivityRecovery
	if native.Problem.Status != 410 || recovery == nil || recovery.AppID != app || recovery.ProjectID != project || recovery.EnvironmentID != env || recovery.Scope != args.Environment || recovery.RetentionSeconds != api.EnvironmentCacheActivityRetentionSeconds {
		return invalid("invalid_response", "invalid activity recovery scope or retention")
	}
	floor, e1 := api.ParseEnvironmentCacheActivityCursor(recovery.OldestCursor, account, app, project, env)
	head, e2 := api.ParseEnvironmentCacheActivityCursor(recovery.RestartCursor, account, app, project, env)
	if recovery.OldestCursor == "" || recovery.RestartCursor == "" || e1 != nil || e2 != nil || floor.Sequence <= before || head.Sequence < floor.Sequence {
		return invalid("invalid_response", "invalid activity recovery checkpoints")
	}
	fresh, freshEnv, e := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if e != nil {
		return e
	}
	if fresh.ID != app || freshEnv.ID != env || freshEnv.ProjectID != project {
		return invalid("plan_stale", "environment changed during activity recovery")
	}
	return &activityCursorExpiredError{recovery: *recovery}
}
