package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 560
func TestObjectFixedMultipartAdmissionMem(t *testing.T) {
	for _, versions := range []bool{false, true} {
		t.Run(fixedMultipartProfile(versions), func(t *testing.T) {
			fixedMultipartAdmissionSuite(t, state.NewMemStore(), versions)
		})
	}
}

func TestObjectFixedMultipartAdmissionPG(t *testing.T) {
	for _, versions := range []bool{false, true} {
		t.Run(fixedMultipartProfile(versions), func(t *testing.T) {
			st, _, _ := pgStoreWithPool(t)
			fixedMultipartAdmissionSuite(t, st, versions)
		})
	}
}

func fixedMultipartProfile(versions bool) string {
	if versions {
		return "all_versions"
	}
	return "current"
}

func fixedMultipartVersionBaseline(t *testing.T, st accountingStore, b state.ObjectBucket) {
	t.Helper()
	ctx := t.Context()
	// The provider once had a version, which is now absent from a verified scan.
	if _, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(ctx, b.AccountID, b.ID, []state.ObjectVersionIdentity{{ID: uuid.NewString(), Key: "historical", ProviderVersionID: "deleted-version"}}); err != nil {
		t.Fatal(err)
	}
	cap := st.(state.ObjectCapacityStore)
	j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "baseline")
	if err != nil || j.State != "scanning" || j.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal(j, err)
	}
	if _, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", nil); err != nil {
		t.Fatal(err)
	}
}

func fixedMultipartCandidate(b state.ObjectBucket, key string) state.ObjectMultipartUpload {
	return state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: key, SizeBytes: 5, PartSizeBytes: 5, PartCount: 1, ExpiresAt: time.Now().Add(time.Hour)}
}

func fixedMultipartUsage(t *testing.T, st accountingStore, b state.ObjectBucket, p api.ObjectStoragePolicy, bytes, keys, authorizations int64) {
	t.Helper()
	snapshot, err := st.ObjectUsage(t.Context(), b.AccountID, time.Now())
	got := state.SummarizeObjectUsage(snapshot, p, time.Now())
	if err != nil || got.CapacityBytes != bytes || got.CapacityKeys != keys || got.Authorizations != authorizations {
		t.Fatal("fixed multipart accounting", got, err)
	}
}

