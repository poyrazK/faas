package datapipeline

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func verificationTransient(err error) bool {
	var pg *pgconn.PgError
	return errors.Is(err, errVerificationSourceRewritten) || errors.As(err, &pg) && (pg.Code == "55P03" || pg.Code == "57014" || pg.Code == "40P01" || pg.Code == "40001")
}
func countDirtyVerification(ctx context.Context, p Pipeline, target receiptSQL, relations []relation) (int64, error) {
	var count int64
	for i := range relations {
		ranges, _ := verificationNames(p, i)
		var n int64
		if err := target.QueryRow(ctx, "SELECT count(*) FROM "+ranges+" WHERE dirty").Scan(&n); err != nil {
			return 0, err
		}
		count += n
	}
	return count, nil
}
func verificationCancelled(p Pipeline, intent string) bool {
	return p.VerificationProgress != nil && p.VerificationProgress.State == "cancelled" && (p.VerificationProgress.IntentID == intent || p.VerificationProgress.ID == intent)
}
func verifyResumable(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, ob *Observation, current func(context.Context) error) (bool, error) {
	intent, spec := verificationID(p, relations)
	c, err := loadVerification(ctx, target, p)
	if err != nil {
		return false, err
	}
	if verificationCancelled(p, intent) {
		ob.VerificationProgress = p.VerificationProgress
		if c != nil {
			if err = cleanupVerification(ctx, p, target); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	storage, err := verificationSourceStorage(ctx, source, relations)
	if err != nil {
		return false, err
	}
	id := physicalVerificationID(intent, storage)
	if c == nil || c.Progress.ID != id {
		restarts := 0
		if c != nil && c.Progress.IntentID == intent {
			restarts = c.Progress.Restarts + 1
		}
		c, err = startVerification(ctx, p, target, relations, id, intent, spec, storage, restarts, current)
		if err != nil {
			return false, err
		}
	}
	if c.Table < 0 || c.Table > len(relations) || c.Progress.TablesTotal != len(relations) || c.Progress.Ranges < 0 || c.Progress.Ranges > api.DataPipelineVerificationRangesMax {
		return false, &blocked{"verification_tracking_lost", true}
	}
	if c.Table < len(relations) && len(c.Cursor) != 0 && len(c.Cursor) != len(primaryColumns(relations[c.Table])) {
		return false, &blocked{"verification_tracking_lost", true}
	}
	ob.VerificationProgress = &c.Progress
	if c.Progress.State == "passed" {
		return true, nil
	}
	if c.Spec != spec {
		return false, ErrConflict
	}
	if c.Progress.State == "mismatch" || c.Progress.State == "limited" {
		return false, retireVerification(ctx, p, target, relations, c, current)
	}
	if err = checkVerificationTracking(ctx, p, target, relations, id); err != nil {
		return false, err
	}
	for range api.DataPipelineVerificationChunksPerTick {
		var dirty *verificationRange
		index := c.Table
		if index >= len(relations) {
			c.Progress.Phase = "changes"
			index = -1
			for i, r := range relations {
				dirty, err = dirtyVerificationRange(ctx, p, target, r, i)
				if err != nil {
					return false, err
				}
				if dirty != nil {
					index = i
					break
				}
			}
			if index < 0 {
				c.Progress.DirtyRanges = 0
				c.Progress.Table = ""
				c.Progress.Reason = ""
				return true, nil
			}
		}
		c.Progress.Table = relations[index].Name
		moved, err := compareVerificationRange(ctx, p, source, target, relations, c, index, dirty, current)
		if verificationTransient(err) {
			c.Progress.Reason = "waiting_for_locks"
			if errors.Is(err, errVerificationSourceRewritten) {
				c.Progress.Reason = "source_storage_changed"
			}
			return false, nil
		}
		if err != nil {
			return false, err
		}
		ob.VerificationProgress = &c.Progress
		if c.Progress.State == "mismatch" || c.Progress.State == "limited" {
			return false, retireVerification(ctx, p, target, relations, c, current)
		}
		if !moved || c.Progress.State != "running" {
			return false, nil
		}
	}
	c.Progress.DirtyRanges, err = countDirtyVerification(ctx, p, target, relations)
	return err == nil && c.Table == len(relations) && c.Progress.DirtyRanges <= api.DataPipelineVerificationSealRangesMax, err
}

// Seal under source and destination write locks after native acknowledgement.
// Range tracking covers every write since each comparison. A clean journal at
// this boundary proves all ranges simultaneously; independent scans never do.
func sealVerification(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, ob *Observation, current func(context.Context) error, complete func(pgx.Tx) error) (bool, error) {
	c, err := loadVerification(ctx, target, p)
	if err != nil || c == nil {
		return false, err
	}
	intent, spec := verificationID(p, relations)
	id := c.Progress.ID
	if c.Progress.IntentID != intent || c.Spec != spec {
		return false, ErrConflict
	}
	ob.VerificationProgress = &c.Progress
	if c.Progress.State == "passed" {
		return true, nil
	}
	if c.Table != len(relations) || c.Progress.State != "running" {
		return false, nil
	}
	copy := *c
	c = &copy
	sourceTx, err := lockVerificationSource(ctx, source, p.Tables)
	if err != nil {
		return false, err
	}
	defer func() { _ = sourceTx.Rollback(ctx) }()
	caught, err := waitVerificationCatchup(ctx, p, source)
	if err != nil || !caught {
		return false, err
	}
	storage, err := verificationSourceStorage(ctx, sourceTx, relations)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(storage, c.Storage) {
		return false, errVerificationSourceRewritten
	}
	actual, _, err := inspectSource(ctx, p, source)
	if err != nil {
		return false, err
	}
	_, actualSpec := verificationID(p, actual)
	if actualSpec != spec {
		return false, ErrConflict
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return false, err
	}
	names := make([]string, len(p.Tables))
	for i, name := range p.Tables {
		names[i] = tableIdent(name)
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+strings.Join(names, ",")+" IN SHARE MODE"); err != nil {
		return false, err
	}
	if err = checkVerificationTracking(ctx, p, tx, relations, id); err != nil {
		return false, err
	}
	dirty, err := countDirtyVerification(ctx, p, tx, relations)
	if err != nil {
		return false, err
	}
	if dirty > api.DataPipelineVerificationSealRangesMax {
		c.Progress.DirtyRanges = dirty
		return false, nil
	}
	for n := int64(0); n < dirty; n++ {
		for i, r := range relations {
			pending, err := dirtyVerificationRange(ctx, p, tx, r, i)
			if err != nil {
				return false, err
			}
			if pending == nil {
				continue
			}
			moved, err := compareLockedVerificationRange(ctx, p, sourceTx, tx, r, c, i, pending)
			if err != nil {
				return false, err
			}
			if !moved || c.Progress.State != "running" {
				if c.Progress.State != "running" {
					if err = saveVerification(ctx, tx, p, c); err != nil {
						return false, err
					}
					if err = current(ctx); err != nil {
						return false, err
					}
					if err = tx.Commit(ctx); err != nil {
						return false, err
					}
					ob.VerificationProgress = &c.Progress
				}
				return false, nil
			}
			break
		}
	}
	dirty, err = countDirtyVerification(ctx, p, tx, relations)
	if err != nil {
		return false, err
	}
	if dirty != 0 {
		c.Progress.DirtyRanges = dirty
		return false, nil
	}
	c.Progress.DirtyRanges = 0
	if err = checkVerificationCoverage(ctx, p, tx, relations, c.Progress.Ranges); err != nil {
		return false, err
	}
	if err = removeVerification(ctx, p, tx, relations); err != nil {
		return false, err
	}
	c.Progress.State, c.Progress.Phase, c.Progress.Table, c.Progress.Reason = "passed", "complete", "", ""
	c.Progress.CompletedAt = time.Now().UTC()
	if complete != nil {
		if err = complete(tx); err != nil {
			return false, err
		}
	}
	if err = saveVerification(ctx, tx, p, c); err != nil {
		return false, err
	}
	if err = current(ctx); err != nil {
		return false, err
	}
	var held int
	if err = sourceTx.QueryRow(ctx, "SELECT 1").Scan(&held); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	ob.VerificationProgress = &c.Progress
	return true, nil
}

func retireVerification(ctx context.Context, p Pipeline, target *pgx.Conn, relations []relation, c *verificationCheckpoint, current func(context.Context) error) error {
	if c.Progress.Phase == "stopped" {
		return nil
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = removeVerification(ctx, p, tx, relations); err != nil {
		return err
	}
	copy := *c
	copy.Progress.Phase = "stopped"
	if err = saveVerification(ctx, tx, p, &copy); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	*c = copy
	return nil
}
