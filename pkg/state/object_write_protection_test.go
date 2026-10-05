package state_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 590
func TestObjectWriteProtectionMem(t *testing.T) {
	m := state.NewMemStore()
	// Propagation advances twice; keep the staged inventory in the past.
	now := time.Now().UTC().Add(-17 * time.Minute)
	m.SetClockForTest(func() time.Time { return now })
	writeProtectionSuite(t, m, nil, func(bucket string) {
		if bucket == "" {
			now = time.Now().UTC()
		} else {
			now = now.Add(16 * time.Minute)
		}
	}, func() time.Time { return now })
}
func TestObjectWriteProtectionPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	writeProtectionSuite(t, st, pool, func(bucket string) {
		if bucket == "" {
			return
		}
		_, err := pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END WHERE bucket_id=$1`, bucket)
		if err != nil {
			t.Fatal(err)
		}
	}, func() time.Time { return time.Now().UTC() })
}
func writeProtectionSuite(t *testing.T, st accountingStore, pool *pgxpool.Pool, advance func(string), clock func() time.Time) {
	b, report := seedAccounting(t, st)
	lock := st.(state.ObjectBucketObjectLockStore)
	days := int32(3)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
	if _, err := lock.RequestObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, cfg); err != nil {
		t.Fatal(err)
	}
	finishLockVersioning(t, st, b, advance)
	j, err := lock.ClaimObjectBucketObjectLock(t.Context(), b.ID, "lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.FinishObjectBucketObjectLock(t.Context(), b.ID, j.Token, cfg); err != nil {
		t.Fatal(err)
	}
	if err = st.(state.ObjectCapacityStore).BeginObjectWrite(t.Context(), b.AccountID, b.ID, uuid.NewString(), "legacy", 1, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy writer admitted", err)
	}
	// Restore real admission time after advancing only the versioning fixture.
	advance("")
	report.ObservedAt = clock()
	if err = st.RecordObjectUsageReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	c, err := writes.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "protected", Bytes: 3, Status: "pending", Protection: state.ObjectWriteProtectionSnapshot{Requested: api.ObjectWriteProtection{LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}}}, accountingPolicy())
	if err != nil || !c.Protection.Enabled || c.Protection.Revision != j.Revision || c.Protection.MinimumRetention().Mode != "COMPLIANCE" {
		t.Fatal(c, err)
	}
	original := c.Protection.Clone()
	*c.Protection.DefaultRetention.Days = 999
	c, err = writes.GetObjectUploadReceipt(t.Context(), b.AccountID, b.AppID, "", "subject", c.ID)
	if err != nil || !c.Protection.Equal(original) {
		t.Fatal("snapshot aliases caller", c, err)
	}
	// A multipart initiation captures the same original default independently.
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(t.Context(), state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart", ExpiresAt: time.Now().Add(24 * time.Hour)}, 100)
	if err != nil || !u.Protection.Enabled {
		t.Fatal(u, err)
	}
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_upload_completions SET write_phase='dispatched' WHERE id=$1`, c.ID); err == nil {
			t.Fatal("unaware dispatch accepted")
		}
		if _, err = pool.Exec(t.Context(), `UPDATE object_upload_completions SET protection_snapshot='{}' WHERE id=$1`, c.ID); err == nil {
			t.Fatal("snapshot erased")
		}
		if _, err = pool.Exec(t.Context(), `UPDATE object_storage_multipart_uploads SET state='initiating',lease_token='old',lease_until=clock_timestamp()+interval '1 minute' WHERE id=$1`, u.ID); err == nil {
			t.Fatal("unaware multipart claim accepted")
		}
		st = state.NewPgStore(pool)
		writes = st.(state.ObjectTrackedGatewayUploadStore)
		sessions = st.(state.ObjectMultipartUploadStore)
	}
	// Queue a default replacement: existing writes retain their accepted policy.
	newDays := int32(1)
	changed := cfg.Clone()
	changed.DefaultRetention.Days = &newDays
	if _, err = lock.RequestObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, changed); err != nil {
		t.Fatal(err)
	}
	applying, err := lock.ClaimObjectBucketObjectLock(t.Context(), b.ID, "replacement")
	if err != nil || applying.Token != "" || applying.LastErrorCode != "unsettled_writes" {
		t.Fatal("defaults crossed active write", applying, err)
	}
	c, err = writes.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag, c.ProviderVersionID = "completed", `"actual"`, "native-locked"
	if _, err = writes.FinishTrackedObjectUpload(t.Context(), c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("ack alone settled", err)
	}
	c.VerifiedProtection = strings.Repeat("f", 64)
	if _, err = writes.FinishTrackedObjectUpload(t.Context(), c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wrong policy settled", err)
	}
	c.VerifiedProtection = original.Proof()
	done, err := writes.FinishTrackedObjectUpload(t.Context(), c)
	if err != nil || done.Status != "completed" || !done.Protection.Equal(original) {
		t.Fatal(done, err)
	}
	c.Protection.Requested.LegalHold.Status = "OFF"
	if _, err = writes.FinishTrackedObjectUpload(t.Context(), c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminal snapshot replaced", err)
	}
	// Initiation and completion remain authorized under the previous defaults.
	u, err = sessions.ClaimObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(t.Context(), u.ID, u.LeaseToken, "native-upload"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	transfers := st.(state.ObjectMultipartTransferStore)
	if err = transfers.BeginObjectMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, "part", 1, 3, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err = transfers.SettleObjectMultipartPart(t.Context(), b.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err = transfers.PrepareObjectMultipartCompletion(t.Context(), u, "complete", 3, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	results := st.(state.ObjectMultipartCompletionStore)
	if err = results.DispatchObjectMultipartCompletion(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	proof := state.ObjectMultipartCompletionResult{ETag: `"multipart"`, ProviderVersionID: "native-multipart"}
	if _, err = results.FinishObjectMultipartCompletion(t.Context(), u, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatal("multipart ack alone settled", err)
	}
	proof.VerifiedProtection = u.Protection.Proof()
	if _, err = results.FinishObjectMultipartCompletion(t.Context(), u, proof); err != nil {
		t.Fatal(err)
	}
	if pool != nil {
		_, down := writeProtectionMigration(t)
		if _, err = pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "Preserve write protection history") {
			t.Fatal("rollback discarded protected history", err)
		}
	}
}

func writeProtectionMigration(t *testing.T) (string, string) {
	t.Helper()
	data, err := fs.ReadFile(migrations.FS, "20261005101500000_object_write_protection.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing rollback")
	}
	return parts[0], parts[1]
}

// adr: 590
func TestObjectWriteProtectionMigrationRoundTrip(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	up, down := writeProtectionMigration(t)
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	part := `{"method":"PUT","key":"part","expires_in":60,"size_bytes":3,"content_type":"application/octet-stream","multipart":{"upload_id":"00000000-0000-4000-8000-000000000001","part_number":1}}`
	for _, step := range []string{"down", "up"} {
		if step == "up" {
			if _, err := pool.Exec(ctx, up); err != nil {
				t.Fatal(err)
			}
		}
		var valid bool
		if err := pool.QueryRow(ctx, `SELECT valid_object_url_request($1::jsonb)`, part).Scan(&valid); err != nil || !valid {
			t.Fatal("multipart URL lost during migration", step, valid, err)
		}
	}
}
