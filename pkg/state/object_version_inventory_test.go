package state_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func versionRecord(id string, size int64) state.ObjectVersionInventoryRecord {
	h := sha256.Sum256([]byte(id))
	return state.ObjectVersionInventoryRecord{Identity: hex.EncodeToString(h[:]), Bytes: size}
}

func versionCandidate(b state.ObjectBucket, size int64) state.ObjectUploadCompletion {
	return state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "credential", Key: "same-key", Bytes: size, Status: "pending"}
}

func settleNativeVersion(t *testing.T, st state.ObjectTrackedGatewayUploadStore, c state.ObjectUploadCompletion) {
	t.Helper()
	ctx := t.Context()
	var err error
	c, err = st.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = "completed"
	c.ETag = `"etag"`
	c.RecoveryVersionsObserved = true
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
}

// adr: 540
func TestObjectVersionInventoryMem(t *testing.T) {
	objectVersionInventorySuite(t, state.NewMemStore(), nil)
}
func TestObjectVersionInventoryPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	objectVersionInventorySuite(t, st, func() {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_version_inventory_entries`).Scan(&count); err != nil || count != 0 {
			t.Fatal("staging rows leaked", count, err)
		}
	})
}

func objectVersionInventorySuite(t *testing.T, st accountingStore, checkCleanup func()) {
	ctx := t.Context()
	p := accountingPolicy()
	b, _ := seedAccounting(t, st)
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	capacity := st.(state.ObjectCapacityStore)
	versions := st.(state.ObjectVersionInventoryStore)
	c, err := writes.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 10), p)
	if err != nil {
		t.Fatal(err)
	}
	settleNativeVersion(t, writes, c)
	if _, err = writes.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 1), p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("write escaped before native baseline", err)
	}
	j, err := capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = capacity.ClaimObjectCapacityReconciliation(ctx, j.ID, "first")
	if err != nil || j.InventoryScope != state.ObjectInventoryAllVersions || j.State != "scanning" {
		t.Fatal(j, err)
	}
	if _, err = capacity.FinishObjectCapacityReconciliation(ctx, j.ID, j.Token, 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("current inventory refunded retained versions", err)
	}
	if _, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, "stale", "private-next", []state.ObjectVersionInventoryRecord{versionRecord("a", 10)}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale page", err)
	}
	j, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "private-next", []state.ObjectVersionInventoryRecord{versionRecord("a", 10)})
	if err != nil || j.State != "waiting" || j.ScannedPages != 1 || j.ScannedBytes != 10 {
		t.Fatal(j, err)
	}
	if j.InventoryEntries != nil || j.InventoryCursors != nil {
		t.Fatal("worker received unbounded staging indexes")
	}
	snap, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if snap.Buckets[0].BaselineBytes != 0 || snap.Buckets[0].GrantedBytes != 10 {
		t.Fatal("partial scan changed quota", snap)
	}
	for _, e := range []error{st.AdmitObjectURL(ctx, b.AccountID, b.ID, "same-key", 1, true, p), capacity.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "same-key", 1, p)} {
		if !errors.Is(e, state.ErrConflict) {
			t.Fatal("write escaped partial scan", e)
		}
	}
	j, err = capacity.ClaimObjectCapacityReconciliation(ctx, j.ID, "second")
	if err != nil || j.InventoryCursor != "private-next" {
		t.Fatal(j, err)
	}
	for _, tc := range []struct {
		next  string
		items []state.ObjectVersionInventoryRecord
	}{
		{"next", []state.ObjectVersionInventoryRecord{versionRecord("a", 10)}},
		{"private-next", []state.ObjectVersionInventoryRecord{versionRecord("b", 11)}},
		{"", []state.ObjectVersionInventoryRecord{{Identity: "bad", Bytes: 1}}},
		{"", []state.ObjectVersionInventoryRecord{versionRecord("b", -1)}},
		{strings.Repeat("é", 4097), []state.ObjectVersionInventoryRecord{versionRecord("b", 11)}},
	} {
		if _, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, tc.next, tc.items); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid page accepted", err)
		}
	}
	j, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", []state.ObjectVersionInventoryRecord{versionRecord("b", 11), versionRecord("marker", 9)})
	if err != nil || j.State != "completed" || j.AfterBytes != 30 || j.AfterKeys != 3 || j.ScannedPages != 2 {
		t.Fatal(j, err)
	}
	if checkCleanup != nil {
		checkCleanup()
	}
	raw, _ := json.Marshal(j)
	if strings.Contains(string(raw), "private-next") || strings.Contains(string(raw), "Identity") {
		t.Fatal("native identities exposed", string(raw))
	}
	status, err := versions.ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
	if err != nil || status.Scope != state.ObjectInventoryAllVersions {
		t.Fatal(status, err)
	}
	if _, err = versions.ObjectVersionAccountingStatus(ctx, uuid.NewString(), b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("tenant isolation", err)
	}
	for _, e := range []error{st.AdmitObjectURL(ctx, b.AccountID, b.ID, "same-key", 1, true, p), capacity.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "same-key", 1, p)} {
		if !errors.Is(e, state.ErrConflict) {
			t.Fatal("untracked native write", e)
		}
	}

	routes := st.(state.ObjectUploadRouteStore)
	route, err := routes.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "native-route", MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	application := versionCandidate(b, 20)
	application.RouteID = route.ID
	application.IdempotencyKey = "once"
	application.RequestFingerprint = "fingerprint"
	application, created, err := writes.BeginTrackedObjectUpload(ctx, application, p)
	if err != nil || !created {
		t.Fatal(application, created, err)
	}
	settleNativeVersion(t, writes, application)
	replay := application
	replay.Status = "pending"
	replay.ID = uuid.NewString()
	exhausted := p
	exhausted.MaxAccountBytes = 1
	if _, created, err = writes.BeginTrackedObjectUpload(ctx, replay, exhausted); err != nil || created {
		t.Fatal("native route replay readmitted quota", created, err)
	}

	copies := st.(state.ObjectTrackedGatewayCopyStore)
	emptyCopy := versionCandidate(b, 0)
	emptyCopy.SourceKey = "source"
	emptyCopy.SourceETag = `"source"`
	emptyCopy, err = copies.BeginTrackedGatewayCopy(ctx, emptyCopy, p)
	if err != nil {
		t.Fatal(err)
	}
	settleNativeVersion(t, writes, emptyCopy)
	for range 2 {
		c, err = writes.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 25), p)
		if err != nil {
			t.Fatal(err)
		}
		settleNativeVersion(t, writes, c)
	}
	before, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if _, err = writes.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 25), p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal("overwrites reused capacity", err)
	}
	after, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
	got := state.SummarizeObjectUsage(after, p, time.Now())
	if got.CapacityBytes != 100 || got.CapacityKeys != 7 || before.Authorizations != after.Authorizations {
		t.Fatal("failed admission spent capacity or authorization", got, after.Authorizations)
	}

	emptyCopy.ID = uuid.NewString()
	emptyCopy.Status = "pending"
	emptyCopy.Origin = "gateway_copy"
	keyLimited := p
	keyLimited.MaxAccountKeys = 7
	if _, err = copies.BeginTrackedGatewayCopy(ctx, emptyCopy, keyLimited); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal("zero-byte copy bypassed retained entry quota", err)
	}
	token := uuid.NewString()
	if err = st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, token, 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old observation erased versions", err)
	}
	j, err = capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = capacity.ClaimObjectCapacityReconciliation(ctx, j.ID, "cancel-me")
	if err != nil {
		t.Fatal(err)
	}
	j, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "next", []state.ObjectVersionInventoryRecord{versionRecord("x", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = capacity.CancelObjectCapacityReconciliation(ctx, b.AccountID, b.ID, j.ID); err != nil {
		t.Fatal(err)
	}
	if checkCleanup != nil {
		checkCleanup()
	}
	if _, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, "cancel-me", "", nil); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cancelled scan completed", err)
	}
	after, _ = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if state.SummarizeObjectUsage(after, p, time.Now()).CapacityBytes != 100 {
		t.Fatal("cancellation refunded versions")
	}
}

func TestObjectVersionInventoryRestartAndDatabaseFencesPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	c, err := st.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 10), p)
	if err != nil {
		t.Fatal(err)
	}
	settleNativeVersion(t, st, c)
	j, err := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "one")
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "private", []state.ObjectVersionInventoryRecord{versionRecord("one", 10)})
	if err != nil {
		t.Fatal(err)
	}
	st = state.NewPgStore(pool)
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "two")
	if err != nil || j.ScannedBytes != 10 || j.InventoryCursor != "private" {
		t.Fatal("restart lost page", j, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET baseline_bytes=0,granted_bytes=0 WHERE bucket_id=$1`, b.ID); err == nil {
		t.Fatal("legacy rebase bypassed fence")
	}
	j, err = st.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", []state.ObjectVersionInventoryRecord{versionRecord("two", 20)})
	if err != nil || j.AfterBytes != 30 {
		t.Fatal(j, err)
	}
	for _, q := range []string{
		`UPDATE object_storage_bucket_usage SET inventory_scope='current' WHERE bucket_id=$1`,
		`UPDATE object_storage_bucket_usage SET observed_bytes=0,observed_keys=0,observed_at=now() WHERE bucket_id=$1`,
		`INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) VALUES($1,repeat('a',64),1)`,
		`INSERT INTO object_storage_write_admissions(id,bucket_id,key_hash,kind) VALUES(gen_random_uuid(),$1,repeat('a',64),'proxy')`,
	} {
		if _, err = pool.Exec(ctx, q, b.ID); err == nil {
			t.Fatal("legacy database write bypassed native admission", q)
		}
	}
	j, err = st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_write_admissions(id,bucket_id,key_hash,kind,native_version,native_bytes) VALUES(gen_random_uuid(),$1,repeat('a',64),'proxy',true,1)`, b.ID); err == nil {
		t.Fatal("native journal bypassed active scan")
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "partial")
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "partial", []state.ObjectVersionInventoryRecord{versionRecord("partial", 2)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET deadline_at=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "expire")
	if err != nil || j.State != "failed" {
		t.Fatal(j, err)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_version_inventory_entries WHERE job_id=$1`, j.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("deadline staging leak", n, err)
	}
}

