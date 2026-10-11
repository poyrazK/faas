package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
)

func appendLifecycleGateFindings(decision *api.RouteGateDecision, before, after *RoutePolicyContract, at time.Time, successorReviewed bool) {
	var old, next []byte
	if before != nil && !before.Truncated {
		old = before.Doc
	}
	if after != nil && !after.Truncated {
		next = after.Doc
	}
	review := routelifecycle.Compare(old, next, at)
	seen := map[string]bool{}
	for _, reason := range decision.Reasons {
		seen[reason] = true
	}
	for _, finding := range review.Findings {
		if successorReviewed && finding.Code == "successor_changed_requires_review" {
			continue
		}
		reason := "lifecycle_" + finding.Code
		if !seen[reason] {
			decision.Reasons = append(decision.Reasons, reason)
			seen[reason] = true
		}
	}
	sort.Strings(decision.Reasons)
	if decision.Mode == "enforce" && len(decision.Reasons) > 0 {
		decision.Status = "blocked"
	}
}
