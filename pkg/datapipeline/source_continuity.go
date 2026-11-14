package datapipeline

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type sourceSlotWitness struct {
	Peer, Confirmed string
	Epoch           string `json:",omitempty"`
	Pending         bool   `json:",omitempty"`
	Ready           bool   `json:",omitempty"`
}

func sourceContinuityFence(p Pipeline) string { return "source-stream:" + p.ID }

func sourceQualificationFence(p Pipeline, change string) bool {
	return change == sourceStorageFence(p) || change == sourceContinuityFence(p)
}

func ensureSourceSlotReceipt(ctx context.Context, target receiptSQL) error {
	var present, guarded bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('gregale_pipeline.mirrors') AND attname='source_slot' AND NOT attisdropped),EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('gregale_pipeline.mirrors') AND conname='source_stream_read_gate')`).Scan(&present, &guarded); err != nil {
		return err
	}
	if present && guarded {
		return nil
	}
	ddl := `ALTER TABLE gregale_pipeline.mirrors `
	if !present {
		ddl += `ADD COLUMN source_slot jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(source_slot)='object'), `
	}
	ddl += `ADD CONSTRAINT source_stream_read_gate CHECK(NOT readable OR NOT coalesce((source_slot->>'Pending')::boolean,false))`
	_, err := target.Exec(ctx, ddl)
	return err
}

// Standalone verification fixtures and older mirrors may have no stream receipt.
// Existing subscriptions qualify that missing baseline before exposing reads.
func loadSourceSlot(ctx context.Context, p Pipeline, target receiptSQL) (*sourceSlotWitness, error) {
	var present bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('gregale_pipeline.mirrors') AND attname='source_slot' AND NOT attisdropped)`).Scan(&present); err != nil || !present {
		return nil, err
	}
	var encoded []byte
	if err := target.QueryRow(ctx, `SELECT source_slot FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation).Scan(&encoded); err != nil {
		return nil, err
	}
	var w sourceSlotWitness
	if json.Unmarshal(encoded, &w) != nil {
		return nil, ErrConflict
	}
	if w.Confirmed == "" {
		return nil, nil
	}
	return &w, nil
}

func saveSourceSlot(ctx context.Context, p Pipeline, target receiptSQL, w sourceSlotWitness) error {
	encoded, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tag, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET source_slot=$3 WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// A live sender holds the slot and prevents replacement or explicit advancement.
// After it changes, only an unchanged confirmed checkpoint establishes continuity.
// Retained WAL may still overlap a checkpoint skipped by pg_replication_slot_advance;
// neither that overlap nor subsequent subscriber progress proves it was consumed.
func sampleSourceSlot(ctx context.Context, p Pipeline, source capacityQuery, previous *sourceSlotWitness) (sourceSlotWitness, bool, error) {
	confirmed := ""
	if previous != nil {
		confirmed = previous.Confirmed
	}
	var w sourceSlotWitness
	var active, unchanged bool
	err := source.QueryRow(ctx, `SELECT coalesce(s.confirmed_flush_lsn::text,''),s.active,
 coalesce(s.active_pid::text||':'||extract(epoch FROM a.backend_start)::text,''),
 coalesce(s.restart_lsn<=nullif($2,'')::pg_lsn AND s.confirmed_flush_lsn=nullif($2,'')::pg_lsn,false)
 FROM pg_replication_slots s LEFT JOIN pg_stat_activity a ON a.pid=s.active_pid
 WHERE s.slot_name=$1 AND s.plugin='pgoutput' AND s.database=current_database() AND coalesce(s.wal_status,'')<>'lost'`, pipelineName(p), confirmed).Scan(&w.Confirmed, &active, &w.Peer, &unchanged)
	if err != nil {
		return w, false, err
	}
	if w.Confirmed == "" || active && w.Peer == "" {
		return w, false, ErrUnavailable
	}
	continuous := previous != nil && (unchanged || w.Peer != "" && w.Peer == previous.Peer)
	if previous != nil {
		w.Epoch, w.Pending, w.Ready = previous.Epoch, previous.Pending, previous.Ready
		// A stopped stream must not advance the last witnessed checkpoint.
		if continuous && !active {
			w.Peer, w.Confirmed = previous.Peer, previous.Confirmed
		}
	}
	return w, continuous, nil
}

func recordSourceSlot(ctx context.Context, p Pipeline, source capacityQuery, target *pgx.Conn, creating bool, current func(context.Context) error) error {
	if err := ensureSourceSlotReceipt(ctx, target); err != nil {
		return err
	}
	previous, err := loadSourceSlot(ctx, p, target)
	if err != nil {
		return err
	}
	w, continuous, err := sampleSourceSlot(ctx, p, source, previous)
	if err != nil {
		return err
	}
	// New subscriptions capture their first sender while selected source writes
	// are locked out. Existing subscriptions must prove their previous boundary.
	newStream := creating && (previous == nil || !previous.Ready)
	qualify := !continuous && !newStream
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if qualify {
		var phase, change string
		if err = tx.QueryRow(ctx, `SELECT schema_phase,schema_change FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation).Scan(&phase, &change); err != nil {
			return err
		}
		fullSchema := phase != "" && p.SchemaChange != nil && change == p.SchemaChange.ID && change == p.SchemaApproval
		if tableWorkflow(p) || activeTableCancellation(p) || phase != "" && !sourceQualificationFence(p, change) && !fullSchema {
			return &blocked{"replication_continuity_lost", true}
		}
		if err = updateMirrorReads(ctx, p, tx, false); err != nil {
			return err
		}
		if !fullSchema {
			if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_phase='verifying',schema_change=$3 WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation, sourceContinuityFence(p)); err != nil {
				return err
			}
		}
		// A new epoch invalidates even a previously passed proof with identical
		// table storage. Logical cancellation remains bound to its original intent.
		w.Epoch, w.Pending = uuid.NewString(), true
	}
	if err = saveSourceSlot(ctx, p, tx, w); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Only an all-selected-table proof can release a shared continuity gate. Clear
