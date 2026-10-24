package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var _ HoneytokenStore = (*PgStore)(nil)

const honeytokenColumns = `id::text, kind, label, token_digest, planted_at, retired_at`

func scanHoneytoken(row pgx.Row) (Honeytoken, error) {
	var h Honeytoken
	err := row.Scan(&h.ID, &h.Kind, &h.Label, &h.TokenDigest, &h.PlantedAt, &h.RetiredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Honeytoken{}, ErrNotFound
	}
	return h, err
}

// CreateHoneytoken registers a decoy credential (ADR 20261024014500).
func (s *PgStore) CreateHoneytoken(ctx context.Context, kind, digest, label string) (Honeytoken, error) {
	if !validHoneytoken(kind, digest, label) {
		return Honeytoken{}, ErrInvalidArgument
	}
	h, err := scanHoneytoken(s.pool.QueryRow(ctx, `
		INSERT INTO honeytokens (kind, token_digest, label) VALUES ($1, $2, $3)
		RETURNING `+honeytokenColumns, kind, digest, label))
	if err != nil {
		return Honeytoken{}, fmt.Errorf("state: create honeytoken: %w", mapErr(err))
	}
	return h, nil
}

// MatchHoneytoken looks a presented credential's digest up in the registry.
func (s *PgStore) MatchHoneytoken(ctx context.Context, digest string) (Honeytoken, error) {
	if !honeytokenDigestPattern.MatchString(digest) {
		return Honeytoken{}, ErrNotFound
	}
	return scanHoneytoken(s.pool.QueryRow(ctx,
		`SELECT `+honeytokenColumns+` FROM honeytokens WHERE token_digest = $1`, digest))
}

// ListHoneytokens returns every honeytoken, oldest first.
func (s *PgStore) ListHoneytokens(ctx context.Context) ([]Honeytoken, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+honeytokenColumns+` FROM honeytokens ORDER BY planted_at, id`)
	if err != nil {
		return nil, fmt.Errorf("state: list honeytokens: %w", err)
	}
	defer rows.Close()
	var out []Honeytoken
	for rows.Next() {
		h, err := scanHoneytoken(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan honeytoken: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RetireHoneytoken stamps retired_at the first time; later calls keep it.
func (s *PgStore) RetireHoneytoken(ctx context.Context, id string) (Honeytoken, error) {
	return scanHoneytoken(s.pool.QueryRow(ctx, `
		UPDATE honeytokens SET retired_at = coalesce(retired_at, now())
		 WHERE id::text = $1
		RETURNING `+honeytokenColumns, id))
}
