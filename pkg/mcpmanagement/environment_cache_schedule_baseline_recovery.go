package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentScheduleRecoveryArgs struct {
	App           string `json:"app" jsonschema:"Gregale app slug"`
	Project       string `json:"project" jsonschema:"Owning project"`
	Environment   string `json:"environment" jsonschema:"Named environment with current read consent"`
	RecoveryToken string `json:"recovery_token,omitempty" jsonschema:"Opaque continuation from the previous recovery page; omit to start a new baseline"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Baseline schedules per call, default 20 and maximum 50"`
}

// Continuations are untrusted pagination metadata, never authorization grants.
// Both constituent cursors and current consent are checked on every request.
type environmentScheduleRecoveryToken struct {
	Version     int    `json:"version"`
	Endpoint    string `json:"endpoint"`
	Account     string `json:"account"`
	App         string `json:"app"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Scope       string `json:"scope"`
	Checkpoint  string `json:"checkpoint"`
	After       string `json:"after"`
}

type environmentScheduleRecoveryResult struct {
	AppID          string                         `json:"app_id"`
	ProjectID      string                         `json:"project_id"`
	EnvironmentID  string                         `json:"environment_id"`
	Scope          string                         `json:"scope"`
	Entries        []api.EnvironmentCacheSchedule `json:"entries"`
	Complete       bool                           `json:"complete"`
	RecoveryToken  string                         `json:"recovery_token,omitempty"`
	ActivityCursor string                         `json:"activity_cursor,omitempty"`
	Basis          string                         `json:"basis"`
}

type activityRecoveryRestartError struct{ expired *activityCursorExpiredError }

func (*activityRecoveryRestartError) Error() string {
	return "activity recovery checkpoint expired; restart baseline discovery"
}

func decodeScheduleRecoveryToken(raw string) (environmentScheduleRecoveryToken, error) {
	var token environmentScheduleRecoveryToken
	bad := invalid("invalid_argument", "invalid recovery token; start a new recovery by omitting recovery_token")
	if len(raw) > api.EnvironmentCacheActivityRecoveryTokenMaxBytes {
		return token, bad
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return token, bad
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&token) != nil || decoder.Decode(&struct{}{}) != io.EOF || token.Version != 1 || token.After == "" || token.Checkpoint == "" {
		return token, bad
	}
	return token, nil
}

func (m *management) guardScheduleRecovery(ctx context.Context, args environmentScheduleRecoveryArgs, checkpoint string) error {
	_, err := m.getEnvironmentCacheScheduleActivity(ctx, environmentScheduleActivityArgs{App: args.App, Project: args.Project, Environment: args.Environment, After: checkpoint, Limit: 1})
	var expired *activityCursorExpiredError
	if errors.As(err, &expired) {
		return &activityRecoveryRestartError{expired: expired}
	}
	return err
}

func (m *management) recoverEnvironmentCacheScheduleActivity(ctx context.Context, args environmentScheduleRecoveryArgs) (result any, err error) {
	defer func() { err = scheduleWorkflowError(err) }()
	options, err := api.NormalizeEnvironmentCacheScheduleList(api.EnvironmentCacheScheduleListOptions{Limit: args.Limit})
	if err != nil || args.Environment == "" || len(args.RecoveryToken) > api.EnvironmentCacheActivityRecoveryTokenMaxBytes {
		return nil, invalid("invalid_argument", "select a named environment and bounded recovery page")
	}
	var token environmentScheduleRecoveryToken
	if args.RecoveryToken != "" {
		token, err = decodeScheduleRecoveryToken(args.RecoveryToken)
		if err != nil {
			return nil, err
		}
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if err = m.requireScheduleRecoverySupport(ctx); err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity missing")
	}
	endpoint := strings.TrimRight(m.opts.APIBase, "/")
	if args.RecoveryToken == "" {
		point, e := m.client.GetEnvironmentCacheScheduleActivityCheckpoint(ctx, args.App, args.Environment)
		if e != nil {
			return nil, e
		}
		if e = validateScheduleActivityCheckpoint(point, account.ID, app.ID, env.ProjectID, env.ID, args.Environment); e != nil {
			return nil, e
		}
		token = environmentScheduleRecoveryToken{Version: 1, Endpoint: endpoint, Account: account.ID, App: app.ID, Project: env.ProjectID, Environment: env.ID, Scope: args.Environment, Checkpoint: point.RestartCursor}
	} else {
		if token.Endpoint != endpoint || token.Account != account.ID || token.App != app.ID || token.Project != env.ProjectID || token.Environment != env.ID || token.Scope != args.Environment {
			return nil, invalid("invalid_argument", "recovery token belongs to another endpoint, account or immutable environment")
		}
		if _, e := api.ParseEnvironmentCacheActivityCursor(token.Checkpoint, account.ID, app.ID, env.ProjectID, env.ID); e != nil {
			return nil, invalid("invalid_argument", "invalid recovery activity checkpoint")
		}
		options.After = token.After
		if _, e := api.ParseEnvironmentCacheScheduleCursor(options, app.ID, env.ProjectID, env.ID); e != nil {
			return nil, invalid("invalid_argument", "invalid recovery schedule continuation")
		}
	}
	if err = m.guardScheduleRecovery(ctx, args, token.Checkpoint); err != nil {
		return nil, err
	}
	value, err := m.listEnvironmentCacheSchedules(ctx, environmentScheduleListArgs{App: args.App, Project: args.Project, Environment: args.Environment, After: token.After, Limit: options.Limit})
	if err != nil {
		return nil, err
	}
	page := value.(api.EnvironmentCacheScheduleList)
	if page.AppID != token.App || page.ProjectID != token.Project || page.EnvironmentID != token.Environment || page.Scope != token.Scope {
		return nil, invalid("plan_stale", "recovery target changed during discovery")
	}
	// A slow page can be overtaken by retention. Withhold it and its continuation
	// rather than silently replacing the original checkpoint with a newer one.
	if err = m.guardScheduleRecovery(ctx, args, token.Checkpoint); err != nil {
		return nil, err
	}
	out := environmentScheduleRecoveryResult{AppID: page.AppID, ProjectID: page.ProjectID, EnvironmentID: page.EnvironmentID, Scope: page.Scope, Entries: page.Entries, Complete: page.NextCursor == "", Basis: "Live schedule baseline, not a frozen snapshot. After completion consume activity after activity_cursor; refresh exact schedule status for overlaps. Removed history is unavailable; execution is separate from gateway convergence."}
	if out.Complete {
		out.ActivityCursor = token.Checkpoint
	} else {
		token.After = page.NextCursor
		raw, _ := json.Marshal(token)
		out.RecoveryToken = base64.RawURLEncoding.EncodeToString(raw)
		if len(out.RecoveryToken) > api.EnvironmentCacheActivityRecoveryTokenMaxBytes {
			return nil, invalid("response_too_large", "recovery token exceeds its bound")
		}
	}
	return out, nil
}
