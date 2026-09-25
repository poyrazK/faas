package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresResolver loads policies from the outbound integration tables. It is
// deliberately read-only; lifecycle/API code owns configuration mutations.
type PostgresResolver struct{ pool *pgxpool.Pool }

func NewPostgresResolver(pool *pgxpool.Pool) (*PostgresResolver, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound postgres pool is required")
	}
	return &PostgresResolver{pool: pool}, nil
}

func (r *PostgresResolver) Integration(ctx context.Context, id string) (Integration, error) {
	integrationID, err := uuid.Parse(id)
	if err != nil {
		return Integration{}, ErrIntegrationNotFound
	}
	var origin string
	var accountID uuid.UUID
	var providerAuthMode string
	var credentialSource string
	var ownerKind string
	var allowedMethods, allowedPathPrefixes []string
	var tokenHash []byte
	var rate float64
	var burst, maxInFlight, timeoutMS int
	var enabled bool
	err = r.pool.QueryRow(ctx, `
		SELECT account_id, origin, token_hash, rate_per_second, burst, max_in_flight,
		       request_timeout_ms, enabled, provider_auth_mode, credential_source,
		       allowed_methods, allowed_path_prefixes, owner_kind
		FROM outbound_integrations WHERE id = $1`, integrationID).
		Scan(&accountID, &origin, &tokenHash, &rate, &burst, &maxInFlight, &timeoutMS, &enabled, &providerAuthMode, &credentialSource, &allowedMethods, &allowedPathPrefixes, &ownerKind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Integration{}, ErrIntegrationNotFound
		}
		return Integration{}, err
	}
	if !enabled {
		return Integration{}, ErrIntegrationDisabled
	}
	u, err := url.Parse(origin)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: invalid origin: %w", ErrInvalidIntegration, err)
	}
	if len(tokenHash) != sha256.Size {
		return Integration{}, fmt.Errorf("%w: token hash has invalid length", ErrInvalidIntegration)
	}
	var hash [sha256.Size]byte
	copy(hash[:], tokenHash)
	rows, err := r.pool.Query(ctx, `
		SELECT attachment.app_id::text, NULL::text[], NULL::text[], true
		  FROM outbound_integration_apps attachment
		  JOIN apps app ON app.id = attachment.app_id
		 WHERE attachment.integration_id = $1 AND app.account_id = $3 AND app.status <> 'deleted'
		UNION ALL
		SELECT binding.app_id::text, binding.allowed_methods, binding.allowed_path_prefixes, false
		  FROM outbound_app_bindings binding
		  JOIN apps app ON app.id = binding.app_id
		 WHERE binding.integration_id = $1 AND binding.account_id = $3
		   AND app.account_id = $3 AND app.status <> 'deleted'
		   AND $2 = 'managed'`, integrationID, providerAuthMode, accountID)
	if err != nil {
		return Integration{}, err
	}
	defer rows.Close()
	apps := make(map[string]struct{})
	operatorApps := make(map[string]struct{})
	customerRoutes := make(map[string]RoutePolicy)
	for rows.Next() {
		var appID string
		var methods, paths []string
		var operator bool
		if err := rows.Scan(&appID, &methods, &paths, &operator); err != nil {
			return Integration{}, err
		}
		apps[appID] = struct{}{}
		if operator {
			operatorApps[appID] = struct{}{}
		} else if methods != nil || paths != nil {
			customerRoutes[appID] = RoutePolicy{AllowedMethods: methods, AllowedPathPrefixes: paths}
		}
	}
	if err := rows.Err(); err != nil {
		return Integration{}, err
	}
	i := Integration{ID: id, Origin: u, TokenHash: hash, AppIDs: apps,
		OperatorAppIDs: operatorApps, CustomerAppRoutes: customerRoutes,
		RatePerSecond: rate, Burst: burst, MaxInFlight: maxInFlight,
		RequestTimeout:   time.Duration(timeoutMS) * time.Millisecond,
		ProviderAuthMode: providerAuthMode, CredentialSource: credentialSource, OwnerKind: ownerKind, AllowedMethods: allowedMethods,
		AllowedPathPrefixes: allowedPathPrefixes, Enabled: true}
	if err := i.Validate(); err != nil {
		return Integration{}, err
	}
	return i, nil
}

var _ Resolver = (*PostgresResolver)(nil)
