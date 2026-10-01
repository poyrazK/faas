// adr: 375
package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
	"go.opentelemetry.io/otel/attribute"
)

// requestForTarget keeps RPC dispatch, guest identity and correlation metadata
// on the same selected target. A retry must not inherit the original instance
// transport header or mutate the header map owned by an earlier attempt.
func requestForTarget(ctx context.Context, request *http.Request, app App, target Target) *http.Request {
	identity := platformIdentityForTarget(ctx, request, app, target)
	attempt := request.Clone(wire.WithPlatformIdentity(ctx, identity))
	identity.ApplyGuestHeaders(attempt.Header)
	return attempt
}

func platformIdentityForTarget(ctx context.Context, request *http.Request, app App, target Target) api.PlatformIdentity {
	identity := target.PlatformIdentity(app.AccountID, requestIDFrom(request))
	identity.PlatformTenantID = authenticatedFrom(ctx).PlatformTenantID
	if identity.AppID == "" {
		identity.AppID = app.ID
	}
	return identity
}

// Empty provenance must overwrite initial span attributes after a replay.
// The ordinary identity attributes omit empty values at span creation.
func completionTargetAttributes(target Target) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("deployment_id", target.DeploymentID),
		attribute.String("instance_id", target.InstanceID),
		attribute.String("node_id", target.NodeID),
		attribute.String("region", target.Region),
		attribute.String("commit_sha", target.CommitSHA),
		attribute.String("deployment_tag", target.DeploymentTag),
		attribute.String("deployment_created_at", target.DeploymentCreatedAt),
		attribute.String("image_digest", target.ImageDigest),
	}
}
