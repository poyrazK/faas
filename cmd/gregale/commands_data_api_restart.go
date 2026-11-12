package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The restart API refuses a parked app: nothing runs to restart. Parked is
// the normal state of an idle Data API, so its commands must not fail there.

const dataAPIParkedSettingsNote = "App is parked; its next wake boots with the updated settings, so no restart was needed."

// dataAPIParkedWakeTimeout bounds how long a schema refresh waits for a
// parked app it woke to become active before restarting it.
const dataAPIParkedWakeTimeout = 2 * time.Minute

func dataAPIAppParked(ctx context.Context, client *Client, slug string) bool {
	app, err := client.GetApp(ctx, slug)
	return err == nil && app.Status == "evicted_cold"
}

// restartDataAPISettings applies Data API settings stored as secrets with a
// fresh restart. Storing a secret invalidates the app's snapshots, so a
// parked app's next wake boots with it: parked is reported, not an error.
func restartDataAPISettings(ctx context.Context, client *Client, slug string) (api.AppRestartResponse, bool, error) {
	out, err := client.RestartAppFresh(ctx, slug)
	if err == nil {
		return out, false, nil
	}
	if dataAPIAppParked(ctx, client, slug) {
		return api.AppRestartResponse{}, true, nil
	}
	return out, false, err
}

// refreshDataAPIFresh restarts the Data API fresh so it reads the current
// schema. A migration stores no setting, so a parked app's snapshot would
// restore the old contract on its next wake: the app is woken, then
// restarted, which also invalidates that snapshot.
func refreshDataAPIFresh(ctx context.Context, client *Client, slug string) (api.AppRestartResponse, error) {
	out, err := client.RestartAppFresh(ctx, slug)
	if err == nil || !dataAPIAppParked(ctx, client, slug) {
		return out, err
	}
	if _, err := client.Wake(ctx, slug); err != nil {
		return out, fmt.Errorf("wake the parked app before refreshing it: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, dataAPIParkedWakeTimeout)
	defer cancel()
	for {
		out, err = client.RestartAppFresh(waitCtx, slug)
		if err == nil {
			return out, nil
		}
		if app, getErr := client.GetApp(waitCtx, slug); getErr == nil && app.Status != "evicted_cold" && app.Status != "active" {
			return out, err
		}
		select {
		case <-waitCtx.Done():
			return out, fmt.Errorf("the parked app did not become active to refresh: %w", err)
		case <-time.After(time.Second):
		}
	}
}
