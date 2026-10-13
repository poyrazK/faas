package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runCommitSubscriberWait(ctx context.Context, client *api.Client, source, event, subscription string, interval time.Duration) int {
	accepted, err := waitCommitEvent(ctx, client, source, event, "accepted", interval)
	if err != nil || accepted.Receipt == nil || accepted.Receipt.Publication == nil {
		if code := jsonOut(writeJSON(accepted)); code != 0 {
			return code
		}
		if err == nil {
			err = fmt.Errorf("subscriber wait requires an accepted event publication")
		}
		return commitSubscriberWaitError(err)
	}
	out, err := client.WaitCommitSubscriber(ctx, *accepted.Receipt.Publication, subscription, interval)
	if errors.Is(err, context.DeadlineExceeded) {
		out.Status = "timed_out"
	}
	if errors.Is(err, context.Canceled) {
		out.Status = "interrupted"
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	if out.Status == "completed" {
		return 0
	}
	return 1
}

func commitSubscriberWaitError(err error) int {
	printErr("Stopped waiting; durable work has not been cancelled", err)
	if errors.Is(err, context.DeadlineExceeded) {
		return 124
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}
