package state

import (
	"context"
	"fmt"
	"time"
)

// PublishServiceCallerKey records this node's current ADR-206 public key.
//
// A node has one current key, and recently replaced keys are retained for the
// maximum assertion lifetime. Re-publishing the same key is a no-op: a daemon
// restart reads the same key off disk, and churning rotated_at on every boot
// would make a real rotation indistinguishable from a routine restart in the
// audit trail.
func (s *PgStore) PublishServiceCallerKey(ctx context.Context, key ServiceCallerKey) error {
	if key.NodeID == "" || key.KeyID == "" || key.PublicKeyPEM == "" {
		return fmt.Errorf("state: publish service caller key: node, key id and PEM are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin service caller key publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize even the first publication, where there is no existing row to
	// lock yet. Otherwise two concurrent starts with the same node id can race
	// through the history insert and lose the intermediate signing key.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('service-caller-key:' || $1, 0))`, key.NodeID); err != nil {
		return fmt.Errorf("state: lock service caller key publish (node=%q): %w", key.NodeID, err)
	}
	if _, err := tx.Exec(ctx, `
		insert into service_caller_key_history (node_id, key_id, public_key_pem, retire_after)
		select node_id, key_id, public_key_pem, now() + ($3 * interval '1 second')
		  from service_caller_keys
		 where node_id = $1 and key_id <> $2
		on conflict (key_id) do update
		   set node_id = excluded.node_id,
		       public_key_pem = excluded.public_key_pem,
		       retire_after = excluded.retire_after
	`, key.NodeID, key.KeyID, int64(serviceCallerKeyRotationGrace/time.Second)); err != nil {
		return fmt.Errorf("state: retain previous service caller key (node=%q): %w", key.NodeID, err)
	}
	if _, err := tx.Exec(ctx, `delete from service_caller_key_history where key_id = $1`, key.KeyID); err != nil {
		return fmt.Errorf("state: clear restored service caller key from history: %w", err)
	}
	if _, err := tx.Exec(ctx, `
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
	if _, err := tx.Exec(ctx, `delete from service_caller_key_history where retire_after <= now()`); err != nil {
		return fmt.Errorf("state: prune expired service caller keys: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit service caller key publish: %w", err)
	}
	return nil
}

// ListServiceCallerKeys returns every current key and recently rotated keys
// that may still sign an unexpired assertion. The result is ordered by node
// and key id so a verifier's refresh is deterministic.
func (s *PgStore) ListServiceCallerKeys(ctx context.Context) ([]ServiceCallerKey, error) {
	rows, err := s.pool.Query(ctx, `
		select node_id, key_id, public_key_pem from service_caller_keys
		union all
		select node_id, key_id, public_key_pem
		  from service_caller_key_history
		 where retire_after > now()
		 order by node_id, key_id
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
