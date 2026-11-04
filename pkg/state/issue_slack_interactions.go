package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

const IssueSlackSigningSecretNamespace = "issue-slack-signing-secret"

var slackWorkspace = lazyregexp.New(`^T[A-Z0-9]{8,31}$`)
var slackApp = lazyregexp.New(`^A[A-Z0-9]{8,31}$`)
var slackUser = lazyregexp.New(`^[UW][A-Z0-9]{8,31}$`)
var slackLinkToken = lazyregexp.New(`^[0-9a-f]{64}$`)

// slackLinkTokenHash is what issue_slack_link_challenges.token stores for a
// link token: the hex SHA-256 of its 32 random bytes, never the token, so a
// database reader cannot link someone's Slack identity to their own account.
// token must match slackLinkToken.
func slackLinkTokenHash(token string) string {
	raw, _ := hex.DecodeString(token)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// slackOAuthStateHash is a stored OAuth state hash: a hex SHA-256, or a
// keyed digest (ADR 20261029202403).
var slackOAuthStateHash = lazyregexp.New(`^[0-9a-f]{64}([0-9a-f]{2})?$`)

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
