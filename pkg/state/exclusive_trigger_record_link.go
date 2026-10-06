package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

func (s *PgStore) LinkExclusiveTriggerRecordOperation(ctx context.Context, recordID string, generation int64, operationID string) error {
	if _, err := uuid.Parse(recordID); err != nil || generation < 1 {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(operationID); err != nil {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `UPDATE trigger_records
SET metadata = CASE
  WHEN jsonb_typeof(metadata) = 'object' AND jsonb_typeof(metadata->'_gregale') = 'object'
    THEN jsonb_set(metadata, '{_gregale,exclusive_operation_id}', to_jsonb($3::text), true)
  WHEN jsonb_typeof(metadata) = 'object'
    THEN jsonb_set(metadata, '{_gregale}', jsonb_build_object('exclusive_operation_id', $3::text), true)
  ELSE jsonb_build_object('_gregale', jsonb_build_object('exclusive_operation_id', $3::text))
END
WHERE id=$1 AND state='claimed' AND claim_generation=$2
  AND claim_expires_at > clock_timestamp()`, recordID, generation, operationID)
	if err != nil {
		return fmt.Errorf("state: link exclusive trigger operation: %w", err)
	}
	return triggerClaimTransitionError("link exclusive operation", tag.RowsAffected(), nil)
}

func (m *MemStore) LinkExclusiveTriggerRecordOperation(ctx context.Context, recordID string, generation int64, operationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := uuid.Parse(recordID); err != nil || generation < 1 {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(operationID); err != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[recordID]
	if !ok || !memTriggerClaimCurrent(record, generation) {
		return ErrNotFound
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(record.Metadata, &metadata); err != nil || metadata == nil {
		metadata = make(map[string]json.RawMessage)
	}
	var platformMetadata map[string]json.RawMessage
	if err := json.Unmarshal(metadata["_gregale"], &platformMetadata); err != nil || platformMetadata == nil {
		platformMetadata = make(map[string]json.RawMessage)
	}
	encodedID, _ := json.Marshal(operationID)
	platformMetadata["exclusive_operation_id"] = encodedID
	encodedPlatformMetadata, err := json.Marshal(platformMetadata)
	if err != nil {
		return fmt.Errorf("state: encode exclusive trigger operation link: %w", err)
	}
	metadata["_gregale"] = encodedPlatformMetadata
	record.Metadata, err = json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("state: encode trigger record metadata: %w", err)
	}
	m.records[recordID] = record
	return nil
}

var _ ExclusiveTriggerRecordLinker = (*PgStore)(nil)
var _ ExclusiveTriggerRecordLinker = (*MemStore)(nil)
