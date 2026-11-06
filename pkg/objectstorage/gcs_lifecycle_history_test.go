package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 550, 628
func TestGCSLifecycleArchivedGenerations(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, scenario := range []string{"live control", "archived only", "edited archived metadata", "recent archival", "missing archival time", "current-only policy"} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, scenario), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				f.enableVersioning(t)
				now := time.Now().UTC().Truncate(time.Second)
				modified, archived := now.AddDate(0, 0, -60), now.AddDate(0, 0, -30)
				if scenario == "edited archived metadata" {
					modified = now.Add(-time.Hour)
				}
				if scenario == "recent archival" {
					archived = now.Add(-time.Hour)
				}
				deletes, removed := 0, false
				native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodDelete {
						if r.URL.Query().Get("generation") != "100" {
							t.Error("deleted another generation", r.URL)
						}
						deletes++
						removed = true
						w.WriteHeader(http.StatusNoContent)
						return
					}
					if !r.URL.Query().Has("versions") {
						t.Errorf("unexpected native operation %s %s", r.Method, r.URL)
						w.WriteHeader(500)
						return
					}
					w.Header().Set("Content-Type", "application/xml")
					_, _ = io.WriteString(w, `<ListVersionsResult><Encoding-Type>url</Encoding-Type><IsTruncated>false</IsTruncated>`)
					if r.URL.Query().Get("key-marker") == "" {
						if !removed && (r.URL.Query().Get("prefix") != "" || scenario != "live control") {
							_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>100</VersionId><Size>1</Size><ETag>&quot;old&quot;</ETag><IsLatest>false</IsLatest><LastModified>%s</LastModified>`, modified.Format(time.RFC3339))
							if scenario != "missing archival time" {
								_, _ = fmt.Fprintf(w, `<DeletedTime>%s</DeletedTime>`, archived.Format(time.RFC3339))
							}
							_, _ = io.WriteString(w, `</Version>`)
						}
						if scenario == "live control" {
							_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>200</VersionId><Size>1</Size><ETag>&quot;new&quot;</ETag><IsLatest>true</IsLatest><LastModified>%s</LastModified></Version>`, now.AddDate(0, 0, -30).Format(time.RFC3339))
						}
					}
					_, _ = io.WriteString(w, `</ListVersionsResult>`)
				}))
				defer native.Close()
				provider := testGCS(native.URL, &fakeGCSStore{bucketState: gcsBucketState{VersioningEnabled: true}})
				rule := api.ObjectLifecycleRule{ID: "expire", Status: "Enabled", NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1}}
				if scenario == "current-only policy" {
					rule.NoncurrentVersionExpiration = nil
					rule.Expiration = &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}
				}
				if _, err := (LifecyclePolicyService{Store: f.st, Provider: provider}).Write(t.Context(), f.bucket, []api.ObjectLifecycleRule{rule}); err != nil {
					t.Fatal(err)
				}
				service := LifecycleExpirationService{Store: f.st, Provider: provider}
				scan, err := service.Step(t.Context(), f.bucket, f.policy)
				if scenario == "missing archival time" {
					if !errors.Is(err, ErrUnavailable) || deletes != 0 || scan.LastKey != "" {
						t.Fatal("unknown age permitted deletion", scan, deletes, err)
					}
					return
				}
				want := 1
				if scenario == "recent archival" || scenario == "current-only policy" {
					want = 0
				}
				if err != nil || scan.LastKey != "key" || deletes != want {
					t.Fatal("scan did not progress with native GCS history", scan, deletes, err)
				}
				for range 3 {
					scan, err = service.Step(t.Context(), f.bucket, f.policy)
					if err != nil {
						t.Fatal("continued scan", scan, err)
					}
					if scan.State == "completed" {
						break
					}
				}
				if scan.State != "completed" || deletes != want {
					t.Fatal("scan did not complete or redispatched", scan, deletes)
				}
			})
		}
	}
}

func TestGCSLifecycleHistoryValidation(t *testing.T) {
	now := time.Now().UTC()
	old := ListedObjectVersion{Object: Object{Key: "key", LastModified: now.AddDate(0, 0, -60)}, ProviderVersionID: "100", NoncurrentSince: now.AddDate(0, 0, -30)}
	newer := old
	newer.ProviderVersionID, newer.NoncurrentSince = "200", now.AddDate(0, 0, -15)
	o, err := gcsLifecycleHistoryObject([]ListedObjectVersion{old, newer}, "100", old.LastModified, now)
	if err != nil || o.NewerNoncurrent != 1 || !o.NoncurrentSince.Equal(old.NoncurrentSince) {
		t.Fatal(o, err)
	}
	newer.NoncurrentSince = old.NoncurrentSince
	o, err = gcsLifecycleHistoryObject([]ListedObjectVersion{old, newer}, "100", old.LastModified, now)
	if err != nil || o.NewerNoncurrent != 0 {
		t.Fatal("timestamp tie changed retention count", o, err)
	}
	if _, err = lifecycleHistoryObject([]ListedObjectVersion{old}, "100", old.LastModified, now); !errors.Is(err, ErrUnavailable) {
		t.Fatal("S3 lost its required latest-version proof", err)
	}
	for _, kind := range []string{"mixed keys", "duplicate generation", "future archival", "marker", "invalid generation", "two live"} {
		t.Run(kind, func(t *testing.T) {
			v := newer
			switch kind {
			case "mixed keys":
				v.Key = "other"
			case "duplicate generation":
				v.ProviderVersionID = "100"
			case "future archival":
				v.NoncurrentSince = now.Add(time.Hour)
			case "marker":
				v.DeleteMarker = true
			case "invalid generation":
				v.ProviderVersionID = strings.Repeat("x", 10)
			case "two live":
				v.IsLatest = true
				v.NoncurrentSince = time.Time{}
			}
			first := old
			if kind == "two live" {
				first.IsLatest = true
				first.NoncurrentSince = time.Time{}
			}
			if _, err := gcsLifecycleHistoryObject([]ListedObjectVersion{first, v}, "100", old.LastModified, now); !errors.Is(err, ErrUnavailable) {
				t.Fatal("ambiguous history accepted", err)
			}
		})
	}
}
