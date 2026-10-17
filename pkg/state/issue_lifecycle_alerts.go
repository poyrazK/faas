package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrIssueLifecycleAlertRevision = errors.New("issue lifecycle alert policy changed")

type IssueLifecycleAlertStore interface {
	GetIssueLifecycleAlertPolicy(context.Context, string, string) (api.IssueLifecycleAlertPolicy, error)
	SetIssueLifecycleAlertPolicy(context.Context, string, string, api.UpdateIssueLifecycleAlertPolicyRequest) (api.IssueLifecycleAlertPolicy, error)
	PreviewIssueLifecycleAlert(context.Context, string, string, api.PreviewIssueLifecycleAlertRequest, time.Time) (api.IssueLifecycleAlertPreview, error)
}
