package appevents

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func lineageControlActorRequest(r *http.Request) *http.Request {
	principal, ok := httpkit.PrincipalFrom(r)
	if !ok {
		return r
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
	return r.WithContext(state.WithEventExecutionLineageControlActor(r.Context(), principal.Acct.ID, kind, id, credential))
}
