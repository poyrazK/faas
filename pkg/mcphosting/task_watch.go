package mcphosting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// WatchTaskBySubmissionKey reports authorized metadata until the retained Task
// needs input or reaches a terminal state. The first lookup pins the Task ID.
// Subscriptions wake the watch; receipt lookups supply every reported snapshot.
func (c *Client) WatchTaskBySubmissionKey(ctx context.Context, key string, interval time.Duration, report func(Task) error) error {
	if interval <= 0 || report == nil {
		return errors.New("MCP task watch requires a positive interval and a status callback")
	}
	current, _, err := c.GetTaskBySubmissionKey(ctx, key)
	if err != nil {
		return fmt.Errorf("read task receipt status: %w", err)
	}
	if err := report(current); err != nil {
		return err
	}

	streaming := true
	failures := 0
	reconnectDelay := taskStreamReconnectBase
	for current.Status == "working" {
		if streaming {
			streamCtx, cancel := context.WithTimeout(ctx, interval)
			_, err := c.listenTaskUpdate(streamCtx, current)
			expired := errors.Is(streamCtx.Err(), context.DeadlineExceeded)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch {
			case err == nil || expired:
				failures = 0
				reconnectDelay = taskStreamReconnectBase
			case errors.Is(err, errTaskSubscriptionsUnsupported):
				streaming = false
			case errors.Is(err, errTaskSubscriptionClosed), errors.Is(err, errTaskSubscriptionInterrupted):
				failures++
				streaming = failures < maxTaskStreamReconnects
			default:
				return err
			}
		} else if err := waitForTaskPoll(ctx, max(interval, current.PollDelay())); err != nil {
			return err
		}

		next, _, err := c.GetTaskBySubmissionKey(ctx, key)
		if err != nil {
			return fmt.Errorf("refresh task receipt status: %w", err)
		}
		if next.TaskID != current.TaskID {
			return errors.New("task receipt lookup returned a different Task")
		}
		if next.Status != current.Status || next.LastUpdatedAt != current.LastUpdatedAt || next.PollDelay() != current.PollDelay() {
			if err := report(next); err != nil {
				return err
			}
		}
		current = next
		if current.Status == "working" && streaming && failures > 0 {
			if err := waitForTaskPoll(ctx, reconnectDelay); err != nil {
				return err
			}
			reconnectDelay = min(reconnectDelay*2, taskStreamReconnectMax)
		}
	}
	return nil
}
