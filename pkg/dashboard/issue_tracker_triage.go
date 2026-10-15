package dashboard

import "github.com/onebox-faas/faas/pkg/state"

type IssueTrackerTriageView struct {
	AppSlug, CSRF string
	Triage        state.IssueTrackerTriage
	CanRelease    bool
}
