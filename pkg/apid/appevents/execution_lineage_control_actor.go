package appevents

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func lineageControlActorContext(ctx context.Context, r *http.Request) context.Context {
	principal, ok := httpkit.PrincipalFrom(r)
	if !ok {
		return ctx
	}
	kind, id, credential := "account", principal.Acct.ID, "session"
	if principal.Key != nil {
		kind, id, credential = "credential", principal.Key.ID, "api_key"
		if principal.Key.PlatformTenantID != "" {
			credential = "platform_tenant_token"
		} else if principal.Key.AppID != "" {
			credential = "deploy_token"
		}
	}
	return state.WithEventExecutionLineageControlActor(ctx, principal.Acct.ID, kind, id, credential)
}
