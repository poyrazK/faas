package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

const PlatformTenantStatementFinalizedEvent = "platform_tenant.statement.finalized"
const PlatformTenantHostnameVerifiedEvent = "platform_tenant.hostname.verified"
const PlatformTenantSurfaceCertificateChangedEvent = "platform_tenant.surface.certificate.changed"
const PlatformTenantSurfaceDeploymentChangedEvent = "platform_tenant.surface.deployment.changed"

// PlatformTenantWebhookStore adds tenant-owned receivers to the shared
// durable webhook ledger. The webhook remains account-owned for quota and
// authorization, while the tenant ID scopes its event stream.
type PlatformTenantWebhookStore interface {
	CreatePlatformTenantWebhookIfUnderQuota(context.Context, AppWebhook, api.Limits) (AppWebhook, error)
	ListPlatformTenantWebhookDeliveries(context.Context, string, string, string, int, string) ([]AppWebhookDelivery, string, error)
}

// ValidPlatformTenantWebhookFilter accepts the closed platform-tenant
// webhook vocabulary and rejects empty, duplicate, or unknown subscriptions.
func ValidPlatformTenantWebhookFilter(events []string) bool {
	if len(events) == 0 || len(events) > 4 {
		return false
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		switch event {
		case PlatformTenantStatementFinalizedEvent, PlatformTenantHostnameVerifiedEvent,
			PlatformTenantSurfaceCertificateChangedEvent, PlatformTenantSurfaceDeploymentChangedEvent:
		default:
			return false
		}
		if _, ok := seen[event]; ok {
			return false
		}
		seen[event] = struct{}{}
	}
	return true
}
