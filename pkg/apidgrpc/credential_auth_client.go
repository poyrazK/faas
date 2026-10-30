package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// AuthenticateCredential asks apid to resolve a bearer credential by the
// SHA-256 of the presented token (ADR 20261029202403): credential hashes
// are keyed by a secret only apid holds, so a gateway cannot look them up
// itself. The errors are the store's: state.ErrNotFound for an unknown
// credential, state.ErrAPIKeyExpired and state.ErrAPIKeyRevoked for a dead
// one, so the shared auth middleware maps them as it always has.
func (c *AuthClientImpl) AuthenticateCredential(ctx context.Context, kind apidpb.CredentialKind, sum []byte) (state.Account, state.APIKey, error) {
	resp, err := c.cli.AuthenticateCredential(ctx, &apidpb.AuthenticateCredentialRequest{Kind: kind, Sha256: sum})
	if err != nil {
		return state.Account{}, state.APIKey{}, fmt.Errorf("apidgrpc: AuthenticateCredential: %w", err)
	}
	switch resp.GetOutcome() {
	case apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_OK:
	case apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_EXPIRED:
		return state.Account{}, state.APIKey{}, state.ErrAPIKeyExpired
	case apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_REVOKED:
		return state.Account{}, state.APIKey{}, state.ErrAPIKeyRevoked
	default:
		return state.Account{}, state.APIKey{}, state.ErrNotFound
	}
	var acct state.Account
	if err := json.Unmarshal(resp.GetAccountJson(), &acct); err != nil {
		return state.Account{}, state.APIKey{}, fmt.Errorf("apidgrpc: decode account: %w", err)
	}
	key, err := state.UnmarshalAPIKeyWire(resp.GetKeyJson())
	if err != nil {
		return state.Account{}, state.APIKey{}, fmt.Errorf("apidgrpc: decode key: %w", err)
	}
	return acct, key, nil
}
