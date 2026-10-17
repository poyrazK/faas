package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type IssueTrackerSyncHealthView struct {
	CanManage                                         bool
	AppSlug, IssueID, LinkID, Provider, CSRF, Summary string
	Policy                                            api.IssueTrackerAutoSync
	Health                                            state.IssueTrackerSyncHealth
	Reasons                                           map[string]string
}

func NewIssueTrackerSyncHealthView(app, issue, link, provider, csrf string, policy api.IssueTrackerAutoSync, health state.IssueTrackerSyncHealth) IssueTrackerSyncHealthView {
	v := IssueTrackerSyncHealthView{AppSlug: app, IssueID: issue, LinkID: link, Provider: provider, CSRF: csrf, Policy: policy, Health: health, Reasons: map[string]string{}, Summary: IssueTrackerRecoverySummary(policy.Enabled, policy.Outcome, policy.PendingState)}
	for _, entry := range health.History {
		reason := IssueTrackerSyncReason(entry.Reason)
		if entry.EventType == "inspected" && entry.ObservedState != "" {
			reason = IssueTrackerInboundReason(entry.Reason)
		}
		v.Reasons[entry.ID] = reason
	}
	return v
}

func IssueTrackerRecoverySummary(enabled bool, outcome, pending string) string {
	if !enabled {
		return "Automatic sync is disabled. Inspect both lifecycles and review a fresh aligned baseline before enabling."
	}
	if outcome == "blocked" || outcome == "conflict" {
		return "Paused for review. A fresh inspection does not resume this policy. Review the connection, issue intent and aligned lifecycles before re-enabling."
	}
	if pending != "" {
		return "Unconfirmed write. Automatic checks observe its outcome without sending it again. Inspect the ticket before resetting delegation."
	}
	if outcome == "error" {
		return "Retryable check failure. Automatic checks remain scheduled. Check now to inspect provider status without changing delegation."
	}
	return "Automatic checks are scheduled. A fresh inspection does not change the saved sync baseline."
}
