package state

import (
	"context"
	"regexp"
	"time"
)

const IssueSlackSigningSecretNamespace = "issue-slack-signing-secret"

var slackWorkspace = regexp.MustCompile(`^T[A-Z0-9]{8,31}$`)
var slackApp = regexp.MustCompile(`^A[A-Z0-9]{8,31}$`)
var slackUser = regexp.MustCompile(`^[UW][A-Z0-9]{8,31}$`)
var slackLinkToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidIssueSlackUser(s string) bool { return slackUser.MatchString(s) }

type IssueSlackInteractionPolicy struct {
	Enabled                 bool
	Revision                int64
	WorkspaceID, SlackAppID string
	SecretSealed            []byte `json:"-"`
}
type IssueSlackInteractionRequest struct {
	ResponseURL                                                  string `json:"-"`
	WorkspaceID                                                  string
	DeliveryID, ChannelID, MessageTS, UserID, Action, RequestKey string
	PolicyRevision                                               int64
}
type IssueSlackInteractionResult struct {
	Status, AppSlug, IssueID string
	LinkToken                string `json:"-"`
}
type IssueSlackAccountLink struct {
	UserID, WorkspaceID, ChallengeUserID, ChallengeWorkspaceID string
	ExpiresAt                                                  *time.Time
	ChallengeToken                                             string `json:"-"`
}
type IssueSlackInteractionStore interface {
	GetIssueSlackInteractionPolicy(context.Context, string, string) (IssueSlackInteractionPolicy, error)
	SetIssueSlackInteractionPolicy(context.Context, string, string, IssueSlackInteractionPolicy) (IssueSlackInteractionPolicy, error)
	IssueSlackInteractionVerifier(context.Context, string) (IssueSlackInteractionPolicy, error)
	PrepareIssueSlackActions(context.Context, IssueSlackClaim) (bool, error)
	ApplyIssueSlackInteraction(context.Context, string, IssueSlackInteractionRequest) (IssueSlackInteractionResult, error)
	GetIssueSlackAccountLink(context.Context, string, string, string, string, string) (IssueSlackAccountLink, error)
	ConfirmIssueSlackAccountLink(context.Context, string, string, string, string, string) error
	DeleteIssueSlackAccountLink(context.Context, string, string) error
}
