package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrIssueCompoundAlertRevision = errors.New("issue compound alert policy changed")

type IssueCompoundAlertStore interface {
	GetIssueCompoundAlertPolicy(context.Context, string, string) (api.IssueCompoundAlertPolicy, error)
	SetIssueCompoundAlertPolicy(context.Context, string, string, api.UpdateIssueCompoundAlertPolicyRequest) (api.IssueCompoundAlertPolicy, error)
	PreviewIssueCompoundAlert(context.Context, string, string, api.PreviewIssueCompoundAlertRequest, time.Time) (api.IssueCompoundAlertPreview, error)
}
