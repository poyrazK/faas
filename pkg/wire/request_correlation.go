// adr: 570
package wire

import (
	"context"

	"google.golang.org/grpc/metadata"
)

// WithRequestCorrelationOutgoing publishes the current complete request
// envelope. Reserved keys replace prior-hop values; unrelated transport
// metadata remains intact. Callers without a canonical envelope are unchanged.
func WithRequestCorrelationOutgoing(ctx context.Context) context.Context {
	fields, ok := FromContext(ctx)
	if !ok {
		return ctx
	}
	previous, _ := metadata.FromOutgoingContext(ctx)
	md := previous.Copy()
	for _, key := range []string{mdKeyRequestID, mdKeyWakeID, mdKeyAppID, mdKeyDeploymentID,
		mdKeyInstanceID, mdKeyNodeID, mdKeyInvocationID, mdKeyTenantID, mdKeyRegion,
		mdKeyCommitSHA, mdKeyDeploymentTag, mdKeyDeploymentCreatedAt, mdKeyImageDigest,
		mdKeyTraceID, mdKeySpanID, mdKeyWakeBootTrigger, mdKeyWakeTriggerClass, mdKeyWakeBootQueued, mdKeyWakeBootConc} {
		delete(md, key)
	}
	return WithCorrelationOutgoing(metadata.NewOutgoingContext(ctx, md), fields)
}
