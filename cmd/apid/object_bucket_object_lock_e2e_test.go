package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 422
func TestBucketObjectLockControlE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	var offset atomic.Int64
	now := time.Now().UTC()
	e.store.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
	bucketObjectLockControlE2E(t, e.s, e.store, e.acct, e.key, nil, func() { offset.Add(int64(20 * time.Minute)) })
}
func TestBucketObjectLockControlE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	bucketObjectLockControlE2E(t, e.s, e.store, e.acct, e.key, e.pool, func() {
		if _, err := e.pool.Exec(t.Context(), `UPDATE object_bucket_object_lock SET retry_at=clock_timestamp(); UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state='propagating' THEN clock_timestamp()-interval '1 second' ELSE propagation_until END`); err != nil {
			t.Fatal(err)
		}
	})
}

func objectLockTestRegistry(t *testing.T, endpoint string, policy api.ObjectStoragePolicy, flags objectstorage.ObjectLockConfig) *objectstorage.Registry {
	t.Helper()
	c := objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: endpoint, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "TEST_KEY", SecretKeyEnv: "TEST_SECRET", ObjectLock: flags}}}
	r, err := objectstorage.NewRegistry(c, func(string) string { return "fixture-secret" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func bucketObjectLockControlE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool, advance func()) {
	t.Helper()
	var mu sync.Mutex
	configuration, versioning := "", ""
	var calls, puts atomic.Int32
	lost := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned native request")
		}
		w.Header().Set("Content-Type", "application/xml")
		q := r.URL.Query()
		switch {
		case q.Has("object-lock"):
			if r.Method == http.MethodGet {
				if configuration == "" {
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
					return
				}
				_, _ = io.WriteString(w, configuration)
				return
			}
			if r.Method != http.MethodPut {
				t.Error("unexpected lock method", r.Method)
				w.WriteHeader(400)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			configuration = string(body)
			puts.Add(1)
			if lost {
				lost = false
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			}
		case q.Has("versioning"):
			if r.Method == http.MethodPut {
				versioning = "Enabled"
				return
			}
			if versioning == "" {
				_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
				return
			}
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>`+versioning+`</Status></VersioningConfiguration>`)
		case q.Has("versions"):
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
		case q.Has("uploads"):
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		default:
			t.Error("unexpected native request", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	_, b, _, policy := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	flags := objectstorage.ObjectLockConfig{Enabled: true, EventHolds: true}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, flags))
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	management := httptest.NewServer(s.handler())
	defer management.Close()
	client := api.NewClient(management.URL, bearer)
	caps, err := client.GetObjectBucketObjectLockCapabilities(t.Context(), "encrypted-journal", b.ID)
	if err != nil || !caps.BucketConfiguration || !caps.DefaultEventHold || calls.Load() != 0 {
		t.Fatal(caps, err, calls.Load())
	}
	days, years := int32(7), int32(1)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days, DefaultEventHold: &api.ObjectRetentionPeriod{Years: &years}}}
	j, err := client.PutObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID, cfg)
	if err != nil || j.State != "waiting" || j.Revision != 1 || !j.EnabledRequired || j.DesiredConfiguration == nil || !j.DesiredConfiguration.Equal(cfg) {
		t.Fatal("accept intent", j, err)
	}
	if err = s.reconcileObjectBucketObjectLock(t.Context(), nil); err != nil || puts.Load() != 0 {
		t.Fatal("versioning drain bypassed", err, puts.Load())
	}
	if _, err = st.(state.ObjectBucketVersioningStore).RequestObjectBucketVersioning(t.Context(), acct.ID, b.AppID, b.ID, "Suspended"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("suspension allowed", err)
	}
	if err = s.reconcileObjectBucketVersioning(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	advance()
	if err = s.reconcileObjectBucketVersioning(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectCapacity(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	advance()
	if err = s.reconcileObjectBucketVersioning(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectBucketObjectLock(t.Context(), nil); err != nil || puts.Load() != 1 {
		t.Fatal("lost ACK not dispatched", err, puts.Load())
	}
	durable, err := st.(state.ObjectBucketObjectLockStore).GetObjectBucketObjectLock(t.Context(), acct.ID, b.AppID, b.ID)
	if err != nil || durable.State != "waiting" || !durable.Dispatched {
		t.Fatal("lost ACK published readiness", durable, err)
	}
	advance()
	if pool != nil {
		s.store = state.NewPgStore(pool)
	}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{}))
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	j, err = client.GetObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID)
	if err != nil || j.State != "waiting" || calls.Load() != before {
		t.Fatal("disabled progress read contacted native", j, err)
	}
	if err = s.reconcileObjectBucketObjectLock(t.Context(), nil); err != nil {
		t.Fatal("disabled recovery", err)
	}
	j, err = client.GetObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID)
	if err != nil || j.State != "ready" || !j.ObservedKnown || j.ObservedConfiguration == nil || !j.ObservedConfiguration.Equal(cfg) || puts.Load() != 1 {
		t.Fatal("recovery lost or repeated accepted request", j, err, puts.Load())
	}
	caps, err = client.GetObjectBucketObjectLockCapabilities(t.Context(), "encrypted-journal", b.ID)
	if err != nil || caps.BucketConfiguration || caps.DefaultEventHold {
		t.Fatal("disabled capabilities advertised", caps, err)
	}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, flags))
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	clear := api.ObjectBucketObjectLockConfiguration{Enabled: true}
	j, err = client.PutObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID, clear)
	if err != nil || j.Revision != 2 {
		t.Fatal("accept clear", j, err)
	}
	if err = s.reconcileObjectBucketObjectLock(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	j, err = client.GetObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID)
	if err != nil || j.State != "ready" || !j.EnabledRequired || j.ObservedConfiguration == nil || !j.ObservedConfiguration.Equal(clear) || puts.Load() != 2 {
		t.Fatal("clear disabled protection", j, err)
	}
	mu.Lock()
	configuration = `<ObjectLockConfiguration/>`
	mu.Unlock()
	j, err = client.GetObjectBucketObjectLock(t.Context(), "encrypted-journal", b.ID)
	if err != nil || j.State != "waiting" || j.ObservedKnown || !j.EnabledRequired || j.ObservedConfiguration != nil {
		t.Fatal("empty 200 became absence", j, err)
	}
}
