package apidgrpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

const (
	// sandboxKeysRefresh is how often a gateway refetches apid's sandbox
	// token keys once it has them; sandboxKeysRetry until it does.
	sandboxKeysRefresh = 5 * time.Minute
	sandboxKeysRetry   = 5 * time.Second
	sandboxKeysTimeout = 5 * time.Second
)

// SandboxTokenKeys fetches the public keys that verify sandbox session
// tokens (ADR-20261027090013 §3). They mint nothing, so a gateway may hold
// them (ADR-20261102191500).
func (c *AuthClientImpl) SandboxTokenKeys(ctx context.Context) ([]sandboxtoken.PublicKey, error) {
	if c == nil || c.cli == nil {
		return nil, errors.New("apidgrpc: no apid auth client for sandbox keys")
	}
	resp, err := c.cli.SandboxTokenKeys(ctx, &apidpb.SandboxTokenKeysRequest{})
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: SandboxTokenKeys: %w", err)
	}
	keys := make([]sandboxtoken.PublicKey, 0, len(resp.GetKeys()))
	for _, k := range resp.GetKeys() {
		keys = append(keys, sandboxtoken.PublicKey{ID: k.GetId(), Key: k.GetPublicKey()})
	}
	return keys, nil
}

// RefreshSandboxKeys keeps v's keys current until ctx ends: it retries until
// apid answers, then refetches every few minutes. A failed refetch keeps the
// keys v has.
func RefreshSandboxKeys(ctx context.Context, c *AuthClientImpl, v *sandboxtoken.Verifier, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	for {
		callCtx, cancel := context.WithTimeout(ctx, sandboxKeysTimeout)
		keys, err := c.SandboxTokenKeys(callCtx)
		cancel()
		wait := sandboxKeysRefresh
		if err == nil {
			v.SetKeys(keys)
		}
		if !v.Ready() {
			wait = sandboxKeysRetry
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("apidgrpc: sandbox token keys unavailable", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
