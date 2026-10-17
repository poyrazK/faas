package state

import (
	"context"
	"errors"
	"regexp"
	"time"
)

const IssueSlackTokenNamespace = "issue-team-slack-bot-token"

var ErrIssueSlackClaimLost = errors.New("slack delivery claim changed")
var issueSlackChannel = regexp.MustCompile(`^[CG][A-Z0-9]{8,31}$`)
var issueSlackTimestamp = regexp.MustCompile(`^[0-9]{1,20}\.[0-9]{1,10}$`)

func ValidIssueSlackChannel(s string) bool   { return issueSlackChannel.MatchString(s) }
func ValidIssueSlackTimestamp(s string) bool { return issueSlackTimestamp.MatchString(s) }

type IssueSlackDestination struct {
	ID, TeamID, TeamName, ChannelID string
	Enabled                         bool
	Revision                        int64
	OAuthRevision                   int64
}
type PutIssueSlackDestination struct {
	TeamID, ChannelID string
	Enabled           bool
	Revision          int64
	TokenSealed       []byte `json:"-"`
}
type IssueSlackDelivery struct {
	RoutingRuleKey                                                                                                string
	RoutingRevision                                                                                               int64
	ID, NotificationID, ActivityID, ReminderID, IssueID, IncidentID, TeamName, ChannelID, Kind, Status, LastError string
	CanRetry                                                                                                      bool
	Attempts, DestinationRevision                                                                                 int64
	ResponseCode                                                                                                  int
	NextAttemptAt, CreatedAt, UpdatedAt                                                                           time.Time
}
type IssueSlackClaim struct {
	RoutingRuleKey                                                                                                                                        string
	RoutingRevision                                                                                                                                       int64
	ID, AppID, AccountID, DestinationID, NotificationID, IncidentID, IssueID, LinkID, AppSlug, TeamID, ChannelID, Kind, Reason, ThreadTS, ClaimID, Status string
	Application                                                                                                                                           bool
	Interactive                                                                                                                                           bool
	CanAssign                                                                                                                                             bool
	Details                                                                                                                                               map[string]string
	DestinationRevision                                                                                                                                   int64
	Attempt                                                                                                                                               int
	TokenSealed                                                                                                                                           []byte `json:"-"`
}
type IssueSlackResult struct {
	Status, Error, MessageTS string
	ResponseCode             int
	RetryAt                  time.Time
	ResetThread              bool
}
type IssueSlackStore interface {
	ListIssueSlackDestinations(context.Context, string, string) ([]IssueSlackDestination, error)
	SetIssueSlackDestination(context.Context, string, string, PutIssueSlackDestination) (IssueSlackDestination, error)
	ListIssueSlackDeliveries(context.Context, string, string) ([]IssueSlackDelivery, error)
	RetryIssueSlackDelivery(context.Context, string, string, string) error
	ClaimIssueSlackDelivery(context.Context) (IssueSlackClaim, bool, error)
	IssueSlackDeliveryReady(context.Context, IssueSlackClaim) (bool, error)
	FinishIssueSlackDelivery(context.Context, IssueSlackClaim, IssueSlackResult) error
	CleanupIssueSlackThreads(context.Context) error
}
