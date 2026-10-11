package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PgStore) BeginManagedRealtimeDirectMessageReceipt(ctx context.Context, endpointID, messageID string, fingerprint []byte) (bool, bool, error) {
	if err := validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, fingerprint); err != nil {
		return false, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("state: begin direct message receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedID string
	if err := tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id = $1 for update`, endpointID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, ErrNotFound
		}
		return false, false, fmt.Errorf("state: lock endpoint for direct message receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_direct_message_receipts
		where endpoint_id = $1 and message_id = $2 and expires_at <= clock_timestamp()
	`, endpointID, messageID); err != nil {
		return false, false, fmt.Errorf("state: expire direct message receipt: %w", err)
	}
	var existingFingerprint []byte
	var dispatchComplete bool
	var leaseUntil time.Time
	err = tx.QueryRow(ctx, `
		select payload_fingerprint, dispatch_complete, dispatch_lease_until
		from managed_realtime_direct_message_receipts
		where endpoint_id = $1 and message_id = $2
		for update
	`, endpointID, messageID).Scan(&existingFingerprint, &dispatchComplete, &leaseUntil)
	if err == nil {
		if !bytes.Equal(existingFingerprint, fingerprint) {
			return false, false, ErrManagedRealtimeDirectMessageConflict
		}
		if dispatchComplete {
			if err := tx.Commit(ctx); err != nil {
				return false, false, fmt.Errorf("state: commit direct message receipt replay: %w", err)
			}
			return false, false, nil
		}
		if leaseUntil.After(time.Now().UTC()) {
			if err := tx.Commit(ctx); err != nil {
				return false, false, fmt.Errorf("state: commit direct message receipt wait: %w", err)
			}
			return false, true, nil
		}
		if _, err := tx.Exec(ctx, `
			update managed_realtime_direct_message_receipts
			set dispatch_lease_until = clock_timestamp() + make_interval(secs => $3::double precision)
			where endpoint_id = $1 and message_id = $2
		`, endpointID, messageID, ManagedRealtimeDirectMessageDispatchLease.Seconds()); err != nil {
			return false, false, fmt.Errorf("state: renew direct message dispatch lease: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, false, fmt.Errorf("state: commit direct message dispatch lease: %w", err)
		}
		return true, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, false, fmt.Errorf("state: read direct message receipt: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `
		select count(*) from managed_realtime_direct_message_receipts
		where endpoint_id = $1 and expires_at > clock_timestamp()
	`, endpointID).Scan(&count); err != nil {
		return false, false, fmt.Errorf("state: count direct message receipts: %w", err)
	}
	if count >= ManagedRealtimeDirectMessageMaxReceipts {
		return false, false, ErrManagedRealtimeDirectMessageLimit
	}
	if _, err := tx.Exec(ctx, `
		with expired as (
			select endpoint_id, message_id
			from managed_realtime_direct_message_receipts
			where endpoint_id = $1 and expires_at <= clock_timestamp()
			order by expires_at limit 128
		)
		delete from managed_realtime_direct_message_receipts r
		using expired e where r.endpoint_id = e.endpoint_id and r.message_id = e.message_id
	`, endpointID); err != nil {
		return false, false, fmt.Errorf("state: prune direct message receipts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into managed_realtime_direct_message_receipts
			(endpoint_id, message_id, payload_fingerprint, expires_at, dispatch_lease_until)
		values ($1, $2, $3, clock_timestamp() + make_interval(secs => $4::double precision),
			clock_timestamp() + make_interval(secs => $5::double precision))
	`, endpointID, messageID, fingerprint, ManagedRealtimeDirectMessageReceiptRetention.Seconds(), ManagedRealtimeDirectMessageDispatchLease.Seconds()); err != nil {
		return false, false, fmt.Errorf("state: create direct message receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("state: commit direct message receipt: %w", err)
	}
	return true, false, nil
}

func (s *PgStore) RegisterManagedRealtimeDirectMessageTargets(ctx context.Context, endpointID, messageID, nodeID string, targets []ManagedRealtimeDirectMessageTarget) ([]string, error) {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || validateManagedRealtimeDirectMessageNode(nodeID) != nil || validateManagedRealtimeDirectMessageTargets(targets) != nil {
		return nil, ErrManagedRealtimeDirectMessageInvalid
	}
	if len(targets) == 0 {
		return []string{}, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: begin direct message target registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from managed_realtime_direct_message_receipts
		where endpoint_id = $1 and message_id = $2 and expires_at > clock_timestamp())
	`, endpointID, messageID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("state: check direct message receipt: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1), hashtext($2))`, endpointID, "managed-realtime-direct-message-deliveries"); err != nil {
		return nil, fmt.Errorf("state: lock direct message delivery quota: %w", err)
	}
	connectionIDs := make([]string, len(targets))
	ackSupported := make([]bool, len(targets))
	targetAck := make(map[string]bool, len(targets))
	for i, target := range targets {
		connectionIDs[i], ackSupported[i] = target.ConnectionID, target.AckSupported
		targetAck[target.ConnectionID] = target.AckSupported
	}
	rows, err := tx.Query(ctx, `
		select connection_id, node_id, ack_supported
		from managed_realtime_direct_message_deliveries
		where endpoint_id = $1 and message_id = $2 and connection_id = any($3::text[])
	`, endpointID, messageID, connectionIDs)
	if err != nil {
		return nil, fmt.Errorf("state: check direct message targets: %w", err)
	}
	existing := make(map[string]struct{}, len(targets))
	for rows.Next() {
		var connectionID, existingNode string
		var existingAckSupported bool
		if err := rows.Scan(&connectionID, &existingNode, &existingAckSupported); err != nil {
			rows.Close()
			return nil, fmt.Errorf("state: scan direct message target: %w", err)
		}
		if existingNode != nodeID {
			rows.Close()
			return nil, ErrManagedRealtimeDirectMessageInvalid
		}
		if targetAck[connectionID] != existingAckSupported {
			rows.Close()
			return nil, ErrManagedRealtimeDirectMessageInvalid
		}
		existing[connectionID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("state: read direct message targets: %w", err)
	}
	rows.Close()
	newCount := len(targets) - len(existing)
	if newCount > 0 {
		var deliveryCount int
		if err := tx.QueryRow(ctx, `
			select count(*) from managed_realtime_direct_message_deliveries d
			join managed_realtime_direct_message_receipts r using (endpoint_id, message_id)
			where d.endpoint_id = $1 and r.expires_at > clock_timestamp()
		`, endpointID).Scan(&deliveryCount); err != nil {
			return nil, fmt.Errorf("state: count direct message deliveries: %w", err)
		}
		if deliveryCount+newCount > ManagedRealtimeDirectMessageMaxDeliveries {
			return nil, ErrManagedRealtimeDirectMessageLimit
		}
	}
	rows, err = tx.Query(ctx, `
		insert into managed_realtime_direct_message_deliveries
			(endpoint_id, message_id, connection_id, node_id, ack_supported, queue_status)
		select $1, $2, target.connection_id, $4, target.ack_supported,
			case when target.ack_supported then 'pending' else 'unsupported' end
		from unnest($3::text[], $5::boolean[]) as target(connection_id, ack_supported)
		on conflict (endpoint_id, message_id, connection_id) do nothing
		returning connection_id
	`, endpointID, messageID, connectionIDs, nodeID, ackSupported)
	if err != nil {
		return nil, fmt.Errorf("state: register direct message targets: %w", err)
	}
	created := make([]string, 0, newCount)
	for rows.Next() {
		var connectionID string
		if err := rows.Scan(&connectionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("state: scan registered direct message target: %w", err)
		}
		created = append(created, connectionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("state: read registered direct message targets: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit direct message targets: %w", err)
	}
	sort.Strings(created)
	return created, nil
}

func (s *PgStore) UpdateManagedRealtimeDirectMessageDeliveries(ctx context.Context, endpointID, messageID, nodeID string, results []ManagedRealtimeDirectMessageDeliveryResult) error {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || validateManagedRealtimeDirectMessageNode(nodeID) != nil || validateManagedRealtimeDirectMessageResults(results) != nil {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	if len(results) == 0 {
		return nil
	}
	connectionIDs, queueStatuses := make([]string, len(results)), make([]string, len(results))
	for i, result := range results {
		connectionIDs[i], queueStatuses[i] = result.ConnectionID, result.QueueStatus
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin direct message delivery update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invalid bool
	if err := tx.QueryRow(ctx, `
		select exists(
			select 1 from unnest($4::text[]) target(connection_id)
			left join managed_realtime_direct_message_deliveries d
			  on d.endpoint_id = $1 and d.message_id = $2 and d.connection_id = target.connection_id and d.node_id = $3
			where d.connection_id is null
		)
	`, endpointID, messageID, nodeID, connectionIDs).Scan(&invalid); err != nil {
		return fmt.Errorf("state: validate direct message delivery update: %w", err)
	}
	if invalid {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		update managed_realtime_direct_message_deliveries d
		set queue_status = case when d.acknowledged_at is not null then d.queue_status else result.queue_status end,
		    queued_at = case when result.queue_status = 'queued' then coalesce(d.queued_at, clock_timestamp()) else d.queued_at end
		from unnest($4::text[], $5::text[]) as result(connection_id, queue_status)
		where d.endpoint_id = $1 and d.message_id = $2 and d.node_id = $3 and d.connection_id = result.connection_id
	`, endpointID, messageID, nodeID, connectionIDs, queueStatuses); err != nil {
		return fmt.Errorf("state: update direct message delivery outcomes: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit direct message delivery update: %w", err)
	}
	return nil
}

func (s *PgStore) AcknowledgeManagedRealtimeDirectMessage(ctx context.Context, endpointID, messageID, nodeID, connectionID string) error {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || validateManagedRealtimeDirectMessageNode(nodeID) != nil || connectionID == "" || len(connectionID) > 128 {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	var acknowledgedAt time.Time
	err := s.pool.QueryRow(ctx, `
		update managed_realtime_direct_message_deliveries d
		set acknowledged_at = coalesce(acknowledged_at, clock_timestamp())
		from managed_realtime_direct_message_receipts r
		where d.endpoint_id = $1 and d.message_id = $2 and d.connection_id = $4 and d.node_id = $3
		  and d.ack_supported and r.endpoint_id = d.endpoint_id and r.message_id = d.message_id
		  and r.expires_at > clock_timestamp()
		returning d.acknowledged_at
	`, endpointID, messageID, nodeID, connectionID).Scan(&acknowledgedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: acknowledge direct message: %w", err)
	}
	return nil
}

func (s *PgStore) CompleteManagedRealtimeDirectMessageReceipt(ctx context.Context, endpointID, messageID string, summary ManagedRealtimeDirectMessageSummary) error {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || summary.Recipients < 0 || summary.Queued < 0 || summary.Unsupported < 0 || summary.QueueFull < 0 || summary.Failed < 0 {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("state: encode direct message summary: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		update managed_realtime_direct_message_receipts
		set summary = $3::jsonb, dispatch_complete = true, dispatch_lease_until = clock_timestamp()
		where endpoint_id = $1 and message_id = $2 and expires_at > clock_timestamp()
	`, endpointID, messageID, encoded)
	if err != nil {
		return fmt.Errorf("state: complete direct message receipt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) GetManagedRealtimeDirectMessageReceipt(ctx context.Context, endpointID, messageID string) (ManagedRealtimeDirectMessageReceipt, error) {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil {
		return ManagedRealtimeDirectMessageReceipt{}, ErrManagedRealtimeDirectMessageInvalid
	}
	var out ManagedRealtimeDirectMessageReceipt
	var encodedSummary []byte
	err := s.pool.QueryRow(ctx, `
		select endpoint_id, message_id, created_at, expires_at, dispatch_complete, summary
		from managed_realtime_direct_message_receipts
		where endpoint_id = $1 and message_id = $2 and expires_at > clock_timestamp()
	`, endpointID, messageID).Scan(&out.EndpointID, &out.MessageID, &out.CreatedAt, &out.ExpiresAt, &out.DispatchComplete, &encodedSummary)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeDirectMessageReceipt{}, ErrNotFound
	}
	if err != nil {
		return ManagedRealtimeDirectMessageReceipt{}, fmt.Errorf("state: read direct message receipt: %w", err)
	}
	if len(encodedSummary) != 0 {
		if err := json.Unmarshal(encodedSummary, &out.Summary); err != nil {
			return ManagedRealtimeDirectMessageReceipt{}, fmt.Errorf("state: decode direct message receipt summary: %w", err)
		}
	}
	rows, err := s.pool.Query(ctx, `
		select connection_id, ack_supported, queue_status, created_at, queued_at, acknowledged_at
		from managed_realtime_direct_message_deliveries
		where endpoint_id = $1 and message_id = $2
		order by connection_id
	`, endpointID, messageID)
	if err != nil {
		return ManagedRealtimeDirectMessageReceipt{}, fmt.Errorf("state: list direct message receipt deliveries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var delivery managedRealtimeDirectMessageDeliveryState
		var queuedAt, acknowledgedAt pgtype.Timestamptz
		if err := rows.Scan(&delivery.connectionID, &delivery.ackSupported, &delivery.queueStatus, &delivery.createdAt, &queuedAt, &acknowledgedAt); err != nil {
			return ManagedRealtimeDirectMessageReceipt{}, fmt.Errorf("state: scan direct message receipt delivery: %w", err)
		}
		if queuedAt.Valid {
			value := queuedAt.Time
			delivery.queuedAt = &value
		}
		if acknowledgedAt.Valid {
			value := acknowledgedAt.Time
			delivery.acknowledgedAt = &value
		}
		out.Deliveries = append(out.Deliveries, managedRealtimeDirectDeliveryProjection(delivery, time.Now().UTC()))
	}
	if err := rows.Err(); err != nil {
		return ManagedRealtimeDirectMessageReceipt{}, fmt.Errorf("state: read direct message receipt deliveries: %w", err)
	}
	return out, nil
}

func (s *PgStore) PruneExpiredManagedRealtimeDirectMessageReceipts(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > ManagedRealtimeDirectMessageReceiptPruneMaxBatch {
		return 0, ErrManagedRealtimeDirectMessageInvalid
	}
	tag, err := s.pool.Exec(ctx, `
		with expired as (
			select ctid from managed_realtime_direct_message_receipts
			where expires_at <= clock_timestamp()
			order by expires_at
			limit $1
			for update skip locked
		)
		delete from managed_realtime_direct_message_receipts r
		using expired e where r.ctid = e.ctid
	`, batch)
	if err != nil {
		return 0, fmt.Errorf("state: prune expired direct message receipts: %w", err)
	}
	return tag.RowsAffected(), nil
}
