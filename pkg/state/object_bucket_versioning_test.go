package state_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestObjectBucketVersioningMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	objectVersioningSuite(t, m, func(bucket string) { now = now.Add(api.ObjectBucketVersioningPropagation + time.Second) })
}
func TestObjectBucketVersioningPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectVersioningSuite(t, s, func(bucket string) {
		if _, err := pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second' WHERE bucket_id=$1`, bucket); err != nil {
			t.Fatal(err)
		}
	})
}
func objectVersioningSuite(t *testing.T, st accountingStore, advance func(string)) {
	ctx := t.Context()
	v := st.(state.ObjectBucketVersioningStore)
	cap := st.(state.ObjectCapacityStore)
	inventory := st.(state.ObjectVersionInventoryStore)
	b, _ := seedAccounting(t, st)
	token := uuid.NewString()
	if err := cap.BeginObjectWrite(ctx, b.AccountID, b.ID, token, "pending", 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	j, err := v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
	if err != nil || j.State != "waiting" {
		t.Fatal(j, err)
	}
	replay, err := v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
	if err != nil || replay.Revision != j.Revision {
		t.Fatal(replay, err)
	}
	for _, status := range []string{"Suspended", ""} {
		if _, err = v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, status); !errors.Is(err, state.ErrConflict) {
			t.Fatal(err)
		}
	}
	if _, err = v.GetObjectBucketVersioning(ctx, uuid.NewString(), b.AppID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account configuration", err)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "new", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("write bypassed configuration fence", err)
	}
	if _, err = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "delete", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("deletion bypassed transition", err)
	}
	waiting, err := v.ClaimObjectBucketVersioning(ctx, b.ID, "wait")
	if err != nil || waiting.Token != "" || waiting.LastErrorCode != "unsettled_writes" {
		t.Fatal(waiting, err)
	}
	if err = cap.SettleObjectWrite(ctx, b.AccountID, b.ID, token); err != nil {
		t.Fatal(err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "apply")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.DispatchObjectBucketVersioning(ctx, b.ID, j.Token)
	if err != nil || !j.VersionsRequired || !j.Dispatched {
		t.Fatal(j, err)
	}
	if _, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, "stale", "Enabled"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker advanced", err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, "apply", "Enabled")
	if err != nil || j.State != "propagating" || j.CapacityJobID != "" {
		t.Fatal(j, err)
	}
	if _, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "early"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("propagation gate bypassed", err)
	}
	if _, err = cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unrelated capacity scan during transition", err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "inventory")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil || j.State != "inventory" || j.CapacityJobID == "" {
		t.Fatal(j, err)
	}
	if _, err = cap.CancelObjectCapacityReconciliation(ctx, b.AccountID, b.ID, j.CapacityJobID); err != nil {
		t.Fatal(err)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "still-fenced", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cancellation opened writes", err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "replacement")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil {
		t.Fatal(j, err)
	}
	c, err := cap.ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, "scan")
	if err != nil || c.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal(c, err)
	}
	c, err = inventory.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 30}, {Identity: strings.Repeat("b", 64), Bytes: 20}})
	if err != nil || c.State != "completed" {
		t.Fatal(c, err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "verify")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].BaselineBytes != 50 || usage.Buckets[0].BaselineKeys != 2 {
		t.Fatal(usage, err)
	}
	j, err = v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended")
	if err != nil || j.Revision != 2 || !j.VersionsRequired {
		t.Fatal(j, err)
	}
	j, err = v.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "")
	if err != nil || !j.VersionsRequired {
		t.Fatal("forgot retained versions", j, err)
	}
	// Legacy grants are never made safe just because time passes.
	other, _ := seedAccounting(t, st)
	if err = st.AdmitObjectURL(ctx, other.AccountID, other.ID, "legacy", 1, true, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	advance(other.ID)
	if _, err = v.RequestObjectBucketVersioning(ctx, other.AccountID, other.AppID, other.ID, "Enabled"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unsafe legacy grant accepted", err)
	}
	j, err = v.GetObjectBucketVersioning(ctx, other.AccountID, other.AppID, other.ID)
	if err != nil || j.State != "ready" || j.DesiredStatus != "" {
		t.Fatal("rejection persisted intent", j, err)
	}
}

func TestObjectBucketVersioningDatabaseFencesPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	j, err := st.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
	if err != nil {
		t.Fatal(j, err)
	}
	for _, sql := range []string{
		`UPDATE object_buckets SET state='deleting' WHERE id=$1`,
		`INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) VALUES($1,repeat('a',64),1)`,
		`UPDATE object_storage_bucket_usage SET observed_bytes=1 WHERE bucket_id=$1`,
		`UPDATE object_bucket_versioning SET state='ready',observed_status='Enabled' WHERE bucket_id=$1`,
		`UPDATE object_bucket_versioning SET desired_status='Suspended',revision=revision+1 WHERE bucket_id=$1`,
	} {
		_, err = pool.Exec(ctx, sql, b.ID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatal("SQL bypassed transition fence", sql, err)
		}
	}
	j, err = st.ClaimObjectBucketVersioning(ctx, b.ID, "old")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = st.DispatchObjectBucketVersioning(ctx, b.ID, j.Token)
	if err != nil {
		t.Fatal(j, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_versioning SET lease_until=now()-interval '1 second',retry_at=now(),versions_required=false WHERE bucket_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	j, err = restarted.ClaimObjectBucketVersioning(ctx, b.ID, "new")
	if err != nil || !j.VersionsRequired || !j.Dispatched {
		t.Fatal(j, err)
	}
	if _, err = st.AdvanceObjectBucketVersioning(ctx, b.ID, "old", "Enabled"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker cut over", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_versioning SET state='ready',observed_status='Enabled',lease_token='',lease_until=NULL WHERE bucket_id=$1`, b.ID); err == nil {
		t.Fatal("unverified ready state accepted")
	}
}
