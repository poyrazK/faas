package dashboard

import "github.com/onebox-faas/faas/pkg/state"

type IssueTrackerNotificationsView struct {
	AppSlug, CSRF, NextURL, TeamFilter, InboxView string
	CanManage, Saved                              bool
	FailureThresholdMax                           int
	Policy                                        state.IssueTrackerNotificationPolicy
	Items                                         []state.IssueTrackerSyncNotification
	Reasons                                       map[string]string
}
