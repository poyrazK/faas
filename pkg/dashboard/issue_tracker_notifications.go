package dashboard

import "github.com/onebox-faas/faas/pkg/state"

type IssueTrackerNotificationsView struct {
	AppSlug, CSRF, NextURL, TeamFilter, InboxView          string
	CanManage, Saved, ReminderSaved                        bool
	FailureThresholdMax                                    int
	Policy                                                 state.IssueTrackerNotificationPolicy
	ReminderPolicy                                         state.IssueTrackerReminderPolicy
	ReminderIntervalMin, ReminderIntervalMax, RemindersMax int
	Items                                                  []state.IssueTrackerSyncNotification
	Reasons                                                map[string]string
}
