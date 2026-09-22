package state

import (
	"context"
	"fmt"
)

// PublishServiceCallerKey records this node's current ADR-206 public key.
//
// A node publishes exactly one key, so the row is keyed by node and rotation
// replaces it rather than accumulating history. Re-publishing the same key is
// a no-op: a daemon restart reads the same key off disk, and churning
// rotated_at on every boot would make a real rotation indistinguishable from
// a routine restart in the audit trail.
func (s *PgStore) PublishServiceCallerKey(ctx context.Context, key ServiceCallerKey) error {
	if key.NodeID == "" || key.KeyID == "" || key.PublicKeyPEM == "" {
		return fmt.Errorf("state: publish service caller key: node, key id and PEM are required")
	}
	if _, err := s.pool.Exec(ctx, `
		insert into service_caller_keys (node_id, key_id, public_key_pem)
		values ($1, $2, $3)
		on conflict (node_id) do update
		   set key_id         = excluded.key_id,
		       public_key_pem = excluded.public_key_pem,
		       rotated_at     = now()
		 where service_caller_keys.key_id <> excluded.key_id
	`, key.NodeID, key.KeyID, key.PublicKeyPEM); err != nil {
		return fmt.Errorf("state: publish service caller key (node=%q, key=%s): %w", key.NodeID, key.KeyID, err)
	}
	return nil
}

// ListServiceCallerKeys returns every node's published key. The result is
// ordered by node so a verifier's refresh is deterministic and a diff between
// two refreshes is readable.
func (s *PgStore) ListServiceCallerKeys(ctx context.Context) ([]ServiceCallerKey, error) {
	rows, err := s.pool.Query(ctx, `
		select node_id, key_id, public_key_pem
		  from service_caller_keys
		 order by node_id
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list service caller keys: %w", err)
	}
	defer rows.Close()

	var out []ServiceCallerKey
	for rows.Next() {
		var key ServiceCallerKey
		if err := rows.Scan(&key.NodeID, &key.KeyID, &key.PublicKeyPEM); err != nil {
			return nil, fmt.Errorf("state: scan service caller key: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate service caller keys: %w", err)
	}
	return out, nil
}
