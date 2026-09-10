package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type consumerRowScanner interface {
	Scan(dest ...any) error
}

const apiConsumerSelectCols = `id, account_id, app_id, external_ref, name, status, created_at, updated_at, revoked_at`

func scanAPIConsumerRow(row consumerRowScanner) (APIConsumer, error) {
	var c APIConsumer
	var status string
	var revokedAt *time.Time
	if err := row.Scan(
		&c.ID,
		&c.AccountID,
		&c.AppID,
		&c.ExternalRef,
		&c.Name,
		&status,
		&c.CreatedAt,
		&c.UpdatedAt,
		&revokedAt,
	); err != nil {
		return APIConsumer{}, err
	}
	c.Status = APIConsumerStatus(status)
	c.RevokedAt = revokedAt
	return c, nil
}

func validateAPIConsumerPgInput(op, accountID, appID, externalRef, name string) error {
	if accountID == "" || appID == "" {
		return fmt.Errorf("pgstore: %s: empty account_id or app_id", op)
	}
	if externalRef == "" || len(externalRef) > 256 {
		return fmt.Errorf("pgstore: %s: external_ref must be 1-256 characters", op)
	}
	if name == "" || len(name) > 128 {
		return fmt.Errorf("pgstore: %s: name must be 1-128 characters", op)
	}
	return nil
}

func (s *PgStore) CreateAPIConsumer(ctx context.Context, accountID, appID, externalRef, name string) (APIConsumer, error) {
	if err := validateAPIConsumerPgInput("CreateAPIConsumer", accountID, appID, externalRef, name); err != nil {
		return APIConsumer{}, err
	}
	row := s.pool.QueryRow(ctx,
		`insert into api_consumers (account_id, app_id, external_ref, name)
		 values ($1::uuid, $2::uuid, $3, $4)
		 returning `+apiConsumerSelectCols,
		accountID, appID, externalRef, name)
	c, err := scanAPIConsumerRow(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return APIConsumer{}, ErrConflict
		}
		return APIConsumer{}, err
	}
	return c, nil
}

func (s *PgStore) GetAPIConsumerByID(ctx context.Context, accountID, consumerID string) (APIConsumer, error) {
	if accountID == "" || consumerID == "" {
		return APIConsumer{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+apiConsumerSelectCols+`
		   from api_consumers
		  where id = $1::uuid and account_id = $2::uuid`,
		consumerID, accountID)
	c, err := scanAPIConsumerRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, ErrNotFound
	}
	return c, err
}

func (s *PgStore) ListAPIConsumersForApp(ctx context.Context, accountID, appID string) ([]APIConsumer, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+apiConsumerSelectCols+`
		   from api_consumers
		  where account_id = $1::uuid and app_id = $2::uuid
		  order by created_at desc, id desc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumer
	for rows.Next() {
		c, err := scanAPIConsumerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PgStore) RevokeAPIConsumer(ctx context.Context, accountID, consumerID string) (APIConsumer, error) {
	if accountID == "" || consumerID == "" {
		return APIConsumer{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`update api_consumers
		    set status = 'revoked', revoked_at = coalesce(revoked_at, now()), updated_at = now()
		  where id = $1::uuid and account_id = $2::uuid
		  returning `+apiConsumerSelectCols,
		consumerID, accountID)
	c, err := scanAPIConsumerRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, ErrNotFound
	}
	return c, err
}

func (s *PgStore) CreateConsumerKeyForConsumer(ctx context.Context, accountID, consumerID, name, prefix string, hash []byte, scopes []string, expiresAt *time.Time) (ConsumerKey, error) {
	if accountID == "" || consumerID == "" {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKeyForConsumer: empty account_id or consumer_id")
	}
	if name == "" || len(name) > 64 {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKeyForConsumer: name must be 1-64 characters")
	}
	if prefix == "" || len(prefix) > 16 {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKeyForConsumer: prefix must be 1-16 characters")
	}
	if len(hash) != 32 {
		return ConsumerKey{}, fmt.Errorf("pgstore: CreateConsumerKeyForConsumer: hash must be 32 bytes, got %d", len(hash))
	}
	if len(scopes) == 0 {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKeyForConsumer: scopes cannot be empty")
	}
	for _, scope := range scopes {
		switch scope {
		case "read", "write", "admin":
		default:
			return ConsumerKey{}, fmt.Errorf("pgstore: CreateConsumerKeyForConsumer: scope %q is not in the closed-set {read, write, admin}", scope)
		}
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKeyForConsumer: expires_at must be in the future")
	}
	row := s.pool.QueryRow(ctx,
		`insert into consumer_keys
		   (consumer_id, account_id, app_id, name, prefix, hashed_secret, scopes, expires_at)
		 select c.id, c.account_id, c.app_id, $3, $4, $5, $6, $7
		   from api_consumers c
		  where c.id = $2::uuid and c.account_id = $1::uuid
		    and c.status = 'active'
		 returning `+consumerKeySelectCols,
		accountID, consumerID, name, prefix, hash, scopes, expiresAt)
	k, err := scanConsumerKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish an IDOR/missing consumer from a known but revoked
		// consumer. The SQL insert intentionally filters on active status;
		// this second, scoped read preserves the memstore's ErrConflict
		// contract for attempts to issue a credential after revocation.
		c, getErr := s.GetAPIConsumerByID(ctx, accountID, consumerID)
		if getErr == nil && !c.Active() {
			return ConsumerKey{}, ErrConflict
		}
		if getErr != nil {
			return ConsumerKey{}, getErr
		}
		return ConsumerKey{}, ErrNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ConsumerKey{}, ErrConflict
		}
	}
	return k, err
}
