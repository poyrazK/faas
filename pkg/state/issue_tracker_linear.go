package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type IssueTrackerLinearRecord struct {
	AppID, AccountID                    string
	Connection                          api.IssueTrackerLinearConnection
	SealedCredential                    []byte
	CredentialGeneration                int64
	RefreshClaimID                      string
	RefreshClaimUntil, RefreshStartedAt time.Time
}
type IssueTrackerLinearStore interface {
	GetIssueTrackerLinearConnection(context.Context, string, string) (IssueTrackerLinearRecord, error)
	ConfigureIssueTrackerLinearConnection(context.Context, IssueTrackerLinearRecord, int64) (IssueTrackerLinearRecord, error)
}
