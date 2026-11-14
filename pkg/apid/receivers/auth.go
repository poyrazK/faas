// apid-side gRPC handler for the Auth service (ADR-127 PR-D).
// Wired by RegisterAuthReceiver in main.go onto /run/faas/auth.sock.
//
// Direction: gatewayd-public → apid. gatewayd-public is the ONLY
// caller — the OTel spans writer needs to translate the raw
// Bearer token from the customer's OTLP POST into the matching
// (account_id, plan) tuple so the gateway can enforce per-plan
// telemetry caps without holding a direct Postgres connection
// (CLAUDE.md ownership: apid is the sole writer to api_keys;
// gatewayd-public never opens a direct connection for this path).
//
// Wire discipline mirrors grpc_server_request_telemetry.go:
// errors map to gRPC codes (InvalidArgument / Unauthenticated /
// Internal), and the lookup is one-shot per OTLP POST (the
// gateway caches the result for the duration of the request
// envelope; it does NOT pollute PR-B's per-account caps cache
// because the OTel writer has its own limiter in Stage 3).

package receivers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// authStore is the Store subset the receiver needs. Declared as
// an interface here so unit tests can substitute a fake without
// spinning a real Postgres pool.
type authStore interface {
	AuthenticateKey(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
	AuthenticateOIDCBearer(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
}

// authReceiver is the in-package server implementation of
// apidpb.AuthServer. Wired by RegisterAuthReceiver onto a
// *grpc.Server. It is intentionally NOT kill-switched: gatewayd-public
// dials only when its OTel handler is enabled, so the kill-switch
// lives at the dial site (no point returning codes.Unavailable here).
type authReceiver struct {
	apidpb.UnimplementedAuthServer
	store authStore
	// tracing decides the tracing capability for an OTLP key's subject;
	// nil releases everyone.
	tracing TracingReleased
	// sessions opens dashboard session cookies for VerifySession; nil
	// answers Unavailable.
	sessions SessionOpener
	// sandboxKey verifies sandbox session tokens, derived from the session
	// key when sessions has one; nil answers SandboxTokenKeys Unavailable.
	sandboxKey *sandboxtoken.PublicKey
	// agents answers gateways' agent approval and activity calls; nil
	// answers Unavailable (ADR-20261111150000).
	agents AgentRequests
}

// sessionKeyHolder is a SessionOpener that can derive keys:
// *session.Manager.
type sessionKeyHolder interface {
	BindingKey() []byte
}

// SessionOpener opens a dashboard session cookie: apid's *session.Manager.
type SessionOpener interface {
	Verify(value string) (session.Envelope, error)
}

// newAuthReceiver wires a production receiver.
func newAuthReceiver(store authStore, tracing TracingReleased, sessions SessionOpener, opts ...AuthOption) *authReceiver {
	r := &authReceiver{store: store, tracing: tracing, sessions: sessions}
	for _, opt := range opts {
		opt(r)
	}
	if holder, ok := sessions.(sessionKeyHolder); ok && holder != nil {
		if issuer, err := sandboxtoken.NewIssuer(holder.BindingKey()); err == nil {
			pub := issuer.PublicKey()
			r.sandboxKey = &pub
		}
	}
	return r
}

// SandboxTokenKeys returns the public key that verifies sandbox session
// tokens; it mints nothing.
func (r *authReceiver) SandboxTokenKeys(context.Context, *apidpb.SandboxTokenKeysRequest) (*apidpb.SandboxTokenKeysResponse, error) {
	if r.sandboxKey == nil {
		return nil, status.Error(codes.Unavailable, "sandbox tokens are not configured")
	}
	return &apidpb.SandboxTokenKeysResponse{Keys: []*apidpb.SandboxTokenKey{{Id: r.sandboxKey.ID, PublicKey: r.sandboxKey.Key}}}, nil
}

// AuthenticateKey resolves the bearer token to (account_id, plan).
// The token is the raw value from the customer's OTLP POST's
// Authorization header (i.e. the value AFTER `Bearer ` is stripped
// by the caller). The server hashes with SHA-256 + looks up the
// matching api_keys row + returns the parent account's id + plan.
//
// Errors:
//   - InvalidArgument when token is empty.
//   - Unauthenticated when no api_keys row matches the hash, or
//     when the matched key is expired/revoked (same posture as
//     pkg/auth/middleware/middleware.go:464-475).
//   - Internal on Postgres errors.
//
// Note: the matched key's expiration / revocation status is read
// from the same AuthenticateKey call — PR-D doesn't add a new
// Store method to avoid widening the interface for one consumer.
// IAM-5 (issue #189) sentinels map to Unauthenticated here (the
// gateway doesn't need the audit seam; it just drops the request).
func (r *authReceiver) AuthenticateKey(ctx context.Context, req *apidpb.AuthenticateKeyRequest) (*apidpb.AuthenticateKeyResponse, error) {
	tok := strings.TrimSpace(req.GetToken())
	if tok == "" {
		return nil, status.Error(codes.InvalidArgument, "empty token")
	}
	acct, key, err := r.store.AuthenticateKey(ctx, api.HashAPIKey(tok))
	if err != nil {
		// Hash miss + IAM-5 expired/revoked keys are all
		// authenticated-shape errors from the gateway's POV:
		// the bearer presented the right format, but no live
		// account owns it. Map every one of them to
		// Unauthenticated so the gateway treats them identically.
		if errors.Is(err, state.ErrNotFound) ||
			errors.Is(err, state.ErrAPIKeyExpired) ||
			errors.Is(err, state.ErrAPIKeyRevoked) {
			return nil, status.Error(codes.Unauthenticated, "api key invalid")
		}
		// Any other pgconn error — Postgres trip, unique-violation
		// leak, etc — is internal. The gateway logs + drops.
		if errors.As(err, new(*pgconn.PgError)) {
			return nil, status.Errorf(codes.Internal, "apid auth lookup: %v", err)
		}
		// Generic store-side error: same shape — Internal.
		return nil, status.Errorf(codes.Internal, "apid auth lookup: %v", err)
	}
	return r.traceKeyPrincipal(ctx, acct, key)
}

// AuthenticateCredential resolves a credential for a gateway that cannot
// key its hash itself (ADR 20261029202403). The gateway sends the presented
// token's SHA-256; an unknown, expired or revoked credential is an outcome,
// so a store failure is the only error besides a malformed request.
func (r *authReceiver) AuthenticateCredential(ctx context.Context, req *apidpb.AuthenticateCredentialRequest) (*apidpb.AuthenticateCredentialResponse, error) {
	sum := req.GetSha256()
	if len(sum) != sha256.Size {
		return nil, status.Error(codes.InvalidArgument, "sha256 must be 32 bytes")
	}
	var lookup func(context.Context, []byte) (state.Account, state.APIKey, error)
	switch req.GetKind() {
	case apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY:
		lookup = r.store.AuthenticateKey
	case apidpb.CredentialKind_CREDENTIAL_KIND_OIDC_BEARER:
		lookup = r.store.AuthenticateOIDCBearer
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown credential kind")
	}
	acct, key, err := lookup(ctx, sum)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return &apidpb.AuthenticateCredentialResponse{Outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_NOT_FOUND}, nil
	case errors.Is(err, state.ErrAPIKeyExpired):
		return &apidpb.AuthenticateCredentialResponse{Outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_EXPIRED}, nil
	case errors.Is(err, state.ErrAPIKeyRevoked):
		return &apidpb.AuthenticateCredentialResponse{Outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_REVOKED}, nil
	case err != nil:
		return nil, status.Errorf(codes.Internal, "apid credential lookup: %v", err)
	}
	accountJSON, err := json.Marshal(acct)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode account: %v", err)
	}
	keyJSON, err := state.MarshalAPIKeyWire(key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode key: %v", err)
	}
	return &apidpb.AuthenticateCredentialResponse{Outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_OK, AccountJson: accountJSON, KeyJson: keyJSON}, nil
}

