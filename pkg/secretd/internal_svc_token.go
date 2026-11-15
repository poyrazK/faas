package secretd

import (
	"context"
	"crypto/ed25519"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// InternalServiceTokenTTL is the lifetime of a minted internal-service
// token: schedd's own minter's (ADR-119).
const InternalServiceTokenTTL = 30 * time.Second

// internalSvcKeyRefresh is how often the signer reloads the cluster key, so
// a rotation (ADR-125) reaches secretd without a restart.
const internalSvcKeyRefresh = 5 * time.Minute

// InternalServiceKeyLoader returns the cluster signing key and its kid.
type InternalServiceKeyLoader func(context.Context) (ed25519.PrivateKey, string, error)

type internalSvcSigner struct {
	load     InternalServiceKeyLoader
	mu       sync.Mutex
	priv     ed25519.PrivateKey
	kid      string
	loadedAt time.Time
}

// key returns the cached key, reloading it when stale. A failed reload
// keeps the key it had.
func (k *internalSvcSigner) key(ctx context.Context, now time.Time) (ed25519.PrivateKey, string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.priv != nil && now.Sub(k.loadedAt) < internalSvcKeyRefresh {
		return k.priv, k.kid, nil
	}
	priv, kid, err := k.load(ctx)
	if err != nil {
		if k.priv != nil {
			return k.priv, k.kid, nil
		}
		return nil, "", err
	}
	k.priv, k.kid, k.loadedAt = priv, kid, now
	return priv, kid, nil
}

// WithInternalServiceKey lets secretd mint internal-service tokens with the
// cluster signing key load returns (ADR-20261110210000 slice 3k).
func (s *Server) WithInternalServiceKey(load InternalServiceKeyLoader) *Server {
	if load != nil {
		s.internalSvc = &internalSvcSigner{load: load}
	}
	return s
}

// MintInternalServiceToken implements secretdpb.SecretdServer (slice 3k): a
// token for the named app only, to the app's owner or a node holding a
// grant for it, the same authorization as an open by reference.
func (s *Server) MintInternalServiceToken(ctx context.Context, req *secretdpb.MintInternalServiceTokenRequest) (*secretdpb.MintInternalServiceTokenResponse, error) {
	appID := req.GetAppId()
	if appID == "" {
		return nil, rpcErr(codes.InvalidArgument, "secretd needs an app id")
	}
	if s.internalSvc == nil {
		return nil, rpcErr(codes.FailedPrecondition, "secretd holds no internal-service signing key")
	}
	_, _, via, err := s.authorizeByReference(ctx, rpcMintInternalServiceToken, appID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	priv, kid, err := s.internalSvc.key(ctx, now)
	if err != nil {
		s.log.Error("secretd: internal-service key", "err", err)
		return nil, rpcErr(codes.Unavailable, "secretd could not load the internal-service signing key")
	}
	token, err := internalsvc.Mint("schedd", InternalServiceTokenTTL, map[string]any{"app_id": appID}, priv, kid)
	if err != nil {
		s.log.Error("secretd: mint internal-service token", "app_id", logsanitize.Field(appID), "err", err)
		return nil, rpcErr(codes.Internal, "secretd could not mint the token")
	}
	s.metrics.open(rpcMintInternalServiceToken, via)
	return &secretdpb.MintInternalServiceTokenResponse{Token: token, ExpiresAt: now.Add(InternalServiceTokenTTL).UTC().Format(time.RFC3339)}, nil
}
