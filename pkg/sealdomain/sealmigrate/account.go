package sealmigrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// accountBatch is small because a workflow plan runs to 4 MiB.
const accountBatch = 32

// AccountStore is the slice of *state.PgStore the account re-seal uses.
type AccountStore interface {
	ListAccountSealedValues(ctx context.Context, kind, afterID string, limit int) ([]state.AccountSealedValue, error)
	ResealAccountValue(ctx context.Context, v state.AccountSealedValue, sealed []byte, kid string) (bool, error)
	CountAccountCloneValuesOffDomain(ctx context.Context, kid string) (int, error)
}

// accountKinds are the account values the re-seal moves to the domain.
// Project environment clone values are not among them: clone records bind
// each other by ciphertext hash, so apid seals new ones to the domain once
// it is domain-only and the old ones stay on the fleet key until their
// clones finish. The re-seal counts them as Skipped.
var accountKinds = []string{
	state.AccountSealedMFASecret,
	state.AccountSealedInboundWebhook,
	state.AccountSealedBillingKey,
	state.AccountSealedWorkflowPlan,
	state.AccountSealedPagerDutyAck,
	state.AccountSealedGitLabOAuth,
	state.AccountSealedSlackInstallation,
	state.AccountSealedSlackInteraction,
	state.AccountSealedSlackDestination,
	state.AccountSealedSlackOAuthFlow,
	state.AccountSealedLinearConnection,
	state.AccountSealedLinearOAuthFlow,
	state.AccountSealedProductEmailGrant,
	state.AccountSealedProductEmailPause,
	state.AccountSealedProductEmailRecipient,
	state.AccountSealedProductEmailUnsubscribe,
	state.AccountSealedRealtimeCallback,
	state.AccountSealedRealtimeAuth,
	state.AccountSealedRealtimeAuthPrevious,
	state.AccountSealedRealtimePushDevice,
	state.AccountSealedRealtimePushProvider,
}

// MigrateAccount re-seals every account TOTP secret, inbound webhook
// signing secret, billing provider key, pending workflow plan, PagerDuty
// acknowledgement signing secret, integration credential (GitLab, Slack,
// Linear, and their pending consent flows), product email value and managed
// realtime credential to the account domain recipient alone
// (ADR 20261022111812, step 3), and files each plan under the domain's key
// id. legacy are the fleet and host identities that open values sealed
// before the domain. Run it only after every apid holds the domain
// identity: a re-sealed value no longer opens with the fleet identity.
func MigrateAccount(ctx context.Context, store AccountStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: account migration needs the store, the domain identity and the legacy identities")
	}
	kid := domain.Recipient().String()
	report := MigrationReport{Domain: sealdomain.Account, Target: kid}
	for _, kind := range accountKinds {
		afterID := ""
		for {
			page, err := store.ListAccountSealedValues(ctx, kind, afterID, accountBatch)
			if err != nil {
				return report, err
			}
			for _, v := range page {
				report.Scanned++
				afterID = v.ID
				sealed, outcome, err := resealAccountValue(v, domain, legacy)
				if err != nil {
					return report, fmt.Errorf("sealmigrate: reseal account %s %s: %w", v.Kind, v.ID, err)
				}
				replaced := false
				if outcome == resealWrite {
					if replaced, err = store.ResealAccountValue(ctx, v, sealed, kid); err != nil {
						return report, err
					}
				}
				report.count(outcome, replaced)
			}
			if len(page) < accountBatch {
				break
			}
		}
	}
	clones, err := store.CountAccountCloneValuesOffDomain(ctx, kid)
	if err != nil {
		return report, err
	}
	report.Skipped += clones
	return report, nil
}

