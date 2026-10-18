package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func sealMCPFallbackSecret(_ context.Context, value string) ([]byte, *api.Problem) {
	if setSecretRecipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded")
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded")
	}
	sealed, err := secretbox.SealBytes(recipient, alertRuleSecretSealLabel, []byte(value), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		return nil, api.ErrCapacity("could not seal fallback signing secret")
	}
	return sealed, nil
}

func sealMCPPagerDutyKey(_ context.Context, value string) ([]byte, *api.Problem) {
	if setSecretRecipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded")
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded")
	}
	sealed, err := secretbox.SealBytes(recipient, api.MCPReadinessPagerDutySecretNamespace, []byte(value), api.MCPReadinessPagerDutyKeyBytes)
	if err != nil {
		return nil, api.ErrCapacity("could not seal PagerDuty integration key")
	}
	return sealed, nil
}

func sealMCPPagerDutyWebhookSecret(_ context.Context, value string) ([]byte, *api.Problem) {
	if setSecretRecipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded")
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded")
	}
	sealed, err := secretbox.SealBytes(recipient, api.MCPReadinessPagerDutyWebhookSecretNamespace, []byte(value), api.MCPReadinessPagerDutyWebhookSecretMaxBytes)
	if err != nil {
		return nil, api.ErrCapacity("could not seal PagerDuty webhook signing secret")
	}
	return sealed, nil
}
