package dashboard

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/state"
)

type IssueTrackerReviewView struct {
	AppSlug, CSRF, Category, NextURL string
	Items                            []state.IssueTrackerReview
	Reasons                          map[string]string
	Recovery                         map[string]string
}

type IssueTrackerDeploymentSuggestion struct {
	state.IssueTrackerFixingDeployment
	Suggested bool
}

// SuggestFixingDeployments ranks evidence, not authority. No choice is preselected.
func SuggestFixingDeployments(choices []state.IssueTrackerFixingDeployment) []IssueTrackerDeploymentSuggestion {
	out := make([]IssueTrackerDeploymentSuggestion, 0, len(choices))
	for _, d := range choices {
		out = append(out, IssueTrackerDeploymentSuggestion{IssueTrackerFixingDeployment: d, Suggested: d.Status == "live" && !d.LastSeenAt.IsZero() && d.CreatedAt.After(d.LastSeenAt) && d.ObservedEvents == 0})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Suggested && !out[j].Suggested })
	return out
}
