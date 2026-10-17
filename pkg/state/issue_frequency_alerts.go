package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrIssueFrequencyAlertRevision = errors.New("issue frequency alert policy changed")

type IssueFrequencyAlertStore interface {
	GetIssueFrequencyAlertPolicy(context.Context, string, string) (api.IssueFrequencyAlertPolicy, error)
	SetIssueFrequencyAlertPolicy(context.Context, string, string, api.UpdateIssueFrequencyAlertPolicyRequest) (api.IssueFrequencyAlertPolicy, error)
}
