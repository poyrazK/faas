package datapipeline

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sourceStorageFence(p Pipeline) string { return "source-storage:" + p.ID }

func canRetrySourceStorage(p Pipeline) bool {
	return p.DesiredState == "running" && p.State == "blocked" && (p.Reason == "source_storage_verification" || p.Reason == "replication_continuity_verification") && p.ObservationFresh
}

func ensureSourceStorageReceipt(ctx context.Context, target receiptSQL) error {
	var present bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('gregale_pipeline.mirrors') AND attname='source_storage' AND NOT attisdropped)`).Scan(&present); err != nil {
		return err
	}
	if present {
		return nil
	}
	_, err := target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors ADD COLUMN source_storage jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(source_storage)='object')`)
	return err
}

func saveSourceStorage(ctx context.Context, p Pipeline, target receiptSQL, relations []relation, storage []string) error {
	if len(relations) != len(storage) {
		return ErrConflict
	}
	values := make(map[string]string, len(relations))
	for i, r := range relations {
		values[r.Name] = storage[i]
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	tag, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors m SET source_storage=
 (SELECT coalesce(jsonb_object_agg(key,value),'{}'::jsonb) FROM jsonb_each(m.source_storage) WHERE key=ANY(m.tables))||$3::jsonb
 WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// A baseline is taken before a new mirror can begin native copying. Existing
// mirrors with no baseline must verify their data rather than adopt current
// source storage as evidence about previously imported rows.
func initializeSourceStorage(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, current func(context.Context) error) error {
	if err := ensureSourceStorageReceipt(ctx, target); err != nil {
		return err
	}
	storage, err := verificationSourceStorage(ctx, source, relations)
	if err != nil {
		return err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = saveSourceStorage(ctx, p, tx, relations, storage); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sourceStorageChanged(ctx context.Context, p Pipeline, source capacityQuery, target receiptSQL, relations []relation) (bool, error) {
	if len(relations) == 0 {
		return false, nil
	}
	if err := ensureSourceStorageReceipt(ctx, target); err != nil {
		return false, err
	}
	var encoded []byte
	var change, phase string
	if err := target.QueryRow(ctx, `SELECT source_storage,schema_change,schema_phase FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&encoded, &change, &phase); err != nil {
		return false, err
	}
	if sourceQualificationFence(p, change) && phase == "verifying" {
		return true, nil
	}
	var recorded map[string]string
	if json.Unmarshal(encoded, &recorded) != nil {
		return false, ErrConflict
	}
	actual, err := verificationSourceStorage(ctx, source, relations)
	if err != nil {
		return false, err
	}
	for i, r := range relations {
		if recorded[r.Name] != actual[i] {
			return true, nil
		}
	}
	return false, nil
}

func checkRecordedSourceStorage(ctx context.Context, p Pipeline, source capacityQuery, target receiptSQL, relations []relation) error {
	changed, err := sourceStorageChanged(ctx, p, source, target, relations)
	if err != nil {
		return err
	}
	if changed {
		return &blocked{"source_storage_changed", true}
	}
	return nil
}

func gateSourceStorage(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, current func(context.Context) error) (bool, error) {
	changed, err := sourceStorageChanged(ctx, p, source, target, relations)
	if err != nil || !changed {
		return changed, err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return false, err
	}
	if err = updateMirrorReads(ctx, p, tx, false); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_change=CASE WHEN schema_phase='verifying' AND schema_change=$4 THEN $4 ELSE $3 END,schema_phase='verifying' WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring AND (schema_phase='' OR schema_change IN ($3,$4))`, p.ID, p.Generation, sourceStorageFence(p), sourceContinuityFence(p))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, ErrConflict
	}
	if err = current(ctx); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func recheckSourceStorage(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, ob *Observation, current func(context.Context) error) error {
	p.VerifyRequested = true
	ob.State, ob.Reason, ob.Verification = "blocked", "source_storage_verification", "pending"
	w, err := loadSourceSlot(ctx, p, target)
	if err != nil {
		return err
	}
	if w != nil && w.Pending {
		ob.Reason = "replication_continuity_verification"
	}
	ready, err := verifyResumable(ctx, p, source, target, relations, ob, current)
	if verificationTransient(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if v := ob.VerificationProgress; v != nil {
		switch v.State {
		case "mismatch":
			ob.Verification = "mismatch"
			if w != nil && w.Pending {
				return &blocked{"replication_continuity_lost", true}
			}
			return &blocked{"source_storage_changed", true}
		case "limited":
			ob.Verification = "inconclusive"
			return nil
		case "cancelled":
			ob.Verification = "unknown"
			return nil
		}
	}
	if !ready {
		return nil
	}
	sealed, err := sealVerification(ctx, p, source, target, relations, ob, current, nil)
	if verificationTransient(err) {
		return nil
	}
	if err != nil || !sealed {
		return err
	}
	if err = acknowledgeSourceStorage(ctx, p, source, target, relations, current); verificationTransient(err) {
		return nil
	} else if err != nil {
		return err
	}
	ob.State, ob.Reason, ob.Verification = "live", "", "passed"
	ob.VerifiedAt = ob.VerificationProgress.CompletedAt
	return nil
}

// Replay a passed proof under a current source lock. This also qualifies legacy
// mirrors whose last successful verification predates the storage receipt.
func acknowledgeSourceStorage(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, current func(context.Context) error) error {
	locked, err := lockVerificationSource(ctx, source, p.Tables)
	if err != nil {
		return err
	}
	defer func() { _ = locked.Rollback(ctx) }()
	actualStorage, storage, err := verificationStorage(ctx, p, locked, target, relations)
	if err != nil {
		return err
	}
	c, err := loadVerification(ctx, target, p)
	if err != nil {
		return err
	}
	intent, spec := verificationID(p, relations)
	if c == nil || c.Progress.State != "passed" || c.Progress.IntentID != intent || c.Spec != spec {
		return ErrConflict
	}
	if !reflect.DeepEqual(storage, c.Storage) {
		return errVerificationSourceRewritten
	}
	if err = checkProofContinuity(ctx, p, locked, target); err != nil {
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
	if err = saveSourceStorage(ctx, p, tx, relations, actualStorage); err != nil {
		return err
	}
	if err = finishSourceSlotQualification(ctx, p, tx, relations); err != nil {
		return err
	}
	if err = updateMirrorReads(ctx, p, tx, true); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_phase='',schema_change=$3 WHERE pipeline_id=$1 AND generation=$2 AND schema_change IN ($4,$5) AND schema_phase='verifying' AND NOT retiring`, p.ID, p.Generation, p.SchemaApproval, sourceStorageFence(p), sourceContinuityFence(p))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = liveRemovalFence(locked, current)(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// An implicit storage check need not have saved a pending catalog observation
// before completing. Recover its successful result from the durable proof after
// a lost final reply, without retaining a workflow gate after it has finished.
func observePassedVerification(ctx context.Context, p Pipeline, target receiptSQL, relations []relation, ob *Observation) error {
	c, err := loadVerification(ctx, target, p)
	if err != nil || c == nil || c.Progress.State != "passed" {
		return err
	}
	intent, spec := verificationID(p, relations)
	if c.Progress.IntentID == intent && c.Spec == spec {
		ob.VerificationProgress, ob.Verification, ob.VerifiedAt = &c.Progress, "passed", c.Progress.CompletedAt
	}
	return nil
}
