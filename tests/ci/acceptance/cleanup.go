package acceptance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/ci"
)

// Cleanup cancels only intent in the marker-owned test schema. Live daemons
// still own native stop. Missing stop evidence keeps both the schema and quota.
// Fixture expiry is advanced only in this isolated schema; production TTLs and
// the storage/native retirement predicates are unchanged.
func Cleanup(ctx context.Context, dsn, bucket, dir string) error {
	j, err := ReadJournal(dir)
	if err != nil || bucket == "" || bucket != j.Bucket {
		return errors.New("CI acceptance cleanup requires its journal and exact dedicated bucket")
	}
	if j.Cleanup {
		return nil
	}
	pool, err := OpenSchema(ctx, dsn, j)
	if err != nil {
		return err
	}
	defer pool.Close()
	physical, err := ci.NewGCSObjects(ctx, bucket)
	if err != nil {
		return errors.New("CI acceptance cleanup storage unavailable; reservations retained")
	}
	defer func() { _ = physical.Close() }()
	store := ci.NewPostgresStore(pool)
	blobs := &ci.ManagedBlobs{Store: store, Physical: physical}
	engine := ci.Engine{Store: store, Backend: ci.JobsBackend{Store: store, Output: ci.SealedJobsOutput{Store: store, Blobs: blobs}}}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := drainExecutions(ctx, pool, engine, j.AccountID); err != nil {
			return err
		}
		var live, pending int
		if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM instances WHERE state NOT IN ('stopped','parked','failed')),(SELECT count(*) FROM ci_executions WHERE status NOT IN ('succeeded','failed','cancelled') OR lease_until>clock_timestamp())").Scan(&live, &pending); err != nil {
			return errors.New("CI acceptance native stop evidence unavailable")
		}
		if live == 0 && pending == 0 {
			return retireObjects(ctx, pool, blobs, dir, &j)
		}
		select {
		case <-ctx.Done():
			return errors.New("CI acceptance native drain unconfirmed; keep journal, schema and native files")
		case <-ticker.C:
		}
	}
}

func drainExecutions(ctx context.Context, pool *pgxpool.Pool, engine ci.Engine, account string) error {
	rows, err := pool.Query(ctx, "SELECT id::text FROM ci_executions WHERE account_id=$1 AND status NOT IN ('succeeded','failed','cancelled')", account)
	if err != nil {
		return errors.New("CI acceptance cancellation journal unavailable")
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return errors.New("CI acceptance cancellation journal unavailable")
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.New("CI acceptance cancellation journal unavailable")
	}
	for _, id := range ids {
		if _, err := engine.Store.CancelExecution(ctx, account, id); err != nil {
			return errors.New("CI acceptance cancellation pending")
		}
		// Retry recoverable collector/controller leases on the next pass.
		_, _ = engine.Tick(ctx, account, id)
	}
	return nil
}

func retireObjects(ctx context.Context, pool *pgxpool.Pool, blobs *ci.ManagedBlobs, dir string, j *Journal) error {
	// Every row belongs to this random, marker-owned fixture schema. Keep live
	// operation leases intact so cloud I/O and late writers retain their fence.
	_, err := pool.Exec(ctx, `UPDATE ci_managed_objects SET
 created_at=least(created_at,clock_timestamp()-interval '3 hours'),
 expires_at=greatest(least(created_at,clock_timestamp()-interval '3 hours')+interval '1 second',clock_timestamp()-interval '1 second')
 WHERE state!='deleted'`)
	if err != nil {
		return errors.New("CI acceptance fixture expiry failed")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := blobs.Sweep(ctx); err != nil && !errors.Is(err, ci.ErrLeaseBusy) {
			return errors.New("CI acceptance payload retirement unconfirmed; reservations retained")
		}
		var charged, active int
		if err := pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE state!='deleted'),count(*) FILTER(WHERE op_until>clock_timestamp()) FROM ci_managed_objects").Scan(&charged, &active); err != nil {
			return errors.New("CI acceptance retirement journal unavailable")
		}
		if charged == 0 && active == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("CI acceptance payload drain incomplete; retain recovery authority")
		case <-ticker.C:
		}
	}
	rows, err := pool.Query(ctx, "SELECT 'ci-v1/'||account_id::text||'/'||id::text,digest,bytes FROM ci_managed_objects WHERE state='deleted' ORDER BY object_key")
	if err != nil {
		return errors.New("CI acceptance tombstone evidence unavailable")
	}
	defer rows.Close()
	j.Retired = nil
	for rows.Next() {
		var object RetiredObject
		if err := rows.Scan(&object.PhysicalKey, &object.Digest, &object.Bytes); err != nil {
			return errors.New("CI acceptance tombstone evidence unavailable")
		}
		j.Retired = append(j.Retired, object)
	}
	if rows.Err() != nil {
		return errors.New("CI acceptance tombstone evidence unavailable")
	}
	if len(j.Retired) > 0 {
		key := j.Retired[0].PhysicalKey
		body, err := blobs.Physical.Open(ctx, key)
		if err == nil {
			_ = body.Close()
			return errors.New("CI acceptance retired payload is still readable")
		}
		if !errors.Is(err, ci.ErrNotFound) {
			return errors.New("CI acceptance tombstone readback unavailable")
		}
		// An absent-key delete would allow this late create to resurrect bytes.
		late := "late"
		if err := blobs.Physical.Publish(ctx, key, ci.RuntimeObject{Digest: nativePayloadDigest([]byte(late)), Bytes: int64(len(late))}, strings.NewReader(late)); err == nil {
			_ = blobs.Physical.Retire(ctx, key)
			return errors.New("CI acceptance late writer bypassed the retirement fence")
		}
	}
	j.Cleanup = true
	return WriteJournal(dir, *j)
}
