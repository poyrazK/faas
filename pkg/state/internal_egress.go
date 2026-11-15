package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// InternalEgressObserver subtracts the bytes an instance sent to internal
// destinations from its billed egress (billing review R8-2,
// ADR-20261114100000). PgStore and MemStore implement it; meterd calls it
// only when [meter] internal_egress_unbilled is on.
type InternalEgressObserver interface {
	// SubtractInternalEgress takes vmmd's cumulative internal-egress reading
	// for the instance, derives the bytes since the checkpoint (a reading
	// below it is a counter reset: all of it is new), and subtracts them from
	// the minute's usage_minutes.net_tx_bytes, never below zero. Bytes the
	// row could not absorb carry to the next call, at most one reading's
	// worth. It returns the bytes subtracted. The checkpoint advances in the
	// same transaction, so a retried call subtracts nothing twice. It does
	// nothing until AppendNetworkUsageObservation has created the
	// instance's checkpoint.
	SubtractInternalEgress(ctx context.Context, appID, instanceID string, minute time.Time, internalCumulative int64) (int64, error)
}

// internalEgressStep is the checkpoint arithmetic both stores share: the new
// bytes since prev, the bytes to subtract from a row holding rowNetTx, and the
// carry left for the next reading.
func internalEgressStep(prev int64, prevValid bool, pending, cumulative, rowNetTx int64) (subtract, nextPending int64) {
	delta := cumulative
	if prevValid && cumulative >= prev {
		delta = cumulative - prev
	}
	want := delta
	if pending > math.MaxInt64-want {
		want = math.MaxInt64
	} else {
		want += pending
	}
	subtract = min(want, max(rowNetTx, 0))
	// A carry larger than one reading would mean the internal counter ran
	// ahead of the veth counter for longer than one poll; drop the excess
	// rather than let it erase later public egress.
	return subtract, min(want-subtract, delta)
}

const (
	internalEgressCheckpointLock = `select internal_tx_bytes, internal_tx_valid, internal_tx_pending
	   from meter_network_checkpoints
	  where instance_id = $1
	  for update`
	internalEgressUsageLock = `select net_tx_bytes
	   from usage_minutes
	  where instance_id = $1 and minute = $2 and app_id = $3 and meter_kind = 'app'
	  for update`
	internalEgressUsageUpdate = `update usage_minutes
	    set net_tx_bytes = net_tx_bytes - $1
	  where instance_id = $2 and minute = $3 and app_id = $4 and meter_kind = 'app'`
	internalEgressCheckpointUpdate = `update meter_network_checkpoints
	    set internal_tx_bytes = $2,
	        internal_tx_valid = true,
	        internal_tx_pending = $3,
	        updated_at = now()
	  where instance_id = $1`
)

// SubtractInternalEgress implements InternalEgressObserver.
func (s *PgStore) SubtractInternalEgress(ctx context.Context, appID, instanceID string, minute time.Time, internalCumulative int64) (int64, error) {
	if internalCumulative < 0 {
		return 0, fmt.Errorf("state: internal egress counter must be non-negative")
	}
	key := minute.UTC().Truncate(time.Minute)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: subtract internal egress: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var prev, pending int64
	var prevValid bool
	err = tx.QueryRow(ctx, internalEgressCheckpointLock, instanceID).Scan(&prev, &prevValid, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("state: subtract internal egress: lock checkpoint: %w", err)
	}
	var rowNetTx int64
	err = tx.QueryRow(ctx, internalEgressUsageLock, instanceID, key, appID).Scan(&rowNetTx)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("state: subtract internal egress: usage row missing for instance %s at %s", instanceID, key.Format(time.RFC3339))
	}
	if err != nil {
		return 0, fmt.Errorf("state: subtract internal egress: lock usage row: %w", err)
	}
	subtract, nextPending := internalEgressStep(prev, prevValid, pending, internalCumulative, rowNetTx)
	if subtract > 0 {
		if _, err := tx.Exec(ctx, internalEgressUsageUpdate, subtract, instanceID, key, appID); err != nil {
			return 0, fmt.Errorf("state: subtract internal egress: update usage row: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, internalEgressCheckpointUpdate, instanceID, internalCumulative, nextPending); err != nil {
		return 0, fmt.Errorf("state: subtract internal egress: advance checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: subtract internal egress: commit: %w", err)
	}
	return subtract, nil
}

// SubtractInternalEgress mirrors PgStore under MemStore's single mutex.
func (m *MemStore) SubtractInternalEgress(_ context.Context, appID, instanceID string, minute time.Time, internalCumulative int64) (int64, error) {
	if internalCumulative < 0 {
		return 0, fmt.Errorf("state: internal egress counter must be non-negative")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	checkpoint, ok := m.networkUsageCheckpoints[instanceID]
	if !ok {
		return 0, nil
	}
	key := minute.UTC().Truncate(time.Minute)
	usageIndex := -1
	for i := range m.usage {
		if m.usage[i].AppID == appID && m.usage[i].InstanceID == instanceID && m.usage[i].Minute.Equal(key) {
			usageIndex = i
			break
		}
	}
	if usageIndex < 0 {
		return 0, fmt.Errorf("state: subtract internal egress: usage row missing for instance %s at %s", instanceID, key.Format(time.RFC3339))
	}
	subtract, nextPending := internalEgressStep(checkpoint.internalTx, checkpoint.internalTxValid,
		checkpoint.internalTxPending, internalCumulative, m.usage[usageIndex].NetTxBytes)
	checkpoint.internalTx = internalCumulative
	checkpoint.internalTxValid = true
	checkpoint.internalTxPending = nextPending
	m.networkUsageCheckpoints[instanceID] = checkpoint
	if subtract > 0 {
		previous := m.usage[usageIndex]
		m.usage[usageIndex].NetTxBytes -= subtract
		m.recomputeMonthLocked(m.usage[usageIndex].AccountID, appID, key)
		m.retainFinancialUsageLocked(previous, m.usage[usageIndex])
	}
	return subtract, nil
}
