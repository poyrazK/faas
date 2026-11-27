package wire

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const privateForwardKey = "gregale-private-forward"

type privateForwardContextKey struct{}

// WithPrivateForward marks an already-authorized platform service hop. HTTP
// headers cannot establish this identity. Public and legacy RPCs default to the
// metered route; only the authenticated gateway's private hop opts out.
func WithPrivateForward(ctx context.Context) context.Context {
	return context.WithValue(ctx, privateForwardContextKey{}, true)
}

func ForwardContext(ctx context.Context) context.Context {
	// FromOutgoingContext already returns a fresh copy; Copy() made a second.
	md, _ := metadata.FromOutgoingContext(ctx)
	if md == nil {
		md = metadata.MD{}
	}
	md.Delete(privateForwardKey)
	if private, _ := ctx.Value(privateForwardContextKey{}).(bool); private {
		md.Set(privateForwardKey, "1")
	}
	return metadata.NewOutgoingContext(ctx, md)
}

func IsPrivateForward(ctx context.Context) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(privateForwardKey)
	return len(values) == 1 && values[0] == "1"
}
