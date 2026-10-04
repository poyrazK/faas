package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

type encryptedRouteStore interface {
	accountingStore
	state.ObjectUploadRouteStore
	state.ObjectTrackedUploadStore
}

// adr: 417
func TestObjectUploadRouteEncryptionMem(t *testing.T) {
	routeEncryptionSuite(t, state.NewMemStore(), nil)
}
func TestObjectUploadRouteEncryptionPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	routeEncryptionSuite(t, st, pool)
}

func routeEncryptionSuite(t *testing.T, st encryptedRouteStore, pool *pgxpool.Pool) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	snapshot := journalEncryption(b.AccountID)
	route, err := st.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "encrypted", MaxBytes: 10, Enabled: true, Encryption: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	*route.Encryption.Selection.BucketKeyEnabled = true
	route, err = st.GetObjectUploadRoute(ctx, b.AccountID, b.AppID, "encrypted")
	if err != nil || !route.Encryption.Equal(snapshot) {
		t.Fatal("route snapshot aliases a caller", route, err)
	}
	raw, err := json.Marshal(route)
	if err != nil || strings.Contains(string(raw), "provider-private") || strings.Contains(string(raw), "bucket_key_enabled") {
		t.Fatal("private route snapshot escaped", string(raw), err)
	}
	attempt := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "route-object", Bytes: 10, Status: "pending", IdempotencyKey: "once", RequestFingerprint: "fingerprint"}
	if _, _, err = st.BeginTrackedObjectUpload(ctx, attempt, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("older route writer dropped encryption", err)
	}
	if _, err = st.CreateObjectUploadIntent(ctx, attempt); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy route admitted encryption policy", err)
	}
	if pool != nil {
		assertRouteEncryptionDatabaseFences(t, pool, attempt)
	}
	attempt.Encryption = snapshot.Clone()
	var wins atomic.Int32
	var wg sync.WaitGroup
	results := make(chan state.ObjectUploadCompletion, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := attempt
			copy.ID = uuid.NewString()
			c, created, e := st.BeginTrackedObjectUpload(ctx, copy, accountingPolicy())
			if e != nil {
				t.Error(e)
				return
			}
			if created {
				wins.Add(1)
				results <- c
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("idempotent route dispatched duplicate reservations", wins.Load())
	}
	c := <-results
	changed := route
	changed.Encryption = journalEncryption(b.AccountID)
	if _, err = st.UpsertObjectUploadRoute(ctx, changed); err != nil {
		t.Fatal(err)
	}
	stale := attempt
	stale.ID = uuid.NewString()
	stale.IdempotencyKey = "new-attempt"
	stale.Key = "stale-policy"
	if _, _, err = st.BeginTrackedObjectUpload(ctx, stale, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale route enrollment admitted", err)
	}
	c, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag = "completed", `"route"`
	if _, err = st.FinishTrackedObjectUpload(ctx, c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unverified route encryption settled", err)
	}
	c.VerifiedEncryption = snapshot.Clone().Selection
	done, err := st.FinishTrackedObjectUpload(ctx, c)
	if err != nil || !done.Encryption.Equal(snapshot) {
		t.Fatal(done, err)
	}
	replay, err := st.GetObjectUploadIntent(ctx, route.ID, "subject", "once")
	if err != nil || replay.ID != c.ID || replay.Status != "completed" || !replay.Encryption.Equal(snapshot) {
		t.Fatal("idempotent lookup discarded encryption", replay, err)
	}
	view := state.ViewObjectWriteReceipt(replay)
	if view.Encryption == nil || view.Encryption.KeyID != snapshot.Selection.KeyID {
		t.Fatal("owned receipt selection missing", view)
	}
	*view.Encryption.BucketKeyEnabled = true
	replay, err = st.GetObjectUploadIntent(ctx, route.ID, "subject", "once")
	if err != nil || !replay.Encryption.Equal(snapshot) {
		t.Fatal("public receipt projection aliases proof", err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, c.CreatedAt)
	if err != nil || usage.Buckets[0].GrantedBytes != 10 {
		t.Fatal("route replay or stale policy spent capacity", usage, err)
	}
	if _, err = st.GetObjectUploadRoute(ctx, uuid.NewString(), b.AppID, "encrypted"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign policy disclosed", err)
	}
}

func assertRouteEncryptionDatabaseFences(t *testing.T, pool *pgxpool.Pool, c state.ObjectUploadCompletion) {
	t.Helper()
	for _, phase := range []string{"prepared", "untracked"} {
		_, err := pool.Exec(t.Context(), `INSERT INTO object_upload_completions(id,route_id,account_id,app_id,bucket_id,subject_id,object_key,bytes,status,write_phase) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9)`, uuid.NewString(), c.RouteID, c.AccountID, c.AppID, c.BucketID, c.SubjectID, c.Key, c.Bytes, phase)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.ConstraintName != "object_upload_route_encryption_fenced" {
			t.Fatal("older SQL writer bypassed route policy", err)
		}
	}
	body, err := migrations.FS.ReadFile("20261004001511402_object_upload_route_encryption.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(body), "-- +goose Down")
	if !ok {
		t.Fatal("rollback missing")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	_, err = tx.Exec(t.Context(), down)
	if err == nil || !strings.Contains(err.Error(), "Clear encrypted route policies") {
		t.Fatal("rollback discarded route authority", err)
	}
}
