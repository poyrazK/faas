package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type platformTenantOffboardingSnapshot struct {
	AccountID                   string
	TenantID                    string
	Status                      string
	Consumers                   []platformTenantOffboardingConsumer
	Surfaces                    []platformTenantOffboardingSurface
	Hostnames                   []platformTenantOffboardingHostname
	ConsumerKeys                []platformTenantOffboardingCredential
	AccessTokens                []platformTenantOffboardingCredential
	CredentialScopes            []string
	MaxKeysPerConsumer          int
	CustomerProvisioningEnabled bool
	MaxConsumers                int
	AllowedHostnameSuffixes     []string
	MaxHostnames                int
}

type platformTenantOffboardingConsumer struct {
	ID      string
	Active  bool
	Managed bool
}

type platformTenantOffboardingSurface struct {
	ID      string
	Status  SurfaceStatus
	Managed bool
}

type platformTenantOffboardingHostname struct {
	ID        string
	SurfaceID string
	Hostname  string
	Managed   bool
}

type platformTenantOffboardingCredential struct {
	ID         string
	ConsumerID string
	AppID      string
	Active     bool
	Revoked    bool
	ExpiresAt  string
}

func buildPlatformTenantOffboardingPlan(snapshot platformTenantOffboardingSnapshot) (api.PlatformTenantOffboardingPlanResponse, error) {
	if snapshot.Consumers == nil {
		snapshot.Consumers = []platformTenantOffboardingConsumer{}
	}
	if snapshot.Surfaces == nil {
		snapshot.Surfaces = []platformTenantOffboardingSurface{}
	}
	if snapshot.Hostnames == nil {
		snapshot.Hostnames = []platformTenantOffboardingHostname{}
	}
	if snapshot.ConsumerKeys == nil {
		snapshot.ConsumerKeys = []platformTenantOffboardingCredential{}
	}
	if snapshot.AccessTokens == nil {
		snapshot.AccessTokens = []platformTenantOffboardingCredential{}
	}
	if snapshot.CredentialScopes == nil {
		snapshot.CredentialScopes = []string{}
	}
	if snapshot.AllowedHostnameSuffixes == nil {
		snapshot.AllowedHostnameSuffixes = []string{}
	}

	sort.Slice(snapshot.Consumers, func(i, j int) bool { return snapshot.Consumers[i].ID < snapshot.Consumers[j].ID })
	sort.Slice(snapshot.Surfaces, func(i, j int) bool { return snapshot.Surfaces[i].ID < snapshot.Surfaces[j].ID })
	sort.Slice(snapshot.Hostnames, func(i, j int) bool {
		if snapshot.Hostnames[i].Hostname != snapshot.Hostnames[j].Hostname {
			return snapshot.Hostnames[i].Hostname < snapshot.Hostnames[j].Hostname
		}
		return snapshot.Hostnames[i].ID < snapshot.Hostnames[j].ID
	})
	sort.Slice(snapshot.ConsumerKeys, func(i, j int) bool { return snapshot.ConsumerKeys[i].ID < snapshot.ConsumerKeys[j].ID })
	sort.Slice(snapshot.AccessTokens, func(i, j int) bool { return snapshot.AccessTokens[i].ID < snapshot.AccessTokens[j].ID })
	sort.Strings(snapshot.CredentialScopes)
	sort.Strings(snapshot.AllowedHostnameSuffixes)

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	digest := sha256.Sum256(encoded)
	actions := api.PlatformTenantOffboardingPlanActions{
		SuspendTenant:                 snapshot.Status == PlatformTenantActive,
		DisableCredentialDelegation:   len(snapshot.CredentialScopes) > 0 && snapshot.MaxKeysPerConsumer > 0,
		DisableCustomerProvisioning:   snapshot.CustomerProvisioningEnabled,
		DisableHostnameDelegation:     len(snapshot.AllowedHostnameSuffixes) > 0 && snapshot.MaxHostnames > 0,
		PreserveUsageHistory:          true,
		PreserveBillingStatements:     true,
		PreserveReconciliationHistory: true,
		PreserveWebhookSubscriptions:  true,
	}
	for _, consumer := range snapshot.Consumers {
		if consumer.Managed {
			actions.DetachManagedConsumers++
		} else {
			actions.RetainUnmanagedConsumers++
		}
	}
	for _, surface := range snapshot.Surfaces {
		if surface.Managed {
			actions.DetachManagedSurfaces++
		} else {
			actions.RetainUnmanagedSurfaces++
		}
	}
	for _, hostname := range snapshot.Hostnames {
		if hostname.Managed {
			actions.RemoveManagedHostnames++
		} else {
			actions.RetainUnmanagedHostnames++
		}
	}
	for _, credential := range snapshot.ConsumerKeys {
		if credential.Active {
			actions.RevokeConsumerKeys++
		}
	}
	for _, token := range snapshot.AccessTokens {
		if token.Active {
			actions.RevokeAccessTokens++
		}
	}
	return api.PlatformTenantOffboardingPlanResponse{TenantID: snapshot.TenantID, Status: snapshot.Status,
		PlanHash: hex.EncodeToString(digest[:]), Actions: actions}, nil
}
