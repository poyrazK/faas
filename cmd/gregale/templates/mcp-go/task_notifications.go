package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func taskNotificationChannel(namespace string) string {
	digest := md5.Sum([]byte(namespace))
	return "gregale_mcp_task_" + hex.EncodeToString(digest[:])
}

func (runtime *mcpTaskRuntime) startTaskNotificationListener() {
	ctx, cancel := context.WithCancel(context.Background())
	runtime.taskNotifyCancel = cancel
	runtime.taskNotifyDone = make(chan struct{})
	go runtime.taskNotificationLoop(ctx)
}

func (runtime *mcpTaskRuntime) taskNotificationLoop(ctx context.Context) {
	defer close(runtime.taskNotifyDone)
	retryDelay := time.Second
	for ctx.Err() == nil {
		connection, err := runtime.pool.Acquire(ctx)
		if err == nil {
			_, err = connection.Exec(ctx, "LISTEN "+runtime.notificationChannel)
		}
		if err == nil {
			retryDelay = time.Second
			for ctx.Err() == nil {
				var notification *pgconn.Notification
				notification, err = connection.Conn().WaitForNotification(ctx)
				if err != nil {
					break
				}
				if notification.Channel == runtime.notificationChannel && taskIDPattern.MatchString(notification.Payload) {
					runtime.notifyTaskChanged(notification.Payload)
				}
			}
		}
		if connection != nil {
			// LISTEN state is session-scoped. Discard this connection on shutdown
			// and reconnect so it cannot leak an active channel back into the pool.
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = connection.Conn().Close(closeCtx)
			cancel()
			connection.Release()
		}
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
		if retryDelay < 30*time.Second {
			retryDelay *= 2
			if retryDelay > 30*time.Second {
				retryDelay = 30 * time.Second
			}
		}
	}
}

func (runtime *mcpTaskRuntime) subscribeTaskChanges(taskIDs []string) (<-chan string, func()) {
	updates := make(chan string, maxTaskSubscriptionIDs)
	runtime.taskNotifyMu.Lock()
	for _, taskID := range taskIDs {
		watchers := runtime.taskNotifyWatchers[taskID]
		if watchers == nil {
			watchers = make(map[chan string]struct{})
			runtime.taskNotifyWatchers[taskID] = watchers
		}
		watchers[updates] = struct{}{}
	}
	runtime.taskNotifyMu.Unlock()

	return updates, func() {
		runtime.taskNotifyMu.Lock()
		defer runtime.taskNotifyMu.Unlock()
		for _, taskID := range taskIDs {
			watchers := runtime.taskNotifyWatchers[taskID]
			delete(watchers, updates)
			if len(watchers) == 0 {
				delete(runtime.taskNotifyWatchers, taskID)
			}
		}
	}
}

func (runtime *mcpTaskRuntime) notifyTaskChanged(taskID string) {
	runtime.taskNotifyMu.RLock()
	defer runtime.taskNotifyMu.RUnlock()
	for updates := range runtime.taskNotifyWatchers[taskID] {
		select {
		case updates <- taskID:
		default:
			// A full queue is recovered by the bounded snapshot polling path.
		}
	}
}
