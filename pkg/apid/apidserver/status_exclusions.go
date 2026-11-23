package apidserver

import (
	"context"
	"regexp"
	"strings"
)

// excludedAppIDs lists the apps of the operator's status_excluded_account_ids,
// whose gateway traffic the API availability indicator leaves out, as the
// deployment-success aggregate leaves out their deployments. A listing error
// keeps that account's apps in: the indicator then over-counts rather than
// hides a fault.
func (c *statusCache) excludedAppIDs(ctx context.Context) []string {
	if c.excludedAccounts == nil || c.store == nil {
		return nil
	}
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
	}
	return ids
}

// availabilityQueryExcluding is statusAPIAvailabilityQuery without the given
// apps' series. The gateway counters carry only app and plan labels, so the
// account exclusion becomes an app matcher (prod 2026-11-23: an internal load
// test stopping and an internal probe's 503s during its redeploy put the
// public status page's Networking at 13% availability).
func availabilityQueryExcluding(appIDs []string) string {
	if len(appIDs) == 0 {
		return statusAPIAvailabilityQuery
	}
	quoted := make([]string, len(appIDs))
	for i, id := range appIDs {
		quoted[i] = regexp.QuoteMeta(id)
	}
	matcher := `app!="-",app!~"` + strings.Join(quoted, "|") + `"`
	return strings.ReplaceAll(statusAPIAvailabilityQuery, `app!="-"`, matcher)
}
