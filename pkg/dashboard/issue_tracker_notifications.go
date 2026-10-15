package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type IssueTrackerNotificationsView struct {
	ApplicationSlackAvailable, ApplicationSlackSaved       bool
	ApplicationSlackPolicy                                 state.IssueApplicationSlackPolicy
	ApplicationSlackEvents                                 []IssueSlackEventChoice
	ApplicationSlackMaxDeliveries                          int
	SlackTeams                                             []IssueSlackTeamSettings
	SlackDeliveries                                        []state.IssueSlackDelivery
	SlackSaved                                             bool
	SlackTokenMax                                          int
	AppSlug, CSRF, NextURL, TeamFilter, InboxView          string
	CanManage, Saved, ReminderSaved, EscalationSaved       bool
	FailureThresholdMax                                    int
	EscalationPolicy                                       state.IssueTrackerEscalationPolicy
	Teams                                                  []api.IssueTeam
	EscalationDelayMin, EscalationDelayMax                 int
	Policy                                                 state.IssueTrackerNotificationPolicy
	ReminderPolicy                                         state.IssueTrackerReminderPolicy
	ReminderIntervalMin, ReminderIntervalMax, RemindersMax int
	Items                                                  []state.IssueTrackerSyncNotification
	Reasons                                                map[string]string
}

type IssueSlackTeamSettings struct {
	TeamID, TeamName string
	Destination      state.IssueSlackDestination
}

type IssueSlackEventChoice struct {
	Key, Label string
	Selected   bool
}
