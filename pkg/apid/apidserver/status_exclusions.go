package apidserver

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// excludedDeletedAppWindow is how far back excludedAppIDs reaches for an
// excluded account's deleted apps: their gateway series stay in the
// availability query's 5 m rate window after the delete, with margin.
const excludedDeletedAppWindow = 15 * time.Minute

// excludedDeletedAppPage bounds the deleted apps read per excluded account
// (the store's page limit); a burst of more deletes in the window keeps the
// oldest of them counted.
const excludedDeletedAppPage = 51

// excludedAppIDs lists the apps of the operator's status_excluded_account_ids,
// whose gateway traffic the API availability indicator leaves out, as the
// deployment-success aggregate leaves out their deployments: live apps, and
// apps deleted within excludedDeletedAppWindow (an internal load test or
// journey often deletes its app right after the traffic). A listing error
// keeps that account's apps in: the indicator then over-counts rather than
// hides a fault.
func (c *statusCache) excludedAppIDs(ctx context.Context) []string {
	if c.excludedAccounts == nil || c.store == nil {
		return nil
	}
	deleted, _ := c.store.(state.AccountDeletedAppsStore)
	cutoff := time.Now().Add(-excludedDeletedAppWindow)
	var ids []string
	for _, account := range c.excludedAccounts() {
		apps, err := c.store.ListApps(ctx, account)
		if err != nil {
			c.log.Warn("status: list excluded account apps", "account", account, "err", err)
			continue
		}
		for _, app := range apps {
			ids = append(ids, app.ID)
		}
		if deleted == nil {
			continue
		}
		recent, err := deleted.ListAccountDeletedApps(ctx, account, time.Time{}, "", excludedDeletedAppPage)
		if err != nil {
			c.log.Warn("status: list excluded account deleted apps", "account", account, "err", err)
			continue
		}
		for _, app := range recent {
			if app.DeletedAt != nil && app.DeletedAt.After(cutoff) {
				ids = append(ids, app.ID)
			}
		}
	}
	return ids
}

// availabilityQueryExcluding is statusAPIAvailabilityQuery without the given
// apps' series. The gateway counters carry only app and plan labels, so the
// account exclusion becomes an app matcher (prod 2026-11-23: an internal load
// test stopping and an internal probe's 503s during its redeploy put the
// public status page's Networking at 13% availability). Each ID is a regexp
// literal inside a PromQL string literal, so the regexp's own backslashes
// are escaped again for the string.
func availabilityQueryExcluding(appIDs []string) string {
	if len(appIDs) == 0 {
		return statusAPIAvailabilityQuery
	}
	quoted := make([]string, len(appIDs))
	for i, id := range appIDs {
		quoted[i] = strings.ReplaceAll(regexp.QuoteMeta(id), `\`, `\\`)
	}
	matcher := `app!="-",app!~"` + strings.Join(quoted, "|") + `"`
	return strings.ReplaceAll(statusAPIAvailabilityQuery, `app!="-"`, matcher)
}
