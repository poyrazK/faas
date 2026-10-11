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
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// authStore is the Store subset the receiver needs. Declared as
// an interface here so unit tests can substitute a fake without
// spinning a real Postgres pool.
type authStore interface {
	AuthenticateKey(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
}

// authReceiver is the in-package server implementation of
// apidpb.AuthServer. Wired by RegisterAuthReceiver onto a
// *grpc.Server. It is intentionally NOT kill-switched: gatewayd-public
// dials only when its OTel handler is enabled, so the kill-switch
// lives at the dial site (no point returning codes.Unavailable here).
type authReceiver struct {
	apidpb.UnimplementedAuthServer
	store authStore
}

// newAuthReceiver wires a production receiver.
func newAuthReceiver(store authStore) *authReceiver {
	return &authReceiver{store: store}
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
	acct, _, err := r.store.AuthenticateKey(ctx, api.HashAPIKey(tok))
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
	return &apidpb.AuthenticateKeyResponse{
		AccountId: acct.ID,
		Plan:      string(acct.Plan),
	}, nil
}

// RegisterAuthReceiver binds the AuthServer onto a gRPC server.
// Called from runAuthServer in main.go alongside the other gRPC
// services.
func RegisterAuthReceiver(s *grpc.Server, store authStore) {
	apidpb.RegisterAuthServer(s, newAuthReceiver(store))
}
