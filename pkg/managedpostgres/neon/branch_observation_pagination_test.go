// adr: 463, 590 — read-only health and source selection require complete metadata.
package neon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestObserveReadsCompleteBranchPagesBeforeReportingHealth(t *testing.T) {
	for _, selector := range []string{"quiet-river-12345678", "quiet-river-12345678/br-main-123"} {
		for _, fault := range []string{"later_branch", "uppercase_sort_order", "descending_later_page", "missing_list", "missing_later_list", "cycle", "page_budget", "duplicate", "foreign_project"} {
			t.Run(selector+"/"+fault, func(t *testing.T) {
				var lists, mutations atomic.Int32
				base := readyProjectHandler(t, &mutations)
				p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						mutations.Add(1)
					}
					if r.URL.Path != "/api/v2/projects/quiet-river-12345678/branches" {
						base.ServeHTTP(w, r)
						return
					}
					page := int(lists.Add(1))
					rows := []branch{{ID: fmt.Sprintf("br-other-%d", page), ProjectID: "quiet-river-12345678", CurrentState: "ready"}}
					target := branch{ID: "br-main-123", ProjectID: "quiet-river-12345678", Default: true, CurrentState: "ready"}
					next := "page-2"
					pagination := map[string]any{"sort_by": "created_at", "sort_order": "asc"}
					if page == 2 {
						rows, next = []branch{target}, ""
					}
					switch fault {
					case "uppercase_sort_order":
						pagination["sort_order"] = "ASC"
					case "descending_later_page":
						if page == 2 {
							pagination["sort_order"] = "DESC"
						}
					case "missing_list":
						rows, next = nil, ""
					case "missing_later_list":
						if page == 2 {
							rows = nil
						}
					case "cycle":
						next = "page-2"
					case "page_budget":
						next = fmt.Sprintf("page-%d", page+1)
						rows = []branch{{ID: fmt.Sprintf("br-other-%d", page)}}
					case "duplicate":
						rows = []branch{target}
					case "foreign_project":
						target.ProjectID = "project-other"
						rows, next = []branch{target}, ""
					}
					pagination["next"] = next
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": rows, "pagination": pagination})
				}))
				actual, err := p.Observe(t.Context(), selector)
				if fault == "later_branch" || fault == "uppercase_sort_order" {
					if err != nil || actual.Status != managedpostgres.ProviderStatusReady || actual.ComputeState != managedpostgres.ComputeStateSuspended || lists.Load() != 2 {
						t.Fatalf("later branch health: %+v %v; pages=%d", actual, err, lists.Load())
					}
				} else if !errors.Is(err, managedpostgres.ErrUnavailable) || actual.ProviderResourceID != "" {
					t.Fatalf("unknown branch inventory reported health or absence: %+v %v", actual, err)
				}
				if mutations.Load() != 0 || lists.Load() > 10 {
					t.Fatalf("mutating/unbounded health observation: mutations=%d pages=%d", mutations.Load(), lists.Load())
				}
			})
		}
	}
}

func TestRestoreSelectsDefaultSourceFromLaterBranchPage(t *testing.T) {
	point := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	var lists, posts atomic.Int32
	var p *Provider
	p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			lists.Add(1)
			rows, next := []branch{}, ""
			if r.URL.Query().Get("search") == "" {
				if r.URL.Query().Get("cursor") == "" {
					rows, next = []branch{{ID: "br-unrelated", ProjectID: "quiet-river-12345678"}}, "default-page"
				} else {
					rows = []branch{{ID: "br-main-123", ProjectID: "quiet-river-12345678", Default: true}}
				}
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": rows, "pagination": map[string]any{"next": next}})
		case http.MethodPost:
			posts.Add(1)
			var payload createBranchRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Branch.ParentID != "br-main-123" || payload.Branch.ParentTimestamp != point.Format(time.RFC3339) {
				t.Error("restore did not use the observed default source and exact point")
			}
			writeResponse(t, w, http.StatusCreated, map[string]any{"branch": branch{ID: "br-target", ProjectID: "quiet-river-12345678", Name: p.restoreBranchName("target"),
				ParentID: "br-main-123", ParentTimestamp: point.Format(time.RFC3339), InitSource: "parent-data"}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	actual, err := p.Restore(t.Context(), managedpostgres.RestoreRequest{ResourceID: "target", SourceResourceID: "quiet-river-12345678", PointInTime: point, Spec: testDatabaseSpec(), IdempotencyKey: "restore"})
	if err != nil || actual.ProviderResourceID != "quiet-river-12345678/br-target" || actual.RestoreLineage == nil || actual.RestoreLineage.SourceResourceID != "quiet-river-12345678/br-main-123" || posts.Load() != 1 || lists.Load() != 3 {
		t.Fatalf("later default source: %+v %v; lists=%d posts=%d", actual, err, lists.Load(), posts.Load())
	}
}