func resealAccountValue(v state.AccountSealedValue, domain *age.X25519Identity, legacy []*age.X25519Identity) ([]byte, resealOutcome, error) {
	switch v.Kind {
	case state.AccountSealedMFASecret:
		return resealEnvelope(v.Sealed, domain, legacy, func(env secretbox.Envelope) bool {
			return len(env) == 1 && env["MFA_SECRET"] != ""
		})
	case state.AccountSealedInboundWebhook:
		return resealBlob(v.Sealed, api.InboundWebhookSecretNamespace, api.InboundWebhookSigningSecretMaxBytes, domain, legacy)
	case state.AccountSealedBillingKey:
		account, provider, ok := strings.Cut(v.ID, "/")
		if !ok {
			return nil, resealFailed, nil
		}
		return resealBlob(v.Sealed, api.BillingProviderKeyNamespace(provider, account), api.BillingProviderKeyMaxBytes, domain, legacy)
	case state.AccountSealedWorkflowPlan:
		return resealBlob(v.Sealed, executionpayload.WorkflowPlanNamespace, api.ExecutionWorkflowManagedPlanMaxBytes, domain, legacy)
	case state.AccountSealedPagerDutyAck:
		return resealBlob(v.Sealed, api.MCPReadinessPagerDutyWebhookSecretNamespace, api.MCPReadinessPagerDutyWebhookSecretMaxBytes, domain, legacy)
	case state.AccountSealedGitLabOAuth:
		return resealBlob(v.Sealed, api.GitLabOAuthSecretNamespace, api.GitLabOAuthSecretMaxBytes, domain, legacy)
	case state.AccountSealedSlackInstallation, state.AccountSealedSlackDestination:
		return resealBlob(v.Sealed, state.IssueSlackTokenNamespace, api.IssueSlackTokenMaxBytes, domain, legacy)
	case state.AccountSealedSlackInteraction:
		return resealBlob(v.Sealed, state.IssueSlackSigningSecretNamespace, api.IssueSlackSigningSecretBytes, domain, legacy)
	case state.AccountSealedSlackOAuthFlow:
		return resealBlob(v.Sealed, api.IssueSlackOAuthFlowNamespace, api.IssueSlackOAuthSealedMaxBytes, domain, legacy)
	case state.AccountSealedLinearConnection:
		// A connection holds an API key credential or an OAuth credential.
		return resealBlobInNamespace(v.Sealed, []string{api.IssueTrackerLinearCredentialNamespace, api.IssueTrackerLinearOAuthNamespace}, 0, domain, legacy)
	case state.AccountSealedLinearOAuthFlow:
		return resealBlob(v.Sealed, api.IssueTrackerLinearOAuthFlowNamespace, 0, domain, legacy)
	case state.AccountSealedProductEmailGrant, state.AccountSealedProductEmailPause,
		state.AccountSealedProductEmailRecipient, state.AccountSealedProductEmailUnsubscribe:
		// A product email value binds its token to the subscription or
		// evidence id it was sealed for.
		return resealEnvelope(v.Sealed, domain, legacy, func(env secretbox.Envelope) bool {
			return len(env) == 2 && env["PRODUCT_EMAIL_SUBSCRIPTION"] != "" && env["PRODUCT_EMAIL_RECIPIENT"] != ""
		})
	case state.AccountSealedRealtimeCallback:
		return resealBlob(v.Sealed, api.RealtimeCallbackAuthNamespace, api.RealtimeCallbackAuthTokenMaxBytes, domain, legacy)
	case state.AccountSealedRealtimeAuth, state.AccountSealedRealtimeAuthPrevious:
		return resealBlob(v.Sealed, api.RealtimeAuthTokenNamespace, api.RealtimeCallbackAuthTokenMaxBytes, domain, legacy)
	case state.AccountSealedRealtimePushDevice:
		return resealBlob(v.Sealed, api.RealtimePushDeviceNamespacePrefix+v.ID, api.RealtimePushTargetSealMaxBytes, domain, legacy)
	case state.AccountSealedRealtimePushProvider:
		return resealBlob(v.Sealed, api.RealtimePushProviderNamespacePrefix+v.ID, api.RealtimePushConfigSealMaxBytes, domain, legacy)
	}
	return nil, resealFailed, fmt.Errorf("unknown account value kind %q", v.Kind)
}

// resealEnvelope is resealBlob for a secretbox.Seal envelope: it opens the
// envelope with the legacy identities and re-seals its keys to the domain
// alone. valid rejects an envelope that is not the column's kind.
func resealEnvelope(blob []byte, domain *age.X25519Identity, legacy []*age.X25519Identity, valid func(secretbox.Envelope) bool) ([]byte, resealOutcome, error) {
	env, err := secretbox.OpenMulti(legacy, blob)
	if err != nil {
		if _, domainErr := secretbox.OpenMulti([]*age.X25519Identity{domain}, blob); domainErr == nil {
			return nil, resealCurrent, nil
		}
		return nil, resealFailed, nil
	}
	defer clear(env)
	if !valid(env) {
		return nil, resealFailed, nil
	}
	sealed, err := secretbox.SealTo([]*age.X25519Recipient{domain.Recipient()}, env)
	if err != nil {
		return nil, resealFailed, err
	}
	return sealed, resealWrite, nil
}
