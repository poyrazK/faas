package apidgrpc

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// CredentialResolver resolves a credential by its token's SHA-256;
// *AuthClientImpl is one.
type CredentialResolver interface {
	AuthenticateCredential(ctx context.Context, kind apidpb.CredentialKind, sum []byte) (state.Account, state.APIKey, error)
}

// CredentialStore is the store a gateway read API keys from before ADR
// 20261029202403. A store that also has AuthenticateOIDCBearer falls back
// for OIDC bearers too.
type CredentialStore interface {
	AuthenticateKey(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
}

type oidcCredentialStore interface {
	AuthenticateOIDCBearer(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
}

// CredentialLookup is how a gateway authenticates an API key or OIDC
// bearer: it asks apid, which alone holds the key credential hashes are
// stored under (ADR 20261029202403). It reads Fallback only while apid runs
// a release without the RPC (codes.Unimplemented during a rollout; the
// deploy order updates apid first). Fallback may be nil.
type CredentialLookup struct {
	Apid     CredentialResolver
	Fallback CredentialStore
	Log      *slog.Logger
}

func (c CredentialLookup) resolve(ctx context.Context, kind apidpb.CredentialKind, sum []byte, fallback func(context.Context, []byte) (state.Account, state.APIKey, error)) (state.Account, state.APIKey, error) {
	acct, key, err := c.Apid.AuthenticateCredential(ctx, kind, sum)
	if err == nil || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrAPIKeyExpired) || errors.Is(err, state.ErrAPIKeyRevoked) {
		return acct, key, err
	}
	if fallback != nil && status.Code(errors.Unwrap(err)) == codes.Unimplemented {
		if c.Log != nil {
			c.Log.Warn("apid has no credential RPC yet; reading the store", "kind", kind.String())
		}
		return fallback(ctx, sum)
	}
	return state.Account{}, state.APIKey{}, err
}

// AuthenticateKey resolves an account API key.
func (c CredentialLookup) AuthenticateKey(ctx context.Context, sum []byte) (state.Account, state.APIKey, error) {
	var fallback func(context.Context, []byte) (state.Account, state.APIKey, error)
	if c.Fallback != nil {
		fallback = c.Fallback.AuthenticateKey
	}
	return c.resolve(ctx, apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum, fallback)
}

// AuthenticateOIDCBearer resolves an OIDC-exchanged bearer.
func (c CredentialLookup) AuthenticateOIDCBearer(ctx context.Context, sum []byte) (state.Account, state.APIKey, error) {
	var fallback func(context.Context, []byte) (state.Account, state.APIKey, error)
	if store, ok := c.Fallback.(oidcCredentialStore); ok {
		fallback = store.AuthenticateOIDCBearer
	}
	return c.resolve(ctx, apidpb.CredentialKind_CREDENTIAL_KIND_OIDC_BEARER, sum, fallback)
}
