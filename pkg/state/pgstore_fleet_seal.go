package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// FleetSealProbe is the singleton non-secret ciphertext used by node-join to
// prove that a candidate node can consume envelopes produced by the customer
// secret path before the node is activated.
type FleetSealProbe struct {
	Recipient  string
	SealedBlob []byte
	UpdatedAt  time.Time
}

func (s *PgStore) LoadFleetSealProbe(ctx context.Context) (FleetSealProbe, error) {
	var p FleetSealProbe
	err := s.pool.QueryRow(ctx, `
		SELECT recipient, sealed_blob, updated_at
		  FROM fleet_seal_domain_probe
		 WHERE id = 1
	`).Scan(&p.Recipient, &p.SealedBlob, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FleetSealProbe{}, ErrNotFound
	}
	if err != nil {
		return FleetSealProbe{}, fmt.Errorf("state: load fleet seal probe: %w", err)
	}
	return p, nil
}

func (s *PgStore) UpsertFleetSealProbe(ctx context.Context, recipient string, sealed []byte) error {
	if recipient == "" || len(sealed) == 0 {
		return errors.New("state: fleet seal probe recipient and ciphertext are required")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO fleet_seal_domain_probe (id, recipient, sealed_blob, updated_at)
		VALUES (1, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET
			recipient = EXCLUDED.recipient,
			sealed_blob = EXCLUDED.sealed_blob,
			updated_at = now()
	`, recipient, sealed)
	if err != nil {
		return fmt.Errorf("state: upsert fleet seal probe: %w", err)
	}
	return nil
}

// ResealClusterSigningKey replaces only the ciphertext of the current
// singleton row. The old ciphertext predicate makes concurrent signing-key
// rotation fail with ErrConflict instead of overwriting the new key.
func (s *PgStore) ResealClusterSigningKey(ctx context.Context, keyID string, oldSealed, newSealed []byte) error {
	if keyID == "" || len(oldSealed) == 0 || len(newSealed) == 0 {
		return errors.New("state: reseal cluster signing key: empty input")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE cluster_signing_keys
		   SET sealed_blob = $3
		 WHERE id = 1 AND key_id = $1 AND sealed_blob = $2
	`, keyID, oldSealed, newSealed)
	if err != nil {
		return fmt.Errorf("state: reseal cluster signing key: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// ResealAppSecretForFleet is a maintenance-only ciphertext replacement. It
// preserves value_hash and all managed-credential ownership columns. The
// compare-and-swap predicate prevents a concurrent customer rotation from
// being lost.
func (s *PgStore) ResealAppSecretForFleet(ctx context.Context, row AppSecret, recipient string, sealed []byte) error {
	if recipient == "" || len(sealed) == 0 || len(row.Ciphertext) == 0 {
		return errors.New("state: reseal app secret for fleet: empty input")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE app_secrets
		   SET ciphertext = $6, kid = $5, updated_at = now()
		 WHERE account_id = $1 AND app_id = $2 AND scope = $3 AND key = $4
		   AND ciphertext = $7
	`, row.AccountID, row.AppID, row.Scope, row.Key, recipient, sealed, row.Ciphertext)
	if err != nil {
		return fmt.Errorf("state: reseal app secret for fleet: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
