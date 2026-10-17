package state

import "context"

type IssueApplicationEscalationPolicy struct {
	Enabled          bool
	Revision         int64
	DelayMinutes     int
	TeamID, TeamName string
}
type IssueApplicationEscalationStore interface {
	GetIssueApplicationEscalationPolicy(context.Context, string, string) (IssueApplicationEscalationPolicy, error)
	SetIssueApplicationEscalationPolicy(context.Context, string, string, IssueApplicationEscalationPolicy) (IssueApplicationEscalationPolicy, error)
	EnqueueIssueApplicationEscalation(context.Context) (bool, error)
}
