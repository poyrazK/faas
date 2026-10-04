package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 572
func TestVersionProtectionControlE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	var offset atomic.Int64
	now := time.Now().UTC()
	e.store.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
	versionProtectionControlE2E(t, e.s, e.store, e.acct, e.key, nil, func() { offset.Add(int64(20 * time.Minute)) })
}
func TestVersionProtectionControlE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	versionProtectionControlE2E(t, e.s, e.store, e.acct, e.key, e.pool, func() {
		if _, err := e.pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END;UPDATE object_bucket_object_lock SET retry_at=clock_timestamp();UPDATE object_version_protection SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_until IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END`); err != nil {
			t.Fatal(err)
		}
	})
}
func versionProtectionControlE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool, advance func()) {
	var mu sync.Mutex
	hold := "OFF"
	lost := true
	var puts, calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.URL.Query().Has("object-lock"):
			_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
		case r.URL.Query().Has("versioning"):
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
		case r.URL.Query().Has("versions"):
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
		case r.URL.Query().Has("uploads"):
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		case r.URL.Query().Has("legal-hold"):
			if r.URL.Query().Get("versionId") != "private-exact" || r.URL.Path != "/physical/key" {
				t.Error("wrong private target", r.URL)
			}
			w.Header().Set("X-Amz-Version-Id", "private-exact")
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `<LegalHold><Status>`+hold+`</Status></LegalHold>`)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			h, err := objectstorage.DecodeObjectVersionLegalHold(body)
			if err != nil {
				t.Error(err)
			}
			hold = h.Status
			puts.Add(1)
			if lost {
				lost = false
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			}
		default:
			t.Error("unexpected provider request", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	_, b, _, policy := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{Enabled: true}))
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	public := httptest.NewTLSServer(s.handler())
	defer public.Close()
	client := api.NewClient(public.URL, bearer)
	client.HTTPClient().Transport = public.Client().Transport
	if _, err := client.PutObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID, api.ObjectBucketObjectLockConfiguration{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.reconcileObjectBucketVersioning(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.reconcileObjectCapacity(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.reconcileObjectBucketObjectLock(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		advance()
	}
	lock, err := st.(state.ObjectBucketObjectLockStore).GetObjectBucketObjectLock(t.Context(), acct.ID, b.AppID, b.ID)
	if err != nil || lock.State != "ready" {
		t.Fatal(lock, err)
	}
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), acct.ID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-exact"}})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	input := api.ObjectVersionLegalHoldRequest{ID: id, LegalHold: api.ObjectVersionLegalHold{Status: "ON"}}
	j, err := client.PutObjectVersionLegalHold(t.Context(), "encrypted-journal", b.ID, "key", refs[0].ID, input)
	if err != nil || j.State != "waiting" || puts.Load() != 0 {
		t.Fatal("acceptance was not journaled", j, err)
	}
	if err = s.reconcileObjectVersionProtection(t.Context(), nil); err != nil || puts.Load() != 1 {
		t.Fatal(err, puts.Load())
	}
	j, err = client.GetObjectVersionProtection(t.Context(), "encrypted-journal", b.ID, id)
	if err != nil || j.State != "waiting" || j.LastErrorCode != "provider_uncertain" {
		t.Fatal(j, err)
	}
	registry := s.objectStorage
	s.objectStorage = nil
	inspected, inspectErr := client.GetObjectVersionProtection(t.Context(), "encrypted-journal", b.ID, id)
	s.objectStorage = registry
	if inspectErr != nil || inspected.State != "waiting" {
		t.Fatal("missing placement blocked receipt inspection", inspected, inspectErr)
	}
	if raw, _ := json.Marshal(j); strings.Contains(string(raw), "private-exact") || strings.Contains(string(raw), "lease") {
		t.Fatal("private progress leaked", string(raw))
	}
	replay, err := client.PutObjectVersionLegalHold(t.Context(), "encrypted-journal", b.ID, "key", refs[0].ID, input)
	if err != nil || replay.ID != id || puts.Load() != 1 {
		t.Fatal("retry created another operation", replay, err)
	}
	input.LegalHold.Status = "OFF"
	if _, err = client.PutObjectVersionLegalHold(t.Context(), "encrypted-journal", b.ID, "key", refs[0].ID, input); err == nil {
		t.Fatal("retry ID accepted a different policy")
	}
	advance()
	if pool != nil {
		s.store = state.NewPgStore(pool)
	}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{}))
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectVersionProtection(t.Context(), nil); err != nil {
		t.Fatal("disabled recovery", err)
	}
	j, err = client.GetObjectVersionProtection(t.Context(), "encrypted-journal", b.ID, id)
	if err != nil || j.State != "ready" || puts.Load() != 1 {
		t.Fatal("recovery repeated PUT", j, err, puts.Load())
	}
	read, err := client.GetObjectVersionLegalHold(t.Context(), "encrypted-journal", b.ID, "key", refs[0].ID)
	if err != nil || read.LegalHold.Status != "ON" {
		t.Fatal(read, err)
	}
	before := calls.Load()
	if _, err = client.GetObjectVersionLegalHold(t.Context(), "encrypted-journal", b.ID, "other", refs[0].ID); err == nil || calls.Load() != before {
		t.Fatal("foreign key contacted provider", err)
	}
	if _, err = client.PutObjectVersionLegalHold(t.Context(), "encrypted-journal", b.ID, "key", refs[0].ID, api.ObjectVersionLegalHoldRequest{ID: uuid.NewString(), LegalHold: api.ObjectVersionLegalHold{Status: "OFF"}}); err == nil {
		t.Fatal("disabled enrollment accepted new intent")
	}
}
