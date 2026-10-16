package state

import (
	"context"
	"time"
)

type IssueApplicationReminderPolicy struct {
	Enabled                      bool
	Revision                     int64
	IntervalMinutes, RepeatLimit int
}
type IssueApplicationReminder struct {
	ID, Kind, ResponseID, Outcome, DeliveryStatus, TeamName string
	Sequence                                                int
	CreatedAt                                               time.Time
}
type IssueApplicationReminderStore interface {
	GetIssueApplicationReminderPolicy(context.Context, string, string) (IssueApplicationReminderPolicy, error)
	SetIssueApplicationReminderPolicy(context.Context, string, string, IssueApplicationReminderPolicy) (IssueApplicationReminderPolicy, error)
	EnqueueIssueApplicationReminder(context.Context) (bool, error)
}
