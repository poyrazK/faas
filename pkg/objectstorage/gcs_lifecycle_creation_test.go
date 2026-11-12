package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGCSLifecycleCurrentCreationClock(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, scenario := range []string{"old", "tag edit", "young", "missing creation", "wrong generation", "preflight metadata race"} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, scenario), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				f.enableVersioning(t)
				now := time.Now().UTC().Truncate(time.Second)
				created, modified := now.AddDate(0, 0, -60), now.AddDate(0, 0, -60)
				if scenario == "young" {
					created = now.Add(-time.Hour)
					modified = created
				}
				tags := map[string]string{}
				metaVersion := 1
				removed, deletes, requests, pinned := false, 0, 0, 0
				provider := newTaggingGCS(t, func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.URL.Query().Has("versions") {
						_, _ = io.WriteString(w, `<ListVersionsResult><Encoding-Type>url</Encoding-Type><IsTruncated>false</IsTruncated>`)
						if !removed && r.URL.Query().Get("key-marker") == "" {
							_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>100</VersionId><Size>1</Size><ETag>&quot;etag&quot;</ETag><IsLatest>true</IsLatest><LastModified>%s</LastModified><DeletedTime/></Version>`, modified.Format(time.RFC3339))
						}
						_, _ = io.WriteString(w, `</ListVersionsResult>`)
						return
					}
					if r.Method == http.MethodDelete {
						if r.Header.Get("X-Goog-If-Generation-Match") != "100" || r.URL.Query().Get("generation") != "" {
							t.Error("mutable deletion lost its original generation fence", r.URL, r.Header)
						}
						deletes++
						removed = true
						w.WriteHeader(http.StatusNoContent)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if !strings.Contains(r.URL.Path, "/o/") {
						_ = json.NewEncoder(w).Encode(map[string]any{"versioning": map[string]bool{"enabled": true}})
						return
					}
					if r.Method == http.MethodPatch {
						var patch struct{ Metadata map[string]string }
						if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
							t.Error(err)
						}
						tags = patch.Metadata
						metaVersion++
						modified = now.Add(-time.Minute)
					}
					generation := "100"
					updated := modified
					if selected := r.URL.Query().Get("generation"); selected != "" {
						if selected != "100" {
							t.Error("creation read targeted another generation", r.URL)
						}
						pinned++
						if scenario == "wrong generation" {
							generation = "200"
						}
						if scenario == "preflight metadata race" && pinned >= 2 {
							updated = now
						}
					}
					body := map[string]any{"name": "key", "generation": generation, "metageneration": fmt.Sprint(metaVersion), "metadata": tags, "size": "1", "etag": "json-etag", "timeCreated": created.Format(time.RFC3339), "updated": updated.Format(time.RFC3339)}
					if scenario == "missing creation" {
						delete(body, "timeCreated")
					}
					_ = json.NewEncoder(w).Encode(body)
				})
				if scenario == "tag edit" {
					if _, err := (TaggingService{Provider: provider}).Do(t.Context(), f.bucket, http.MethodPut, "key", "", map[string]string{"team": "storage"}); err != nil {
						t.Fatal(err)
					}
				}
				rule := api.ObjectLifecycleRule{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(30)}}
				if _, err := (LifecyclePolicyService{Store: f.st, Provider: provider}).Write(t.Context(), f.bucket, []api.ObjectLifecycleRule{rule}); err != nil {
					t.Fatal(err)
				}
				requests = 0
				before := 0
				service := LifecycleExpirationService{Store: f.st, Provider: provider, BeforeRequest: func(context.Context) error { before++; return nil }}
				scan, err := service.Step(t.Context(), f.bucket, f.policy)
				if scenario == "missing creation" || scenario == "wrong generation" {
					if !errors.Is(err, ErrUnavailable) || deletes != 0 || scan.LastKey != "" {
						t.Fatal("unproven creation age allowed progress", scan, deletes, err)
					}
				} else if scenario == "preflight metadata race" {
					if deletes != 0 || pinned < 2 {
						t.Fatal("metadata race dispatched deletion", scan, deletes, pinned, err)
					}
				} else {
					want := 1
					if scenario == "young" {
						want = 0
					}
					if err != nil || deletes != want || scan.LastKey != "key" {
						t.Fatal("creation age was not used", scan, deletes, pinned, err)
					}
					if want == 1 && pinned != 2 {
						t.Fatal("creation not revalidated under the fence", pinned)
					}
					for range 3 {
						scan, err = service.Step(t.Context(), f.bucket, f.policy)
						if err != nil {
							t.Fatal(err)
						}
						if scan.State == "completed" {
							break
						}
					}
					if scan.State != "completed" || deletes != want {
						t.Fatal("scan replayed deletion or failed to finish", scan, deletes)
					}
				}
				if before != requests {
					t.Fatal("native requests escaped metering", before, requests)
				}
			})
		}
	}
}

func TestGCSLifecycleCreationReadRejectsBeforeNativeAttempt(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	provider := newTaggingGCS(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) })
	v := ListedObjectVersion{Object: Object{Key: "key", LastModified: now.Add(-time.Hour)}, ProviderVersionID: "100", IsLatest: true}
	rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}}}
	for _, deny := range []bool{true, false} {
		service := DeletionService{Provider: provider, BeforeRequest: func(context.Context) error {
			if deny {
				return ErrConflict
			}
			return nil
		}}
		_, err := service.lifecycleObject(t.Context(), newLifecycleServiceFixture(t, false).bucket, []ListedObjectVersion{v}, "100", v.LastModified, now, rules)
		if err == nil || deny && (calls != 0 || !errors.Is(err, ErrConflict)) || !deny && calls != 1 {
			t.Fatal("creation read bypassed admission or retried natively", calls, err)
		}
	}
}