// it in the same transaction as schema completion or ordinary read opening.
func finishSourceSlotQualification(ctx context.Context, p Pipeline, target receiptSQL, relations []relation) error {
	w, err := loadSourceSlot(ctx, p, target)
	if err != nil || w == nil || !w.Pending {
		return err
	}
	var tables []string
	if err = target.QueryRow(ctx, `SELECT tables FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation).Scan(&tables); err != nil {
		return err
	}
	if !slices.Equal(tables, relationNames(relations)) {
		return &blocked{"replication_continuity_lost", true}
	}
	w.Pending = false
	return saveSourceSlot(ctx, p, target, *w)
}

// Preserve the latest boundary of a trusted sender before deliberately stopping
// it. Verification and reader-policy work can outlast an earlier observation.
// This never adopts an uncertain stream or starts qualification during pause.
func rememberSourceSlotBeforePause(ctx context.Context, p Pipeline, source capacityQuery, target *pgx.Conn, current func(context.Context) error) error {
	previous, err := loadSourceSlot(ctx, p, target)
	if err != nil || previous == nil {
		return err
	}
	witnessCtx, cancel := context.WithTimeout(ctx, api.DataPipelineVerificationLockTimeout)
	w, continuous, err := sampleSourceSlot(witnessCtx, p, source, previous)
	cancel()
	if err != nil || !continuous {
		return err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = saveSourceSlot(ctx, p, tx, w); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func captureInitialSourceSlot(ctx context.Context, p Pipeline, source capacityQuery, target *pgx.Conn, current func(context.Context) error) error {
	deadline := time.NewTimer(api.DataPipelineVerificationQueryTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(min(api.DataPipelineWorkerIdleInterval, api.DataPipelineVerificationLockTimeout))
	defer poll.Stop()
	for {
		w, _, err := sampleSourceSlot(ctx, p, source, nil)
		if err != nil {
			return err
		}
		if w.Peer != "" {
			return recordSourceSlot(ctx, p, source, target, true, current)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			// Copying remains gated. An unobserved first stream will need
			// an unchanged checkpoint or qualification on the next reconciliation.
			return nil
		case <-poll.C:
		}
	}
}

func checkProofContinuity(ctx context.Context, p Pipeline, source capacityQuery, target receiptSQL) error {
	w, err := loadSourceSlot(ctx, p, target)
	if err != nil || w == nil {
		return err
	}
	_, continuous, err := sampleSourceSlot(ctx, p, source, w)
	if err != nil {
		return err
	}
	if !continuous {
		return errVerificationSourceRewritten
	}
	return nil
}
