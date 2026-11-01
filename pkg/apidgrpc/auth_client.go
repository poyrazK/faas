// Package apidgrpc — auth client (ADR-127 PR-D).
//
// gatewayd-public dials apid's Auth service over the unix socket
// to translate a Bearer token into the matching (account_id, plan)
// tuple so the OTel spans writer can enforce per-plan telemetry
// caps without holding a direct Postgres connection (CLAUDE.md
// ownership: apid is the sole writer to api_keys; gatewayd-public
// never opens a direct connection for this path).
//
// The client surface mirrors RequestTelemetryClientImpl (PR-B):
// same dial pattern, same pkg/wire.DialContext, same lazy-conn
// model. The shape difference: Auth is a unary RPC (token lookup
// is rare on the hot path — the gateway caches the result for
// the duration of one OTLP POST envelope).

package apidgrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// AuthClient is the gatewayd-public-side interface the OTel
// handler holds. The interface is the union of every gRPC call
// gatewayd-public makes against apid's Auth service.
type AuthClient interface {
	// AuthenticateKey resolves a bearer token to the principal its
	// spans are written for. The token is the raw value from the
	// customer's OTLP POST Authorization header (i.e. the value
	// AFTER `Bearer ` is stripped by the caller).
	// Errors:
	//   - nil error + an empty AccountID means the token resolved
	//     but the response fields were empty — should not happen
	//     in practice; the handler treats it as a 401.
	//   - non-nil error carries the gRPC code: Unauthenticated
	//     (hash miss / expired / revoked), PermissionDenied (the
	//     key cannot write traces), NotFound (tracing is not
	//     released to the account) or Internal (Postgres trip).
	AuthenticateKey(ctx context.Context, token string) (TraceKeyPrincipal, error)
	Close() error
}

// TraceKeyPrincipal is what apid resolved an OTLP bearer key to.
type TraceKeyPrincipal struct {
	// AccountID holds the key.
	AccountID string
	// Plan admits and bounds the key's spans.
	Plan string
	// AppID, when set, is the one app the key may write spans for.
	AppID string
	// OrgID, when set, is the company organization the key is bound to:
	// its spans merge into that company's apps.
	OrgID string
}

// AuthClientImpl is the production implementation of AuthClient.
// It owns the lazy gRPC connection to apid's unix socket.
type AuthClientImpl struct {
	conn *grpc.ClientConn
	cli  apidpb.AuthClient
}

// compile-time assertion that *AuthClientImpl satisfies AuthClient.
var _ AuthClient = (*AuthClientImpl)(nil)

// DialAuth opens a lazy gRPC connection to apid's unix socket
// for the Auth service. Same auth model as DialRequestTelemetry:
// socket DAC mode 0660 group `faas` is the only auth in v1.0;
// transport uses insecure credentials over a trusted local
// socket. Connection dials on first RPC; Dial never blocks on
// apid being up.
func DialAuth(ctx context.Context, target string, tlsCfg *tls.Config) (*AuthClientImpl, error) {
	if target == "" {
		return nil, errors.New("apidgrpc: empty apid target for auth")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial apid %q for auth: %w", target, err)
	}
	return &AuthClientImpl{conn: conn, cli: apidpb.NewAuthClient(conn)}, nil
}

// NewAuthClient wraps an already-dialed connection (used by
// bufconn tests in pkg/apidgrpc/).
func NewAuthClient(conn *grpc.ClientConn) *AuthClientImpl {
	return &AuthClientImpl{conn: conn, cli: apidpb.NewAuthClient(conn)}
}

// AuthenticateKey resolves the bearer token to its trace principal.
// The gRPC status survives the wrap, so the handler maps its code.
func (c *AuthClientImpl) AuthenticateKey(ctx context.Context, token string) (TraceKeyPrincipal, error) {
	resp, err := c.cli.AuthenticateKey(ctx, &apidpb.AuthenticateKeyRequest{Token: token})
	if err != nil {
		return TraceKeyPrincipal{}, fmt.Errorf("apidgrpc: AuthenticateKey: %w", err)
	}
	return TraceKeyPrincipal{AccountID: resp.GetAccountId(), Plan: resp.GetPlan(), AppID: resp.GetAppId(), OrgID: resp.GetOrgId()}, nil
}

// Close shuts down the underlying gRPC connection. Safe to call
// multiple times; subsequent calls are no-ops.
func (c *AuthClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
