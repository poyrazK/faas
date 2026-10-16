package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentScheduleActivityWatchArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Named environment with current read consent"`
	After       string `json:"after,omitempty" jsonschema:"Saved scoped activity cursor; omit to read from feed start"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Page size, default 20 and maximum 50"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"Maximum wait, default 20 seconds and maximum 45 seconds"`
}

type environmentScheduleActivityWatchResult struct {
	api.EnvironmentCacheScheduleActivity
	WatchStatus   string `json:"watch_status"`
	WatchTimedOut bool   `json:"watch_timed_out"`
}

func (m *management) watchEnvironmentCacheScheduleActivity(ctx context.Context, args environmentScheduleActivityWatchArgs) (any, error) {
	return m.watchScheduleActivityChecked(ctx, args, nil)
}

func activityWatchWait(wait int) (time.Duration, error) {
	if wait == 0 {
		wait = api.EnvironmentCacheActivityWatchDefaultSeconds
	}
	if wait < 1 || wait > api.EnvironmentCacheActivityWatchMaxSeconds {
		return 0, invalid("invalid_argument", "wait_seconds must be between 1 and 45")
	}
	return time.Duration(wait) * time.Second, nil
}

// The optional check pins a consumer revision/generation across observations,
// including an empty timeout. It must not mutate progress or hold a store lock
// across native calls. Raw cursor watches retain their existing behavior.
func (m *management) watchScheduleActivityChecked(ctx context.Context, args environmentScheduleActivityWatchArgs, check func(context.Context) error) (any, error) {
	wait, err := activityWatchWait(args.WaitSeconds)
	if err != nil {
		return nil, err
	}
	guard := func(c context.Context) error {
		if check != nil {
			return check(c)
		}
		return c.Err()
	}
	read := environmentScheduleActivityArgs{App: args.App, Project: args.Project, Environment: args.Environment, After: args.After, Limit: args.Limit}
	watchCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var last *environmentScheduleActivityWatchResult
	timeout := func() (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := guard(ctx); err != nil {
			return nil, err
		}
		if last == nil {
			return nil, watchCtx.Err()
		}
		last.WatchStatus, last.WatchTimedOut = "timed_out", true
		return *last, nil
	}
	for {
		if err := guard(watchCtx); err != nil {
			if errors.Is(watchCtx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) {
				return timeout()
			}
			return nil, err
		}
		value, err := m.getEnvironmentCacheScheduleActivity(watchCtx, read)
		// Use the parent context so the final guard also runs after the watch's
		// own deadline. A parent cancellation always remains an error.
		if checkErr := guard(ctx); checkErr != nil {
			return nil, checkErr
		}
		if err != nil {
			if errors.Is(watchCtx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) {
				return timeout()
			}
			return nil, err
		}
		page := value.(api.EnvironmentCacheScheduleActivity)
		if len(page.Entries) > 0 {
			return environmentScheduleActivityWatchResult{EnvironmentCacheScheduleActivity: page, WatchStatus: "events"}, nil
		}
		if read.After == "" {
			read.After = page.Cursor
		}
		// An empty observation never consumes or rewrites the caller's checkpoint.
		page.Cursor = read.After
		last = &environmentScheduleActivityWatchResult{EnvironmentCacheScheduleActivity: page}
		timer := time.NewTimer(time.Second)
		select {
		case <-watchCtx.Done():
			timer.Stop()
			return timeout()
		case <-timer.C:
		}
	}
}
