// adr: 531
package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) verifyTenantHostnameChallenge(ctx context.Context, hostname, token string) (bool, error) {
	verifier, ok := s.store.(state.TenantHostnameChallengeVerifier)
	if !ok {
		return false, errors.New("challenge-bound tenant hostname verification is unavailable")
	}
	return verifier.MarkTenantHostnameVerifiedIfChallenge(ctx, hostname, token)
}