func fixedMultipartAdmissionSuite(t *testing.T, st accountingStore, versions bool) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	if versions {
		fixedMultipartVersionBaseline(t, st, b)
	}
	admitted := st.(state.ObjectFixedMultipartAdmissionStore)
	sessions := st.(state.ObjectMultipartUploadStore)
	for _, tc := range []struct {
		name string
		edit func(*state.ObjectMultipartUpload)
	}{
		{"layout", func(u *state.ObjectMultipartUpload) { u.PartCount = 2 }},
		{"unknown_size", func(u *state.ObjectMultipartUpload) { u.SizeBytes, u.PartSizeBytes, u.PartCount = 0, 0, 0 }},
		{"foreign_owner", func(u *state.ObjectMultipartUpload) { u.AccountID = uuid.NewString() }},
		{"forged_flag", func(u *state.ObjectMultipartUpload) { u.FixedAdmission = true }},
		{"quota", func(u *state.ObjectMultipartUpload) { u.SizeBytes, u.PartSizeBytes = 101, 101 }},
	} {
		u := fixedMultipartCandidate(b, tc.name)
		tc.edit(&u)
		if _, err := admitted.ReserveAdmittedObjectMultipartUpload(ctx, u, 100, p); err == nil {
			t.Fatal("invalid admission accepted", tc.name)
		}
		if _, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("failed admission published session", tc.name, err)
		}
		fixedMultipartUsage(t, st, b, p, 0, 0, 0)
	}

	// Every replica may retry creation, but they publish one session and budget.
	results := make(chan state.ObjectMultipartUpload, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			u, err := admitted.ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, "same-key"), 1, p)
			if err != nil {
				t.Error(err)
				return
			}
			results <- u
		})
	}
	wg.Wait()
	close(results)
	var u state.ObjectMultipartUpload
	for got := range results {
		if !got.FixedAdmission || u.ID != "" && u.ID != got.ID {
			t.Fatal("replay published another admission", got, u)
		}
		u = got
	}
	if u.ID == "" {
		t.Fatal("no session accepted")
	}
	fixedMultipartUsage(t, st, b, p, 5, 1, 1)
	raw, err := json.Marshal(u)
	if err != nil || strings.Contains(string(raw), "FixedAdmission") || strings.Contains(string(raw), "fixed_admission") {
		t.Fatal("private admission exposed", string(raw), err)
	}
	for _, candidate := range []state.ObjectMultipartUpload{fixedMultipartCandidate(b, "limit"), func() state.ObjectMultipartUpload {
		c := fixedMultipartCandidate(b, "duplicate-id")
		c.ID = u.ID
		return c
	}()} {
		limit := 1
		if candidate.ID == u.ID {
			limit = 100
		}
		if _, err = admitted.ReserveAdmittedObjectMultipartUpload(ctx, candidate, limit, p); !errors.Is(err, state.ErrConflict) {
			t.Fatal("session or identity limit bypass", err)
		}
		fixedMultipartUsage(t, st, b, p, 5, 1, 1)
	}
	changed := fixedMultipartCandidate(b, u.Key)
	changed.ContentType = "text/plain"
	if _, err = admitted.ReserveAdmittedObjectMultipartUpload(ctx, changed, 1, p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	tight := p
	tight.MaxAccountBytes, tight.MaxMonthlyAuthorizations = 1, 1
	if replay, e := admitted.ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, u.Key), 1, tight); e != nil || replay.ID != u.ID {
		t.Fatal("accepted replay spent another budget", replay, e)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native-upload"); err != nil {
		t.Fatal(err)
	}
	// Fixed broker part transfers consume request budget, not a second object grant.
	u, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	transfers := st.(state.ObjectMultipartTransferStore)
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "part"}}
	if _, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 4, parts, p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("declared size changed", err)
	}
	u, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 5, parts, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.SetObjectMultipartUploadSize(ctx, u.ID, "complete", 4); !errors.Is(err, state.ErrConflict) {
		t.Fatal("completion resized admission", err)
	}
	fixedMultipartUsage(t, st, b, p, 5, 1, 1)
	if err = sessions.FinishObjectMultipartUpload(ctx, u.ID, "complete", state.ObjectMultipartCompleted); err != nil {
		t.Fatal(err)
	}
	fixedMultipartUsage(t, st, b, p, 5, 1, 1)
	// A second retained version needs fresh capacity even at the same logical key.
	if versions {
		overwrite := fixedMultipartCandidate(b, u.Key)
		full := p
		full.MaxAccountBytes, full.MaxBucketBytes = 5, 5
		if _, err = admitted.ReserveAdmittedObjectMultipartUpload(ctx, overwrite, 1, full); !errors.Is(err, state.ErrObjectCapacity) {
			t.Fatal("retained overwrite escaped a full bucket", err)
		}
		if _, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, overwrite.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("failed overwrite published a session", err)
		}
		fixedMultipartUsage(t, st, b, p, 5, 1, 1)
	}
	if _, err = admitted.ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, u.Key), 1, p); err != nil {
		t.Fatal(err)
	}
	wantBytes, wantKeys := int64(5), int64(1)
	if versions {
		wantBytes, wantKeys = 10, 2
	}
	fixedMultipartUsage(t, st, b, p, wantBytes, wantKeys, 2)
}

