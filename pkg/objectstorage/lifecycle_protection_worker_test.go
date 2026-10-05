package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 597
func TestLifecycleProtectionWorkerProgressHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableObjectLock(t)
			modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
			var deletes, holdReads atomic.Int32
			var deleted atomic.Bool
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case q.Has("object-lock"):
					_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
				case q.Has("versioning"):
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
				case q.Has("legal-hold"):
					holdReads.Add(1)
					status := "ON"
					if q.Get("versionId") == "eligible" {
						status = "OFF"
					}
					w.Header().Set("X-Amz-Version-Id", q.Get("versionId"))
					_, _ = fmt.Fprintf(w, `<LegalHold><Status>%s</Status></LegalHold>`, status)
				case q.Has("retention"):
					w.Header().Set("X-Amz-Version-Id", q.Get("versionId"))
					_, _ = io.WriteString(w, `<Retention/>`)
				case q.Has("versions"):
					_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
					if q.Get("key-marker") == "" {
						_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>current</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;current&quot;</ETag></Version>`, modified.AddDate(0, 0, 5).Format(time.RFC3339Nano))
						if q.Get("prefix") != "" {
							// More held versions than a worker step permits. Durable
							// protected receipts must let the next step reach eligible.
							for i := range api.ObjectLifecycleActionsPerStep + 1 {
								_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>held-%s</VersionId><IsLatest>false</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;held&quot;</ETag></Version>`, strconv.Itoa(i), modified.Format(time.RFC3339Nano))
							}
							if !deleted.Load() {
								_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>eligible</VersionId><IsLatest>false</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;eligible&quot;</ETag></Version>`, modified.Format(time.RFC3339Nano))
							}
						}
					}
					_, _ = io.WriteString(w, `</ListVersionsResult>`)
				case r.Method == http.MethodDelete && q.Get("versionId") == "eligible":
					deletes.Add(1)
					deleted.Store(true)
					w.Header().Set("X-Amz-Version-Id", "eligible")
					w.WriteHeader(204)
				default:
					t.Error("attempted protected mutation", r.Method, q)
					w.WriteHeader(500)
				}
			}))
			rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1}}}
			if _, err := f.st.SetObjectBucketLifecycle(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, rules); err != nil {
				t.Fatal(err)
			}
			s := LifecycleExpirationService{Store: f.st, Provider: p.(Provider)}
			j, err := s.Step(t.Context(), f.bucket, f.policy)
			if !errors.Is(err, errLifecycleActionsPending) || j.LastKey != "" || deletes.Load() != 0 || holdReads.Load() != api.ObjectLifecycleActionsPerStep {
				t.Fatal("unbounded or blocked first step", j, err, holdReads.Load())
			}
			lifecycleWorkerDue(t, f)
			s.Store = f.reopen()
			j, err = s.Step(t.Context(), f.bucket, f.policy)
			if err != nil || j.LastKey != "key" || deletes.Load() != 1 || holdReads.Load() != api.ObjectLifecycleActionsPerStep+2 {
				t.Fatal("held receipt blocked later eligible version", j, err, deletes.Load(), holdReads.Load())
			}
			j, err = s.Step(t.Context(), f.bucket, f.policy)
			if err != nil || j.State != "completed" || deletes.Load() != 1 {
				t.Fatal("held version blocked scan completion", j, err)
			}
		})
	}
}

// adr: 597
func TestLifecycleProtectionCurrentExpirationHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableObjectLock(t)
			modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
			target := ListedObjectVersion{Object: Object{Key: "key", LastModified: modified, Size: 1}, ProviderVersionID: "protected-current", IsLatest: true}
			var deletes atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				q := r.URL.Query()
				switch {
				case q.Has("versioning"):
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
				case q.Has("versions"):
					_, _ = fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>protected-current</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;protected&quot;</ETag></Version></ListVersionsResult>`, modified.Format(time.RFC3339Nano))
				case r.Method == http.MethodDelete && q.Get("versionId") == "":
					deletes.Add(1)
					w.Header().Set("X-Amz-Version-Id", "new-marker")
					w.Header().Set("X-Amz-Delete-Marker", "true")
					w.WriteHeader(204)
				default:
					t.Error("ordinary expiration attempted permanent deletion or protection read", r.Method, q)
					w.WriteHeader(500)
				}
			}))
			scan := f.scan(t, []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}}})
			j, err := (DeletionService{Store: f.st, Provider: p.(Provider)}).StartLifecycle(t.Context(), f.bucket, scan, target, "", LifecycleDecision{Kind: "current", RuleID: "expire"}, f.policy)
			if err != nil || j.State != "completed" || !j.DeleteMarker || j.ProtectionRequired || deletes.Load() != 1 {
				t.Fatal("locked current data could not acquire expiration marker", j, err)
			}
		})
	}
}
