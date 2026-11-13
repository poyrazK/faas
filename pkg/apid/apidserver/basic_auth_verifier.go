package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/basicverifier"
	"github.com/onebox-faas/faas/pkg/state"
)

// putBasicAuthVerifier records the verify-only form of a basic-auth
// credential, keyed by its sealed blob (ADR-20261110210000 slice 3h). A
// store without verifiers (a test double) records nothing; gateways then
// keep opening the sealed credential.
func (s *server) putBasicAuthVerifier(ctx context.Context, sealed []byte, user, pass string) error {
	store, ok := s.store.(state.BasicAuthVerifierStore)
	if !ok {
		return nil
	}
	verifier, err := basicverifier.New(ctx, user, pass)
	if err != nil {
		return err
	}
	return store.PutAppBasicAuthVerifier(ctx, basicverifier.Key(sealed), verifier)
}