// MatchCredential answers whether a stored credential hash a gateway read
// is the one the presented token may hold (ADR 20261029202403). It answers
// only yes or no, and only for the columns gateways check, under apid's
// policy for the column's family (amendment 1, decision 2).
func (r *authReceiver) MatchCredential(_ context.Context, req *apidpb.MatchCredentialRequest) (*apidpb.MatchCredentialResponse, error) {
	tag, family, ok := state.GatewayCheckedCredential(req.GetColumn())
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "column is not a gateway-checked credential")
	}
	if len(req.GetSha256()) != sha256.Size {
		return nil, status.Error(codes.InvalidArgument, "sha256 must be 32 bytes")
	}
	return &apidpb.MatchCredentialResponse{Match: credhash.For(family).Matches(tag, req.GetSha256(), req.GetStored())}, nil
}

// VerifySession opens a dashboard session cookie for a gateway that holds
// no session key (ADR-20261102191500). A malformed, forged or expired
// cookie is an outcome, not an error. The cookie is never logged.
func (r *authReceiver) VerifySession(_ context.Context, req *apidpb.VerifySessionRequest) (*apidpb.VerifySessionResponse, error) {
	if req.GetCookie() == "" {
		return nil, status.Error(codes.InvalidArgument, "cookie is required")
	}
	if r.sessions == nil {
		return nil, status.Error(codes.Unavailable, "sessions are not configured")
	}
	env, verifyErr := r.sessions.Verify(req.GetCookie())
	switch {
	case errors.Is(verifyErr, session.ErrInvalid):
		return &apidpb.VerifySessionResponse{}, nil
	case verifyErr != nil:
		return nil, status.Error(codes.Internal, "open session cookie")
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode session envelope")
	}
	return &apidpb.VerifySessionResponse{Valid: true, Envelope: raw}, nil
}

// RegisterAuthReceiver binds the AuthServer onto a gRPC server.
// Called from runAuthServer in main.go alongside the other gRPC
// services. tracing gates OTLP keys on the tracing capability; nil
// releases everyone. sessions answers VerifySession; nil answers
// Unavailable.
func RegisterAuthReceiver(s *grpc.Server, store authStore, tracing TracingReleased, sessions SessionOpener, opts ...AuthOption) {
	apidpb.RegisterAuthServer(s, newAuthReceiver(store, tracing, sessions, opts...))
}
