// adr: 1304
package automationconsole

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) inspectBaselineCatalog(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) {
	var catalog api.ListAutomationLiveBaselinesResponse
	if !read(s.Host.Request(r, scope, Operation{Kind: "baseline-list", Name: data.Name}), &catalog) || catalog.ObservedAt.IsZero() || len(catalog.Baselines) > api.AutomationLiveBaselineLimit {
		data.BaselineCatalogError = "Baseline discovery is temporarily unavailable. You can still look up a known name below."
		return
	}
	seen := map[string]bool{}
	for _, entry := range catalog.Baselines {
		receipt := entry.Baseline
		if !baselineInScope(receipt, scope, data.Name, receipt.BaselineName) || !api.ValidAutomationLiveBaselineName(receipt.BaselineName) || seen[receipt.BaselineName] {
			data.BaselineCatalogError = "Baseline discovery returned inconsistent scope information."
			return
		}
		seen[receipt.BaselineName] = true
	}
	data.BaselineCatalog = &catalog
}
