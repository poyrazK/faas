package mcpmanagement

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

const watchActivityConsumerTool = "gregale_watch_environment_cache_activity_consumer"

type activityConsumerWatchArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Named environment with current read consent"`
	Consumer    string `json:"consumer" jsonschema:"Durable consumer name, maximum 64 bytes"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Events per page, default 20 and maximum 50"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"Maximum observation wait, default 20 seconds and maximum 45 seconds"`
}

type activityConsumerWatchResult struct {
	activityConsumerResult
	Page           *api.EnvironmentCacheScheduleActivity `json:"page,omitempty"`
	Acknowledgment *activityConsumerAckArgs              `json:"acknowledgment,omitempty"`
	WatchStatus    string                                `json:"watch_status"`
	WatchTimedOut  bool                                  `json:"watch_timed_out"`
}

func (m *management) checkConsumerObservation(ctx context.Context, args activityConsumerArgs, b activityConsumerBinding, before activityConsumerRecord) error {
	fresh, err := m.consumerRecord(ctx, args, b, nil)
	if err != nil {
		return err
	}
	if fresh.Generation != before.Generation {
		return consumerGenerationConflict()
	}
	if fresh.Revision != before.Revision {
		return invalid("checkpoint_conflict", "consumer revision changed during observation; read it again before resuming")
	}
	return nil
}

func (m *management) watchActivityConsumer(ctx context.Context, args activityConsumerWatchArgs) (any, error) {
	if _, err := activityWatchWait(args.WaitSeconds); err != nil {
		return nil, err
	}
	limit, err := api.NormalizeEnvironmentCacheActivityPage("", args.Limit)
	if err != nil {
		return nil, invalid("invalid_argument", "select a bounded consumer activity page")
	}
	base := activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer}
	b, point, err := m.activityConsumerTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	record, err := m.consumerRecord(ctx, base, b, nil)
	if err != nil {
		return nil, err
	}
	check := func(c context.Context) error { return m.checkConsumerObservation(c, base, b, record) }
	out := activityConsumerWatchResult{activityConsumerResult: consumerResult(b, record), WatchStatus: "recovery_required"}
	if record.Revision == 0 {
		// Read access still requires native validation. Never choose or persist
		// an initial baseline merely because a caller starts watching.
		if err = m.validateConsumerCursor(ctx, base, b, point.RestartCursor); err != nil {
			return nil, err
		}
		if err = check(ctx); err != nil {
			return nil, err
		}
		out.Hint = "Complete and process a baseline with gregale_recover_environment_cache_activity_consumer, then explicitly acknowledge its activity_cursor with expected_revision=0 and this generation before watching."
		return guardConsumerRead(out, base, b, record), nil
	}
	value, err := m.watchScheduleActivityChecked(ctx, environmentScheduleActivityWatchArgs{App: args.App, Project: args.Project, Environment: args.Environment, After: record.Cursor, Limit: limit, WaitSeconds: args.WaitSeconds}, check)
	if err = scheduleWorkflowError(err); err != nil {
		var expired *activityCursorExpiredError
		if !errors.As(err, &expired) {
			return nil, err
		}
		out.Status, out.ActivityRecovery = "cursor_expired", &expired.recovery
		out.Hint = "Rebuild and process the complete baseline using gregale_recover_environment_cache_activity_consumer, then explicitly acknowledge its activity_cursor with baseline_rebuilt=true, this revision and generation. Historical events cannot be reconstructed."
		return guardConsumerRead(out, base, b, record), nil
	}
	page := value.(environmentScheduleActivityWatchResult)
	out.Page, out.WatchStatus, out.WatchTimedOut = &page.EnvironmentCacheScheduleActivity, page.WatchStatus, page.WatchTimedOut
	if len(page.Entries) > 0 {
		out.Acknowledgment = &activityConsumerAckArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer, Cursor: page.Cursor, ExpectedRevision: record.Revision, ExpectedGeneration: record.Generation}
	}
	return guardConsumerRead(out, base, b, record), nil
}
