package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type IssueTrackerLinksView struct {
	AppSlug, IssueID, CSRF, NextURL string
	Items                           []api.IssueTrackerLink
	CanManage                       bool
	Policies                        map[string]api.IssueTrackerAutoSync
	Reasons                         map[string]string
	LinearConnection                *api.IssueTrackerLinearConnection
}
