package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 564
func TestBucketObjectLockNativeDurableRecovery(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint(pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableVersioning(t)
			var mu sync.Mutex
			native := ""
			var calls, puts atomic.Int32
			var lost atomic.Bool
			lost.Store(true)
			provider := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/"+f.bucket.PhysicalName {
					t.Error("wrong placement", r.URL)
				}
				if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
					t.Error("unsigned request")
				}
				w.Header().Set("Content-Type", "application/xml")
				if r.URL.Query().Has("versioning") {
					if r.Method != http.MethodGet {
						t.Error("unexpected versioning mutation")
					}
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status><MfaDelete>Enabled</MfaDelete></VersioningConfiguration>`)
					return
				}
				if !r.URL.Query().Has("object-lock") {
					t.Error("unexpected native operation", r.URL)
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodGet {
					if native == "" {
						w.WriteHeader(404)
						_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
						return
					}
					_, _ = io.WriteString(w, native)
					return
				}
				if r.Method != http.MethodPut {
					t.Error("unexpected native method", r.Method)
					return
				}
				puts.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				native = string(body)
				if lost.Swap(false) {
					w.WriteHeader(500)
					_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
				}
			}))
			lock := f.st.(state.ObjectBucketObjectLockStore)
			metrics := f.st.(state.ObjectStorageProviderUsageStore)
			var metered atomic.Int32
			svc := BucketObjectLockService{Store: lock, Provider: provider.(BucketObjectLockProvider), Versioning: provider.(BucketVersioningProvider), BeforeRequest: func(ctx context.Context) error {
				metered.Add(1)
				return VersioningRequestRecorder(metrics, f.bucket.ID)(ctx)
			}}
			days := int32(7)
			cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
			foreign := f.bucket
			foreign.AccountID = uuid.NewString()
			if _, _, err := svc.Read(t.Context(), foreign); !errors.Is(err, state.ErrNotFound) || calls.Load() != 0 {
				t.Fatal("foreign native read", err, calls.Load())
			}
			if _, err := svc.Request(t.Context(), f.bucket, api.ObjectBucketObjectLockConfiguration{}); !errors.Is(err, ErrInvalid) || calls.Load() != 0 {
				t.Fatal("disable native call", err, calls.Load())
			}
			j, err := svc.Request(t.Context(), f.bucket, cfg)
			if err != nil || j.State != "waiting" || !j.EnabledRequired || j.Revision != 1 {
				t.Fatal(j, err)
			}
			if _, err = svc.Reconcile(t.Context(), f.bucket); !errors.Is(err, ErrUnavailable) || puts.Load() != 1 {
				t.Fatal("lost native acknowledgment", err, puts.Load())
			}
			j, err = lock.GetObjectBucketObjectLock(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if err != nil || !j.Dispatched || j.State != "waiting" {
				t.Fatal(j, err)
			}
			if f.pool != nil {
				if _, err = f.pool.Exec(t.Context(), `UPDATE object_bucket_object_lock SET retry_at=clock_timestamp() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.retry()
			}
			svc.Store = f.reopen().(state.ObjectBucketObjectLockStore)
			restarted := restartEncryptionS3(t, provider.(*S3))
			svc.Provider, svc.Versioning = restarted, restarted
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.State != "ready" || !j.ObservedKnown || !j.ObservedConfiguration.Equal(cfg) || puts.Load() != 1 {
				t.Fatal("restart repeated successful mutation", j, err, puts.Load())
			}
			clear := api.ObjectBucketObjectLockConfiguration{Enabled: true}
			if _, err = svc.Request(t.Context(), f.bucket, clear); err != nil {
				t.Fatal(err)
			}
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.Revision != 2 || !j.ObservedConfiguration.Equal(clear) || !j.EnabledRequired || puts.Load() != 2 {
				t.Fatal("default clear", j, err, puts.Load())
			}
			mu.Lock()
			native = `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><FuturePolicy>unknown</FuturePolicy></ObjectLockConfiguration>`
			mu.Unlock()
			j, _, err = svc.Read(t.Context(), f.bucket)
			if !errors.Is(err, ErrUnsupported) || j.ObservedKnown || j.ObservedConfiguration != nil || !j.NativeEnabledObserved || j.State != "waiting" {
				t.Fatal("future policy became a clear", j, err)
			}
			if _, err = f.st.RequestObjectBucketVersioning(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "Suspended"); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unknown policy lost versioning latch", err)
			}
			if metered.Load() != calls.Load() {
				t.Fatal("unmetered native requests", metered.Load(), calls.Load())
			}
		})
	}
}

// A transient unknown observation on an unenrolled bucket must be recoverable
// even if legacy grants cannot be drained. Exact native absence authorizes no
// policy mutation and does not erase any previously observed enablement.
func TestBucketObjectLockUnenrolledUnknownRecovery(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint(pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			if err := f.st.AdmitObjectURL(t.Context(), f.bucket.AccountID, f.bucket.ID, "legacy", 1, true, f.policy); err != nil {
				t.Fatal(err)
			}
			var unavailable atomic.Bool
			unavailable.Store(true)
			var puts atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					puts.Add(1)
					t.Error("unexpected mutation")
					return
				}
				if unavailable.Swap(false) {
					w.WriteHeader(500)
					_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
					return
				}
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
			}))
			svc := BucketObjectLockService{Store: f.st.(state.ObjectBucketObjectLockStore), Provider: p.(BucketObjectLockProvider)}
			j, _, err := svc.Read(t.Context(), f.bucket)
			if !errors.Is(err, ErrUnavailable) || j.State != "waiting" || j.EnabledRequired || j.ObservedKnown {
				t.Fatal(j, err)
			}
			if f.pool != nil {
				if _, err = f.pool.Exec(t.Context(), `UPDATE object_bucket_object_lock SET retry_at=clock_timestamp() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.retry()
			}
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.State != "ready" || !j.ObservedKnown || j.ObservedConfiguration == nil || j.ObservedConfiguration.Enabled || puts.Load() != 0 {
				t.Fatal("unlocked recovery required legacy drain", j, err, puts.Load())
			}
		})
	}
}
