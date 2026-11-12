package gateway

import (
	"context"

	"golang.org/x/net/http/httpguts"

	"github.com/onebox-faas/faas/pkg/api"
)

// EdgeJWTIdentity is the caller a kind=jwt edge rule verified: the token's
// issuer and subject (ADR-091 amendment 2026-11-04, round 11). The gateway
// hands it to the app as X-Faas-Edge-Jwt-Issuer / X-Faas-Edge-Jwt-Subject.
// An async route stores it on the invocation, since the queued request keeps
// no Authorization header and a token would have expired by delivery anyway.
type EdgeJWTIdentity struct {
	Issuer  string
	Subject string
}

type edgeJWTIdentityKey struct{}

// WithEdgeJWTIdentity records the verified caller. An identity without a
// subject, or with a value HTTP cannot carry, records nothing.
func WithEdgeJWTIdentity(ctx context.Context, id EdgeJWTIdentity) context.Context {
	if !validEdgeJWTIdentityValue(id.Subject) || (id.Issuer != "" && !validEdgeJWTIdentityValue(id.Issuer)) {
		return ctx
	}
	return context.WithValue(ctx, edgeJWTIdentityKey{}, id)
}

// EdgeJWTIdentityFrom returns the caller WithEdgeJWTIdentity recorded.
func EdgeJWTIdentityFrom(ctx context.Context) (EdgeJWTIdentity, bool) {
	if ctx == nil {
		return EdgeJWTIdentity{}, false
	}
	id, ok := ctx.Value(edgeJWTIdentityKey{}).(EdgeJWTIdentity)
	return id, ok
}

func validEdgeJWTIdentityValue(v string) bool {
	return v != "" && len(v) <= api.MaxEdgeJWTIdentityBytes && httpguts.ValidHeaderFieldValue(v)
}

// guestEdgeJWTHeaders are the identity headers the guest receives. They come
// only from the request context: a client's own X-Faas-Edge-Jwt-* headers are
// x-faas-* headers outside guestReceivesFaasHeader and never cross.
func guestEdgeJWTHeaders(ctx context.Context) [][2]string {
	id, ok := EdgeJWTIdentityFrom(ctx)
	if !ok {
		return nil
	}
	out := [][2]string{{api.EdgeJWTSubjectHeader, id.Subject}}
	if id.Issuer != "" {
		out = append(out, [2]string{api.EdgeJWTIssuerHeader, id.Issuer})
	}
	return out
}
