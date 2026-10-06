package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// appSlugsByID maps the account's app IDs to slugs for usage tables. The
// usage endpoints return app_id only, and `usage daily` / `usage storage`
// printed bare UUIDs. The lookup is best-effort: on error the tables fall
// back to IDs.
func appSlugsByID(client *api.Client) map[string]string {
	apps, err := client.ListApps(context.Background())
	if err != nil {
		return nil
	}
	slugs := make(map[string]string, len(apps))
	for _, app := range apps {
		if app.ID != "" && app.Slug != "" {
			slugs[app.ID] = app.Slug
		}
	}
	return slugs
}

// appLabel is the app's slug when known, else its ID (a deleted app keeps
// usage rows).
func appLabel(slugs map[string]string, appID string) string {
	if slug, ok := slugs[appID]; ok {
		return slug
	}
	return appID
}
