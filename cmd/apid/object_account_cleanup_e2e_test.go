package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectAccountCleanupE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	var offset atomic.Int64
	now := time.Now()
	e.store.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
	objectAccountCleanupE2E(t, e.s, e.store, e.acct, nil, func() { offset.Add(int64(2 * time.Hour)) })
}
func TestObjectAccountCleanupE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	objectAccountCleanupE2E(t, e.s, e.store, e.acct, e.pool, func() {
		if _, err := e.pool.Exec(t.Context(), `UPDATE object_buckets SET retry_at=now()-interval '1 second',lease_until=NULL`); err != nil {
			t.Fatal(err)
		}
	})
}

// Local native HTTP -> adapter -> durable bucket worker -> grace cascade.
// New Object Lock enrollment stays disabled; cleanup still reads protection.
func objectAccountCleanupE2E(t *testing.T, s *server, st state.Store, account state.Account, pool *pgxpool.Pool, advance func()) {
	t.Helper()
	var mu sync.Mutex
	versions := map[string]bool{"null": false, "marker": true}
	for i := range api.ObjectOwnedCleanupBatchSize + 1 {
		versions[fmt.Sprintf("v%03d", i)] = false
	}
	hold, retained, lost := true, true, true
	bucketDeleted, lateWrite, lostBucket := false, true, true
	var deletes, calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
			t.Error("unsigned or bypassing cleanup", r.Header)
		}
		w.Header().Set("Content-Type", "application/xml")
		q := r.URL.Query()
		if bucketDeleted {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchBucket</Code></Error>`)
			return
		}
		switch {
		case q.Has("versioning"):
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
		case q.Has("object-lock"):
			_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
		case q.Has("versions"):
			if q.Get("max-keys") != strconv.Itoa(api.ObjectOwnedCleanupBatchSize) || q.Get("key-marker") != "" || q.Get("version-id-marker") != "" {
				t.Error("unbounded or stale cleanup cursor", q)
			}
			ids := make([]string, 0, len(versions))
			for id := range versions {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			truncated := len(ids) > api.ObjectOwnedCleanupBatchSize
			if truncated {
				ids = ids[:api.ObjectOwnedCleanupBatchSize]
			}
			_, _ = fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>%t</IsTruncated>`, truncated)
			if truncated {
				_, _ = fmt.Fprintf(w, `<NextKeyMarker>proof</NextKeyMarker><NextVersionIdMarker>%s</NextVersionIdMarker>`, ids[len(ids)-1])
			}
			for _, id := range ids {
				if versions[id] {
					_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>proof</Key><VersionId>%s</VersionId></DeleteMarker>`, id)
				} else {
					_, _ = fmt.Fprintf(w, `<Version><Key>proof</Key><VersionId>%s</VersionId><Size>1</Size></Version>`, id)
				}
			}
			_, _ = io.WriteString(w, `</ListVersionsResult>`)
		case q.Has("legal-hold"):
			if versions[q.Get("versionId")] {
				t.Error("protection read on a marker")
			}
			status := "OFF"
			if hold {
				status = "ON"
			}
			_, _ = fmt.Fprintf(w, `<LegalHold><Status>%s</Status></LegalHold>`, status)
		case q.Has("retention"):
			if retained {
				_, _ = io.WriteString(w, `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>2099-01-01T00:00:00Z</RetainUntilDate></Retention>`)
			} else {
				_, _ = io.WriteString(w, `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>2000-01-01T00:00:00Z</RetainUntilDate></Retention>`)
			}
		case r.Method == http.MethodDelete && q.Has("versionId"):
			id := q.Get("versionId")
			_, existed := versions[id]
			if !existed {
				t.Error("cleanup repeated a removed identity", id)
			}
			delete(versions, id)
			deletes.Add(1)
			if lost {
				lost = false
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
				return
			}
			w.Header().Set("X-Amz-Version-Id", id)
			w.WriteHeader(204)
		case r.Method == http.MethodDelete && r.URL.Path == "/physical":
			// A legacy provider capability can still arrive after enumeration. A
			// nonempty result must retain the sealed owner and re-enumerate.
			if lateWrite {
				lateWrite = false
				versions["late"] = false
			}
			if len(versions) > 0 {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `<Error><Code>BucketNotEmpty</Code></Error>`)
				return
			}
			bucketDeleted = true
			if lostBucket {
				lostBucket = false
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
				return
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected provider request", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(upstream.Close)
	p := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 1000, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry := objectLockTestRegistry(t, upstream.URL, p, objectstorage.ObjectLockConfig{})
	s.WithObjectStorage(registry)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "account-cleanup", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	buckets := st.(state.ObjectBucketStore)
	b, err := buckets.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "physical"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupExpiredAccountObjectBuckets(t.Context(), account); !errors.Is(err, state.ErrConflict) || calls.Load() != 0 {
		t.Fatal("cleanup ran before grace", err, calls.Load())
	}
	if err = st.MarkAccountDeletionPending(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-31 * 24 * time.Hour)
	if pool != nil {
		_, err = pool.Exec(t.Context(), `UPDATE accounts SET deletion_requested_at=$2 WHERE id=$1`, account.ID, expired)
	} else {
		err = st.(*state.MemStore).SetDeletionRequestedAtForTest(account.ID, expired)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RestoreAccount(t.Context(), account.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired account restored", err)
	}
	// Try a new name with the same still-owned app. The SQL trigger also
	// covers older replicas while account cleanup is in progress.
	newBucket := b
	newBucket.ID = uuid.NewString()
	newBucket.Name = "late"
	newBucket.PhysicalName = "late"
	if _, err = buckets.ReserveObjectBucket(t.Context(), newBucket, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatal("inactive account accepted new bucket", err)
	}
	loop := grace.New(grace.Params{Store: st, BeforeAccountDelete: s.cleanupExpiredAccountObjectBuckets})
	checkPending := func(code string) {
		t.Helper()
		kept, err := buckets.GetObjectBucket(t.Context(), account.ID, app.ID, b.ID)
		if err != nil || kept.State != "deleting" || kept.LeaseToken != "" || kept.LastErrorCode != code {
			t.Fatal("cleanup lost its durable fence", kept, err)
		}
		if _, err = st.AccountByID(t.Context(), account.ID); err != nil {
			t.Fatal("account metadata purged early", err)
		}
	}
	if err = loop.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkPending("protected")
	if deletes.Load() != 0 {
		t.Fatal("legal hold bypassed")
	}
	mu.Lock()
	hold = false
	mu.Unlock()
	advance()
	if err = loop.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkPending("protected")
	if deletes.Load() != 0 {
		t.Fatal("retention bypassed")
	}
	mu.Lock()
	retained = false
	mu.Unlock()
	advance()
	if err = loop.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkPending("temporary")
	if deletes.Load() != 1 {
		t.Fatal("uncertain delete replayed within one attempt", deletes.Load())
	}
	// A reconstructed server uses the stored deleting owner even with all
	// customer ingress/capability enrollment disabled.
	restarted := newServer(st, testLogger(), "gregale.dev", noopNotifier{}).WithObjectStorage(objectLockTestRegistry(t, upstream.URL, p, objectstorage.ObjectLockConfig{}))
	advance()
	if err = restarted.reconcileObjectBuckets(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	checkPending("cleanup_pending")
	advance()
	if err = restarted.reconcileObjectBuckets(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	checkPending("temporary") // Native nonempty response never reopens ownership.
	advance()
	if err = restarted.reconcileObjectBuckets(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	checkPending("temporary") // Native bucket DELETE committed, acknowledgment lost.
	advance()
	if err = restarted.reconcileObjectBuckets(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.GetObjectBucket(t.Context(), account.ID, app.ID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("native bucket not tombstoned", err)
	}
	loop = grace.New(grace.Params{Store: st, BeforeAccountDelete: restarted.cleanupExpiredAccountObjectBuckets})
	if err = loop.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AccountByID(t.Context(), account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("completed account cleanup did not cascade", err)
	}
	if deletes.Load() != api.ObjectOwnedCleanupBatchSize+4 {
		t.Fatal("retained data or markers leaked", deletes.Load())
	}
}
