// adr: 20261119065000
package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Object Lock reaches a creation-only provider only in CreateBucket itself.
func TestS3CreateBucketObjectLockFlag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func(*S3, string) error
		want   string
	}{
		{"plain", func(p *S3, b string) error { return p.CreateBucket(t.Context(), b) }, ""},
		{"object lock", func(p *S3, b string) error { return p.CreateObjectLockBucket(t.Context(), b) }, "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var creates []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut && r.URL.Path == "/bucket" && r.URL.RawQuery == "" {
					mu.Lock()
					creates = append(creates, r.Header.Get("X-Amz-Bucket-Object-Lock-Enabled"))
					mu.Unlock()
				}
			}))
			defer server.Close()
			cfg := testBackend()
			cfg.Endpoint = server.URL
			p, err := NewS3(cfg, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			if err = tc.create(p.(*S3), "bucket"); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(creates) != 1 || creates[0] != tc.want {
				t.Fatalf("CreateBucket Object Lock headers = %q, want one %q", creates, tc.want)
			}
		})
	}
}

func TestObjectLockCreationOnlyEnrollment(t *testing.T) {
	for _, tc := range []struct {
		c      ObjectLockConfig
		driver string
		ok     bool
	}{
		{ObjectLockConfig{CreationOnly: true}, "s3", false},
		{ObjectLockConfig{Enabled: true, CreationOnly: true}, "s3", true},
		{ObjectLockConfig{Enabled: true, CreationOnly: true}, "gcs", false},
		{ObjectLockConfig{Enabled: true, EventHolds: true, CreationOnly: true}, "s3", true},
	} {
		t.Run(fmt.Sprintf("%+v/%s", tc.c, tc.driver), func(t *testing.T) {
			if err := validateObjectLockConfig(tc.c, tc.driver); (err == nil) != tc.ok {
				t.Fatalf("validateObjectLockConfig = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

// Pool members share Object Lock settings (ADR-20261029174627), now
// including whether Object Lock is creation-only.
func TestPoolMembersShareObjectLockCreationOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b ObjectLockConfig
		same bool
	}{
		{"both creation only", ObjectLockConfig{Enabled: true, CreationOnly: true}, ObjectLockConfig{Enabled: true, CreationOnly: true}, true},
		{"one creation only", ObjectLockConfig{Enabled: true, CreationOnly: true}, ObjectLockConfig{Enabled: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := testBackend(), testBackend()
			a.ObjectLock, b.ObjectLock = tc.a, tc.b
			if got := samePlacementFeatures(a, b); got != tc.same {
				t.Fatalf("samePlacementFeatures = %v, want %v", got, tc.same)
			}
		})
	}
}

func TestObjectLockCreationCapable(t *testing.T) {
	p := historyTestProvider(t, http.NotFoundHandler()).(Provider)
	for _, tc := range []struct {
		name string
		c    ObjectLockConfig
		p    Provider
		want bool
	}{
		{"disabled", ObjectLockConfig{}, p, false},
		{"enabled", ObjectLockConfig{Enabled: true}, p, true},
		{"creation only", ObjectLockConfig{Enabled: true, CreationOnly: true}, p, true},
		{"no creator", ObjectLockConfig{Enabled: true}, struct{ Provider }{p}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.CreationCapable(tc.p); got != tc.want {
				t.Fatalf("CreationCapable = %v, want %v", got, tc.want)
			}
		})
	}
}

// On a creation-only backend an enablement request for a bucket that is not
// already Object Lock enabled is refused before any intent is stored and
// before any native PUT; a bucket created with Object Lock keeps accepting
// default retention changes.
func TestBucketObjectLockCreationOnlyRefusesExistingBucket(t *testing.T) {
	enabled := `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`
	days := int32(1)
	retention := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &days}}
	for _, pg := range []bool{false, true} {
		for _, tc := range []struct {
			name          string
			creationOnly  bool
			native        string
			want          error
			wantRevision  int64
			wantNativePut bool
		}{
			{"later enablement allowed", false, "", nil, 1, false},
			{"creation only refuses plain bucket", true, "", ErrObjectLockRequiresNewBucket, 0, false},
			{"creation only accepts lock bucket", true, enabled, nil, 1, false},
		} {
			t.Run(fmt.Sprintf("pg=%v/%s", pg, tc.name), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				f.enableVersioning(t)
				var puts atomic.Int32
				provider := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/xml")
					if r.URL.Query().Has("versioning") {
						_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
						return
					}
					if r.Method == http.MethodPut {
						puts.Add(1)
						return
					}
					if tc.native == "" {
						w.WriteHeader(404)
						_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
						return
					}
					_, _ = io.WriteString(w, tc.native)
				}))
				lock := f.st.(state.ObjectBucketObjectLockStore)
				svc := BucketObjectLockService{Store: lock, Provider: provider.(BucketObjectLockProvider), Versioning: provider.(BucketVersioningProvider), CreationOnly: tc.creationOnly}
				_, err := svc.Request(t.Context(), f.bucket, retention)
				if !errors.Is(err, tc.want) {
					t.Fatalf("Request error = %v, want %v", err, tc.want)
				}
				j, err := lock.GetObjectBucketObjectLock(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
				if err != nil || j.Revision != tc.wantRevision {
					t.Fatalf("journal revision = %d (%v), want %d", j.Revision, err, tc.wantRevision)
				}
				if got := puts.Load() != 0; got != tc.wantNativePut {
					t.Fatalf("native PUT sent = %v", got)
				}
			})
		}
	}
}

// Reconciliation on a creation-only backend never sends the doomed
// enablement PUT to a bucket that is not natively Object Lock enabled.
func TestBucketObjectLockCreationOnlyReconcileSendsNoEnablement(t *testing.T) {
	f := newLifecycleServiceFixture(t, false)
	f.enableVersioning(t)
	var puts atomic.Int32
	provider := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Has("versioning") {
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
			return
		}
		if r.Method == http.MethodPut {
			puts.Add(1)
			return
		}
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
	}))
	lock := f.st.(state.ObjectBucketObjectLockStore)
	svc := BucketObjectLockService{Store: lock, Provider: provider.(BucketObjectLockProvider), Versioning: provider.(BucketVersioningProvider)}
	if _, err := svc.Request(t.Context(), f.bucket, api.ObjectBucketObjectLockConfiguration{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// The operator later declares the backend creation-only.
	svc.CreationOnly = true
	if _, err := svc.Reconcile(t.Context(), f.bucket); !errors.Is(err, ErrUnsupported) || puts.Load() != 0 {
		t.Fatalf("Reconcile = %v with %d PUTs, want ErrUnsupported and none", err, puts.Load())
	}
}
