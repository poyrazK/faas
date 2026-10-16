package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type IssueApplicationResponseFilter struct {
	Status, AfterID string
	MyTeams         bool
}

func (f *IssueApplicationResponseFilter) Validate() error {
	if f.Status == "" {
		f.Status = "unacknowledged"
	}
	switch f.Status {
	case "all", "unacknowledged", "snoozed", "resurfaced", "escalated", "delivery_failed":
	default:
		return ErrInvalidArgument
	}
	if f.AfterID != "" {
		id, err := uuid.Parse(f.AfterID)
		if err != nil || id == uuid.Nil {
			return ErrInvalidArgument
		}
		f.AfterID = id.String()
	}
	return nil
}

type IssueApplicationResponseItem struct {
	IssueID, Environment                                                 string
	Revision                                                             int64
	Acknowledged, Snoozed, Resurfaced, Escalated                         bool
	SnoozedUntil, ReminderDueAt, EscalationDueAt                         *time.Time
	ResponsibleTeamID, ResponsibleTeamName, RoutedTeamID, RoutedTeamName string
	Outcome, DeliveryStatus                                              string
}
type IssueApplicationResponseInbox struct {
	Items  []IssueApplicationResponseItem
	NextID string
}
type IssueApplicationResponseInboxStore interface {
	ListIssueApplicationResponses(context.Context, string, string, string, IssueApplicationResponseFilter) (IssueApplicationResponseInbox, error)
}
