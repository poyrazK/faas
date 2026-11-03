package apidserver

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/resourcelabels"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardSelectApps applies the apps page's label selector
// (ADR-20261031100025) and reads the labels its rows show. It returns the
// apps to list, their labels by app ID, and a message when the selector could
// not be applied; the list is empty then, so a bad selector never reads as a
// match. The caller checks the capability first.
func (s *server) dashboardSelectApps(r *http.Request, acct state.Account, apps []state.App) ([]state.App, map[string]map[string]string, string) {
	selected := resourcelabels.Requested(r.URL.Query())
	sel, problem := resourcelabels.ParseSelector(r.URL.Query())
	if problem != nil {
		return nil, nil, problem.Detail
	}
	store, ok := s.appLabelStore()
	if !ok {
		if selected {
			return nil, nil, "Labels are not available right now."
		}
		return apps, nil, ""
	}
	byApp, err := resourcelabels.AppLabels(r.Context(), store, apps)
	if err != nil {
		s.log.WarnContext(r.Context(), "dashboard: app labels", "account", acct.ID, "err", err)
		if selected {
			return nil, nil, "Labels are temporarily unavailable; try again."
		}
		return apps, nil, ""
	}
	return resourcelabels.SelectApps(apps, byApp, sel), byApp, ""
}

// dashboardResourceLabels reads the labels of databases or crons for a
// dashboard page, keyed by ID; nil while the account does not have resource
// labels or the read fails, so the page renders without them.
func (s *server) dashboardResourceLabels(r *http.Request, acct state.Account, kind state.LabelKind, ids []string) map[string]map[string]string {
	if len(ids) == 0 || !s.labelsReleased(r, acct) {
		return nil
	}
	store, ok := s.resourceLabelStore()
	if !ok {
		return nil
	}
	byID, err := store.ResourceLabelsByID(r.Context(), kind, ids)
	if err != nil {
		s.log.WarnContext(r.Context(), "dashboard: resource labels", "kind", kind, "account", acct.ID, "err", err)
		return nil
	}
	return byID
}

// dashboardSelectorValue is the selector as the page echoes it back.
func dashboardSelectorValue(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get(resourcelabels.SelectorParam))
}

func dashboardCronIDs(items []dashboard.CronItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func attachDashboardCronLabels(items []dashboard.CronItem, byID map[string]map[string]string) {
	for i := range items {
		items[i].Labels = byID[items[i].ID]
	}
}

func dashboardDatabaseIDs(items []dashboard.ManagedPostgresDatabaseItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func attachDashboardDatabaseLabels(items []dashboard.ManagedPostgresDatabaseItem, byID map[string]map[string]string) {
	for i := range items {
		items[i].Labels = byID[items[i].ID]
	}
}
