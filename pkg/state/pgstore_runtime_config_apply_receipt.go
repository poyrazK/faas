package state

import (
	"context"
	"fmt"
)

func (s *pgCore) RecordRuntimeConfigApplyReceipt(ctx context.Context, receipt RuntimeConfigApplyReceipt) error {
	receipt, err := normalizeRuntimeConfigApplyReceipt(receipt)
	if err != nil {
		return err
	}
	var errText *string
	if receipt.Error != "" {
		errText = &receipt.Error
	}
	// A receipt never moves an instance's record to an older version; a
	// failed retry of the same version refreshes the error and time.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO runtime_config_apply_receipts
		    (config_key, scope, scope_id, daemon, instance, version, status, error, reported_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (config_key, scope, scope_id, daemon, instance) DO UPDATE
		SET version = EXCLUDED.version, status = EXCLUDED.status,
		    error = EXCLUDED.error, reported_at = EXCLUDED.reported_at
		WHERE runtime_config_apply_receipts.version <= EXCLUDED.version`,
		receipt.Key, string(receipt.Scope), receipt.ScopeID, receipt.Daemon, receipt.Instance,
		receipt.Version, string(receipt.Status), errText)
	if err != nil {
		return fmt.Errorf("state: record runtime config apply receipt: %w", err)
	}
	return nil
}

func (s *PgStore) ListRuntimeConfigApplyReceipts(ctx context.Context, keys []string) ([]RuntimeConfigApplyReceipt, error) {
	if keys == nil {
		keys = []string{}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT config_key, scope, scope_id, daemon, instance, version, status,
		       COALESCE(error, ''), reported_at
		FROM runtime_config_apply_receipts
		WHERE cardinality($1::text[]) = 0 OR config_key = ANY($1::text[])
		ORDER BY config_key, scope, scope_id, daemon, instance`, keys)
	if err != nil {
		return nil, fmt.Errorf("state: list runtime config apply receipts: %w", err)
	}
	defer rows.Close()
	var out []RuntimeConfigApplyReceipt
	for rows.Next() {
		var receipt RuntimeConfigApplyReceipt
		var scope, status string
		if err := rows.Scan(&receipt.Key, &scope, &receipt.ScopeID, &receipt.Daemon, &receipt.Instance,
			&receipt.Version, &status, &receipt.Error, &receipt.ReportedAt); err != nil {
			return nil, fmt.Errorf("state: scan runtime config apply receipt: %w", err)
		}
		receipt.Scope, receipt.Status = RuntimeConfigScope(scope), RuntimeConfigApplyReceiptStatus(status)
		out = append(out, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate runtime config apply receipts: %w", err)
	}
	return out, nil
}
