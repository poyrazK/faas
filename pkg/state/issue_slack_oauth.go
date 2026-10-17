package state

import (
	"context"
	"time"
)

type IssueSlackInstallation struct {
	AppID, AccountID                                          string
	Revision                                                  int64
	Status, WorkspaceID, WorkspaceName, SlackAppID, BotUserID string
	InstalledAt                                               time.Time
	TokenSealed                                               []byte `json:"-"`
}
type IssueSlackOAuthFlow struct {
	AppID, AccountID, Hash, Phase string
	ExpectedRevision              int64
	Sealed                        []byte `json:"-"`
	ExpiresAt                     time.Time
}
type IssueSlackOAuthStore interface {
	GetIssueSlackInstallation(context.Context, string, string) (IssueSlackInstallation, error)
	IssueSlackInstallationCredential(context.Context, string, string) (IssueSlackInstallation, error)
	StartIssueSlackOAuth(context.Context, IssueSlackOAuthFlow) error
	ClaimIssueSlackOAuth(context.Context, string, string) (IssueSlackOAuthFlow, error)
	SaveIssueSlackOAuth(context.Context, IssueSlackOAuthFlow, []byte) error
	GetIssueSlackOAuth(context.Context, string, string) (IssueSlackOAuthFlow, error)
	FinishIssueSlackOAuth(context.Context, IssueSlackOAuthFlow, IssueSlackInstallation, []byte) error
	DisconnectIssueSlackOAuth(context.Context, string, string, int64) error
	SetIssueSlackOAuthDestination(context.Context, string, string, PutIssueSlackDestination, int64) error
	RevokeIssueSlackOAuth(context.Context, string, string, string, time.Time) error
	IssueSlackOAuthActionApp(context.Context, string, string, string) (string, error)
}
