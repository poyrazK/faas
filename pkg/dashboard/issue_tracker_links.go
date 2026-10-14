package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type IssueTrackerLinksView struct {
	AppSlug, IssueID, CSRF, NextURL string
	Items                           []api.IssueTrackerLink
	CanManage                       bool
	LinearOAuthAvailable            bool
	CallbackHealth                  map[string]issuetracker.CallbackHealth
	Policies                        map[string]api.IssueTrackerAutoSync
	Reasons                         map[string]string
	LinearConnection                *api.IssueTrackerLinearConnection
}
