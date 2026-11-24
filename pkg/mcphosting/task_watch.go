package mcphosting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MaxTaskSubscriptionIDs is the maximum number of Task IDs in a starter subscription.
const MaxTaskSubscriptionIDs = 64

// ListenTaskChanges opens one subscription and calls onChange for each changed
// Task. The caller can stop the stream by returning true. Notifications
// signal changes without passing results or form data to the callback.
func (c *Client) ListenTaskChanges(ctx context.Context, tasks []Task, onChange func(string) (bool, error)) error {
	if len(tasks) == 0 || len(tasks) > MaxTaskSubscriptionIDs || onChange == nil {
		return errors.New("MCP task watch requires 1–64 Tasks and a callback")
	}
	ids := make([]string, 0, len(tasks))
	current := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		if _, ok := current[task.TaskID]; !ok {
			ids = append(ids, task.TaskID)
		}
		current[task.TaskID] = task
	}
	return c.listenTaskNotifications(ctx, ids, func(next Task) (bool, error) {
		prior := current[next.TaskID]
		if next.Status == prior.Status && next.LastUpdatedAt == prior.LastUpdatedAt {
			return false, nil
		}
		prior.Status, prior.LastUpdatedAt = next.Status, next.LastUpdatedAt
		current[next.TaskID] = prior
		return onChange(next.TaskID)
	})
}

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
			case errors.Is(err, ErrTaskSubscriptionsUnsupported):
				streaming = false
			case errors.Is(err, ErrTaskSubscriptionClosed), errors.Is(err, ErrTaskSubscriptionInterrupted):
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