func TestObjectVersionMultipartCapacityMem(t *testing.T) {
	objectVersionMultipartSuite(t, state.NewMemStore())
}
func TestObjectVersionMultipartCapacityPG(t *testing.T) {
	st, _, _ := pgStoreWithPool(t)
	objectVersionMultipartSuite(t, st)
}

func objectVersionMultipartSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	p := accountingPolicy()
	b, _ := seedAccounting(t, st)
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	c, err := writes.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 10), p)
	if err != nil {
		t.Fatal(err)
	}
	settleNativeVersion(t, writes, c)
	cap := st.(state.ObjectCapacityStore)
	versions := st.(state.ObjectVersionInventoryStore)
	j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = versions.StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", []state.ObjectVersionInventoryRecord{versionRecord("existing", 10)}); err != nil {
		t.Fatal(err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	capacity := st.(state.ObjectMultipartCapacityStore)
	for pass := range 2 {
		u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "same-key", ExpiresAt: time.Now().Add(time.Hour)}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
			t.Fatal(err)
		}
		if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native-upload"); err != nil {
			t.Fatal(err)
		}
		if err = transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "part", 1, 20, 100, p); err != nil {
			t.Fatal(err)
		}
		if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "part"); err != nil {
			t.Fatal(err)
		}
		if err = capacity.AdmitObjectMultipartCompletion(ctx, b.AccountID, b.ID, u.ID, u.Key, 20, p); !errors.Is(err, state.ErrConflict) {
			t.Fatal("untracked native completion admitted", err)
		}
		u, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		u, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 20, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "etag"}}, p)
		if err != nil {
			t.Fatal(err)
		}
		if err = sessions.FinishObjectMultipartUpload(ctx, u.ID, "complete", state.ObjectMultipartCompleted); err != nil {
			t.Fatal(err)
		}
		snap, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		got := state.SummarizeObjectUsage(snap, p, time.Now())
		if err != nil || got.CapacityBytes != 10+int64(pass+1)*20 || got.CapacityKeys != int64(pass+2) {
			t.Fatal("multipart overwrite reused native quota", got, err)
		}
	}
}