// adr: 560
func TestObjectFixedMultipartAdmissionOldWriterPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	u, err := st.ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, "fixed"), 100, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE object_storage_multipart_uploads SET fixed_admission=false WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET size_bytes=6 WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET object_key='changed' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET part_size_bytes=6 WHERE id=$1`,
		`DELETE FROM object_storage_multipart_uploads WHERE id=$1`,
		`DELETE FROM object_storage_write_admissions WHERE id=$1`,
		`UPDATE object_storage_write_admissions SET state='settled',settled_at=now() WHERE id=$1`,
		`UPDATE object_storage_write_admissions SET multipart_upload_id=NULL WHERE id=$1`,
	} {
		if _, err = pool.Exec(ctx, statement, u.ID); err == nil {
			t.Fatal("older writer changed live capacity", statement)
		}
	}
	// A store reopened on the durable rows still owns the original reservation.
	reopened := state.NewPgStore(pool)
	replay, err := reopened.ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, u.Key), 100, accountingPolicy())
	if err != nil || replay.ID != u.ID || !replay.FixedAdmission {
		t.Fatal("restart lost admission", replay, err)
	}
	fixedMultipartUsage(t, reopened, b, accountingPolicy(), 5, 1, 1)
	raw, err := migrations.FS.ReadFile("20261004090600520_object_fixed_multipart_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback discarded live admission")
	}
	// Exercise the deferred binding guard, including a missing admission at commit.
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_multipart_uploads(id,account_id,app_id,bucket_id,object_key,size_bytes,part_size_bytes,part_count,fixed_admission,expires_at)
 SELECT $1,account_id,app_id,bucket_id,'unbound',size_bytes,part_size_bytes,part_count,true,expires_at FROM object_storage_multipart_uploads WHERE id=$2`, uuid.NewString(), u.ID); err == nil {
		t.Fatal("unbound fixed session committed")
	}
}

// adr: 560
func TestObjectFixedMultipartLegacyReplayMem(t *testing.T) {
	fixedMultipartLegacyReplay(t, state.NewMemStore())
}
func TestObjectFixedMultipartLegacyReplayPG(t *testing.T) {
	st, _, _ := pgStoreWithPool(t)
	fixedMultipartLegacyReplay(t, st)
}
func fixedMultipartLegacyReplay(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	u := fixedMultipartCandidate(b, "legacy")
	old, err := st.(state.ObjectMultipartUploadStore).ReserveObjectMultipartUpload(t.Context(), u, 1)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := st.(state.ObjectFixedMultipartAdmissionStore).ReserveAdmittedObjectMultipartUpload(t.Context(), fixedMultipartCandidate(b, u.Key), 1, accountingPolicy())
	if err != nil || replay.ID != old.ID || replay.FixedAdmission {
		t.Fatal("legacy replay changed quota contract", replay, err)
	}
	fixedMultipartUsage(t, st, b, accountingPolicy(), 0, 0, 0)
}

// adr: 560
func TestObjectFixedMultipartAbortReconciliationMem(t *testing.T) {
	fixedMultipartAbortReconciliation(t, state.NewMemStore())
}
func TestObjectFixedMultipartAbortReconciliationPG(t *testing.T) {
	st, _, _ := pgStoreWithPool(t)
	fixedMultipartAbortReconciliation(t, st)
}
func fixedMultipartAbortReconciliation(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	fixedMultipartVersionBaseline(t, st, b)
	p := accountingPolicy()
	u, err := st.(state.ObjectFixedMultipartAdmissionStore).ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, "abort"), 100, p)
	if err != nil {
		t.Fatal(err)
	}
	cap := st.(state.ObjectCapacityStore)
	if _, err = cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("inventory escaped a live fixed admission", err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native"); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.FinishObjectMultipartUpload(ctx, u.ID, "abort", state.ObjectMultipartAborted); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unverified abort released native admission", err)
	}
	if err = st.(state.ObjectMultipartTransferStore).FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); err != nil {
		t.Fatal(err)
	}
	fixedMultipartUsage(t, st, b, p, 5, 1, 1)
	j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "after-abort")
	if err != nil || j.State != "scanning" {
		t.Fatal("verified abort did not permit inventory", j, err)
	}
	if _, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", nil); err != nil {
		t.Fatal(err)
	}
	fixedMultipartUsage(t, st, b, p, 0, 0, 1)
}
