package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
)

// internalSvcMinterEnv selects where this schedd's internal-service tokens
// come from (ADR-20261110210000 slice 3k): "local" (default) signs with the
// cluster key on this box; "secretd" asks the control plane's secretd, so a
// compute node needs no cluster key.
const internalSvcMinterEnv = "FAAS_SCHEDD_INTERNAL_SVC_MINTER"

const (
	// remoteMintTimeout bounds one secretd round trip on the dispatch path.
	remoteMintTimeout = 3 * time.Second
	// remoteMintMargin is how long before expiry a cached token is renewed,
	// so a token never reaches the gateway already expired.
	remoteMintMargin = 10 * time.Second
	// remoteMintCacheMax bounds the per-app cache.
	remoteMintCacheMax = 4096
)

// internalSvcMinterFromEnv reads internalSvcMinterEnv. It reports whether
// tokens come from secretd.
func internalSvcMinterFromEnv(getenv func(string) string, haveSecretd bool) (bool, error) {
	switch v := getenv(internalSvcMinterEnv); v {
	case "", "local":
		return false, nil
	case "secretd":
		if !haveSecretd {
			return false, fmt.Errorf("schedd: %s=secretd needs FAAS_SCHEDD_SECRETD_TARGET", internalSvcMinterEnv)
		}
		return true, nil
	default:
		return false, fmt.Errorf("schedd: %s=%q: want local or secretd", internalSvcMinterEnv, v)
	}
}

type internalSvcTokenRPC interface {
	MintInternalServiceToken(ctx context.Context, in *secretdpb.MintInternalServiceTokenRequest, opts ...grpc.CallOption) (*secretdpb.MintInternalServiceTokenResponse, error)
}

type cachedInternalSvcToken struct {
	token   string
	expires time.Time
}

// remoteInternalSvcMinter asks secretd for each app's token and reuses it
// until shortly before it expires.
type remoteInternalSvcMinter struct {
	rpc   internalSvcTokenRPC
	now   func() time.Time
	mu    sync.Mutex
	cache map[string]cachedInternalSvcToken
}

func newRemoteInternalSvcMinter(rpc internalSvcTokenRPC) *remoteInternalSvcMinter {
	return &remoteInternalSvcMinter{rpc: rpc, now: time.Now, cache: map[string]cachedInternalSvcToken{}}
}

// Mint implements sched.InternalSvcMintFunc.
func (m *remoteInternalSvcMinter) Mint(appID string) (string, error) {
	if appID == "" {
		return "", errors.New("schedd: internal-service token needs an app id")
	}
	now := m.now()
	m.mu.Lock()
	if c, ok := m.cache[appID]; ok && now.Add(remoteMintMargin).Before(c.expires) {
		m.mu.Unlock()
		return c.token, nil
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), remoteMintTimeout)
	defer cancel()
	resp, err := m.rpc.MintInternalServiceToken(ctx, &secretdpb.MintInternalServiceTokenRequest{AppId: appID})
	if err != nil {
		return "", fmt.Errorf("secretd: mint internal-service token: %w", err)
	}
	expires, err := time.Parse(time.RFC3339, resp.GetExpiresAt())
	if err != nil || resp.GetToken() == "" {
		return "", fmt.Errorf("secretd: malformed internal-service token reply (expires_at %q)", resp.GetExpiresAt())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cache) >= remoteMintCacheMax {
		for id, c := range m.cache {
			if !now.Before(c.expires) {
				delete(m.cache, id)
			}
		}
		if len(m.cache) >= remoteMintCacheMax {
			m.cache = map[string]cachedInternalSvcToken{}
		}
	}
	m.cache[appID] = cachedInternalSvcToken{token: resp.GetToken(), expires: expires}
	return resp.GetToken(), nil
}

// AsFunc returns the minter as the func type the engine wires.
func (m *remoteInternalSvcMinter) AsFunc() sched.InternalSvcMintFunc { return m.Mint }
