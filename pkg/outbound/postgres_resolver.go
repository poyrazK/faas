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
	var tokenHash []byte
	var rate float64
	var burst, maxInFlight, timeoutMS int
	var enabled bool
	err = r.pool.QueryRow(ctx, `
		SELECT origin, token_hash, rate_per_second, burst, max_in_flight,
		       request_timeout_ms, enabled
		FROM outbound_integrations WHERE id = $1`, integrationID).
		Scan(&origin, &tokenHash, &rate, &burst, &maxInFlight, &timeoutMS, &enabled)
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
	rows, err := r.pool.Query(ctx, `SELECT app_id::text FROM outbound_integration_apps WHERE integration_id = $1`, integrationID)
	if err != nil {
		return Integration{}, err
	}
	defer rows.Close()
	apps := make(map[string]struct{})
	for rows.Next() {
		var appID string
		if err := rows.Scan(&appID); err != nil {
			return Integration{}, err
		}
		apps[appID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return Integration{}, err
	}
	i := Integration{ID: id, Origin: u, TokenHash: hash, AppIDs: apps,
		RatePerSecond: rate, Burst: burst, MaxInFlight: maxInFlight,
		RequestTimeout: time.Duration(timeoutMS) * time.Millisecond, Enabled: true}
	if err := i.Validate(); err != nil {
		return Integration{}, err
	}
	return i, nil
}

var _ Resolver = (*PostgresResolver)(nil)
