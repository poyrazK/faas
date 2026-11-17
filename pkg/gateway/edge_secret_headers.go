package gateway

import (
	"context"
	"net/http"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// ADR-20261110210000 slice 3i: with gatewayd-internal's
// FAAS_GATEWAY_EDGE_HEADER_SECRETS=inject, a sealed request header op keeps
// its reference (SecretAppID, SecretID) and no value. The gateway never
// opens it: applyEdgeRuleHeaders records the reference on the request and
// the forwarder names it in the init frame, where the instance's vmmd opens
// it through secretd and adds it after the request's own headers.

type edgeSecretHeadersKey struct{}

// recordEdgeSecretHeader notes one sealed request header op for the
// forwarder. A set op removes the client's own values now, so the request
// never carries them past the rule.
func recordEdgeSecretHeader(r *http.Request, op EdgeRuleHeaderOp) {
	action := "add"
	if op.Action == "set" {
		action = "set"
		r.Header.Del(op.Name)
	}
	refs := append(edgeSecretHeadersFrom(r.Context()), &vmmdpb.EdgeSecretHeader{
		Name: op.Name, Action: action, SecretAppId: op.SecretAppID, SecretId: op.SecretID})
	*r = *r.WithContext(context.WithValue(r.Context(), edgeSecretHeadersKey{}, refs))
}

// edgeSecretHeadersFrom returns the sealed request header ops a rule
// recorded on the request. The result is a fresh slice.
func edgeSecretHeadersFrom(ctx context.Context) []*vmmdpb.EdgeSecretHeader {
	if ctx == nil {
		return nil
	}
	refs, _ := ctx.Value(edgeSecretHeadersKey{}).([]*vmmdpb.EdgeSecretHeader)
	return append([]*vmmdpb.EdgeSecretHeader(nil), refs...)
}
