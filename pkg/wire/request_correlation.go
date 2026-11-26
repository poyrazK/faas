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
	// FromOutgoingContext already returns a fresh copy (grpc-go copies every
	// value), so it is edited in place and attached once, with the envelope
	// appended exactly as WithCorrelationOutgoing would append it.
	md, _ := metadata.FromOutgoingContext(ctx)
	if md == nil {
		md = metadata.MD{}
	}
	for _, key := range []string{mdKeyRequestID, mdKeyWakeID, mdKeyAppID, mdKeyDeploymentID,
		mdKeyInstanceID, mdKeyNodeID, mdKeyInvocationID, mdKeyTenantID, mdKeyRegion,
		mdKeyCommitSHA, mdKeyDeploymentTag, mdKeyDeploymentCreatedAt, mdKeyImageDigest,
		mdKeyTraceID, mdKeySpanID, mdKeyWakeBootTrigger, mdKeyWakeTriggerClass, mdKeyWakeBootQueued, mdKeyWakeBootConc} {
		delete(md, key)
	}
	appendMDPairs(md, correlationPairs(fields))
	return metadata.NewOutgoingContext(ctx, md)
}
