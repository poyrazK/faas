// adr: 375
package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/wire"
)

// requestForTarget keeps RPC dispatch, guest identity and correlation metadata
// on the same selected target. A retry must not inherit the original instance
// transport header or mutate the header map owned by an earlier attempt.
func requestForTarget(ctx context.Context, request *http.Request, app App, target Target) *http.Request {
	identity := target.PlatformIdentity(app.AccountID, requestIDFrom(request))
	identity.PlatformTenantID = authenticatedFrom(ctx).PlatformTenantID
	if identity.AppID == "" {
		identity.AppID = app.ID
	}
	attempt := request.Clone(wire.WithPlatformIdentity(ctx, identity))
	identity.ApplyGuestHeaders(attempt.Header)
	return attempt
}
