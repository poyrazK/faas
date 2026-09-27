package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

const PlatformTenantStatementFinalizedEvent = "platform_tenant.statement.finalized"

// PlatformTenantWebhookStore adds tenant-owned receivers to the shared
// durable webhook ledger. The webhook remains account-owned for quota and
// authorization, while the tenant ID scopes its event stream.
type PlatformTenantWebhookStore interface {
	CreatePlatformTenantWebhookIfUnderQuota(context.Context, AppWebhook, api.Limits) (AppWebhook, error)
	ListPlatformTenantWebhookDeliveries(context.Context, string, string, string, int, string) ([]AppWebhookDelivery, string, error)
}

func validPlatformTenantWebhookFilter(events []string) bool {
	return len(events) == 1 && events[0] == PlatformTenantStatementFinalizedEvent
}
