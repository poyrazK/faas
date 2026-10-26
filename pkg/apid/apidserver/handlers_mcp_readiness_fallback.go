package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// sealMCPFallbackSecret and sealMCPPagerDutyKey seal what meterd opens to
// deliver MCP readiness incidents: the alerting domain (ADR 20261022111812).
func sealMCPFallbackSecret(_ context.Context, value string) ([]byte, *api.Problem) {
	recipients, err := alertingCredentialRecipients()
	if err != nil {
		return nil, api.ErrCapacity("alerting recipient not loaded")
	}
	sealed, err := secretbox.SealBytesTo(recipients, alertRuleSecretSealLabel, []byte(value), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		return nil, api.ErrCapacity("could not seal fallback signing secret")
	}
	return sealed, nil
}

func sealMCPPagerDutyKey(_ context.Context, value string) ([]byte, *api.Problem) {
	recipients, err := alertingCredentialRecipients()
	if err != nil {
		return nil, api.ErrCapacity("alerting recipient not loaded")
	}
	sealed, err := secretbox.SealBytesTo(recipients, api.MCPReadinessPagerDutySecretNamespace, []byte(value), api.MCPReadinessPagerDutyKeyBytes)
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
