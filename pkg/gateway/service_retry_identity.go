// adr: 531
package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Each attempt owns its identity map even when replayRequest returns the
// bodyless source unchanged. Authorization supplies the account; the endpoint
// supplies target provenance. Neither is inherited from caller claims.
func requestForServiceEndpoint(ctx context.Context, request *http.Request, target ServiceTarget, endpoint ServiceEndpoint, caller ServiceCaller) *http.Request {
	identity := serviceEndpointTarget(target.AppID, endpoint).PlatformIdentity(caller.AccountID, request.Header.Get(api.RequestIDHeader))
	if inherited, ok := ctx.Value(serviceFlagPropagationContextKey{}).(flags.PropagationContext); ok {
		identity.PlatformTenantID = inherited.CustomerID
	}
	attempt := request.Clone(wire.WithPlatformIdentity(ctx, identity))
	applyServiceEndpointIdentity(attempt, target, endpoint, caller)
	return attempt
}
