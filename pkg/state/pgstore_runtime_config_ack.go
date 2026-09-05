package state

import (
	"context"
	"encoding/json"
	"fmt"
)

// AcknowledgeRuntimeConfig upserts one daemon/node's observation of a
// runtime-config version. The method is additive and deliberately not on the
// broad Store interface so deployments can roll out the schema independently.
func (s *PgStore) AcknowledgeRuntimeConfig(ctx context.Context, ack RuntimeConfigAck) error {
	if ack.Key == "" || ack.Consumer == "" || ack.Version <= 0 {
		return fmt.Errorf("state: invalid runtime config acknowledgement identity")
	}
	if ack.Status != RuntimeConfigAckApplied && ack.Status != RuntimeConfigAckFailed {
		return fmt.Errorf("state: invalid runtime config acknowledgement status %q", ack.Status)
	}
	if ack.EffectiveValue == nil {
		ack.EffectiveValue = json.RawMessage("null")
	}
	status := string(ack.Status)
	var appliedAt any
	if ack.AppliedAt != nil {
		appliedAt = ack.AppliedAt.UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_config_acks
		    (config_key, scope, scope_id, consumer, node_id, config_version,
		     status, effective_value, error, updated_at, applied_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NULLIF($9, ''), now(), $10)
		ON CONFLICT (config_key, scope, scope_id, consumer, node_id)
		DO UPDATE SET config_version = EXCLUDED.config_version,
		              status = EXCLUDED.status,
		              effective_value = EXCLUDED.effective_value,
		              error = EXCLUDED.error,
		              updated_at = now(),
		              applied_at = EXCLUDED.applied_at`,
		ack.Key, string(ack.Scope), ack.ScopeID, ack.Consumer, ack.NodeID,
		ack.Version, status, string(ack.EffectiveValue), ack.Error, appliedAt)
	if err != nil {
		return fmt.Errorf("state: acknowledge runtime config: %w", err)
	}
	return nil
}

func (s *PgStore) ListRuntimeConfigAcks(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string) ([]RuntimeConfigAck, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT config_key, scope, scope_id, consumer, node_id, config_version,
		       status, COALESCE(effective_value, 'null'::jsonb),
		       COALESCE(error, ''), updated_at, applied_at
		FROM runtime_config_acks
		WHERE ($1 = '' OR config_key = $1)
		  AND ($2 = '' OR scope = $2)
		  AND ($3 = '' OR scope_id = $3)
		ORDER BY config_key, consumer, node_id`, key, string(scope), scopeID)
	if err != nil {
		return nil, fmt.Errorf("state: list runtime config acknowledgements: %w", err)
	}
	defer rows.Close()
	var out []RuntimeConfigAck
	for rows.Next() {
		var ack RuntimeConfigAck
		var scopeText, status string
		var effective []byte
		if err := rows.Scan(&ack.Key, &scopeText, &ack.ScopeID, &ack.Consumer,
			&ack.NodeID, &ack.Version, &status, &effective, &ack.Error,
			&ack.UpdatedAt, &ack.AppliedAt); err != nil {
			return nil, fmt.Errorf("state: scan runtime config acknowledgement: %w", err)
		}
		ack.Scope = RuntimeConfigScope(scopeText)
		ack.Status = RuntimeConfigAckStatus(status)
		ack.EffectiveValue = append(json.RawMessage(nil), effective...)
		out = append(out, ack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate runtime config acknowledgements: %w", err)
	}
	return out, nil
}
