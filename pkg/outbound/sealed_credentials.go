package outbound

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const ManagedAuthorizationSealNamespace = "outbound_authorization"
const ManagedAuthorizationMaxBytes = 8192

// PostgresSealedCredentialResolver decrypts a customer-owned credential only
// in outboundd. A database read on every request makes rotation/revocation
// effective without restart; the gateway never caches old plaintext.
type PostgresSealedCredentialResolver struct {
	pool       *pgxpool.Pool
	identities []*age.X25519Identity
	allowedIDs map[string]struct{}
}

func NewPostgresSealedCredentialResolver(pool *pgxpool.Pool, identities []*age.X25519Identity, integrationIDs []string) (*PostgresSealedCredentialResolver, error) {
	if pool == nil || len(identities) == 0 || len(integrationIDs) == 0 {
		return nil, errors.New("outbound credential database, identity, and configured integrations are required")
	}
	for _, identity := range identities {
		if identity == nil {
			return nil, errors.New("outbound credential identity is nil")
		}
	}
	allowed := make(map[string]struct{}, len(integrationIDs))
	for _, integrationID := range integrationIDs {
		id, err := uuid.Parse(integrationID)
		if err != nil {
			return nil, errors.New("outbound credential configured integration ID is invalid")
		}
		allowed[id.String()] = struct{}{}
	}
	return &PostgresSealedCredentialResolver{pool: pool, identities: append([]*age.X25519Identity(nil), identities...), allowedIDs: allowed}, nil
}

func (r *PostgresSealedCredentialResolver) Authorization(ctx context.Context, integrationID string) (string, error) {
	id, err := uuid.Parse(integrationID)
	if err != nil {
		return "", errors.New("outbound credential integration ID is invalid")
	}
	if _, configured := r.allowedIDs[id.String()]; !configured {
		return "", errors.New("outbound credential integration is not configured")
	}
	var sealed []byte
	err = r.pool.QueryRow(ctx, `
		SELECT credential.authorization_sealed
		  FROM outbound_integration_credentials credential
		  JOIN outbound_integrations integration ON integration.id = credential.integration_id
		 WHERE credential.integration_id = $1 AND credential.account_id = integration.account_id
		   AND integration.enabled AND integration.provider_auth_mode = 'managed'
		   AND integration.credential_source = 'customer_sealed'`, id).Scan(&sealed)
	if err != nil {
		return "", fmt.Errorf("outbound credential unavailable: %w", err)
	}
	namespace, plaintext, err := secretbox.OpenBytesMulti(r.identities, sealed)
	if err != nil {
		return "", fmt.Errorf("outbound credential cannot be opened: %w", err)
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	value := string(plaintext)
	if namespace != ManagedAuthorizationSealNamespace || !ValidManagedAuthorization(value) {
		return "", errors.New("outbound credential namespace or value is invalid")
	}
	return value, nil
}

var _ ManagedCredentialResolver = (*PostgresSealedCredentialResolver)(nil)
