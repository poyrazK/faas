package state

import (
	"context"
	"time"
)

// IssueApplicationTriage is response state for a canonical issue. It does not
// grant access, change assignment, or alter issue capture/resolution.
type IssueApplicationTriage struct {
	EscalatedAt                                                                  *time.Time
	ResponsibleTeamID, ResponsibleTeamName                                       string
	Escalations                                                                  []IssueApplicationReminder
	IssueID, IncidentID, Environment, TeamID, TeamName, AssigneeID, AssigneeName string
	Revision                                                                     int64
	Active, Snoozed, Resurfaced                                                  bool
	AcknowledgedBy, AcknowledgedName                                             string
	AcknowledgedAt                                                               *time.Time
	SnoozedUntil                                                                 *time.Time
	SnoozedBy, SnoozedName, SnoozeReason                                         string
	History                                                                      []IssueApplicationTriageActivity
	Reminders                                                                    []IssueApplicationReminder
}
type IssueApplicationTriageActivity struct {
	IncidentID, ActorID, ActorName, Action, Reason string
	Revision                                       int64
	CreatedAt                                      time.Time
	SnoozedUntil                                   *time.Time
}
type IssueApplicationTriageAction struct {
	Action       string
	SnoozedUntil *time.Time
	Reason       string
}
type IssueApplicationTriageStore interface {
	GetIssueApplicationTriage(context.Context, string, string, string, string) (IssueApplicationTriage, error)
	UpdateIssueApplicationTriage(context.Context, string, string, string, string, int64, IssueApplicationTriageAction) error
}
