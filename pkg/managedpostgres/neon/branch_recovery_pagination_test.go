// adr: 590 — complete discovery must precede restore creation or cleanup.
package neon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRestoreRecoveryCompletesBranchPagesBeforeMutationOrAbsence(t *testing.T) {
	for _, operation := range []string{"restore", "cleanup", "discover"} {
		for _, mode := range []string{"later_target", "uppercase_sort_order", "later_duplicate", "missing_list", "missing_later_list", "cycle", "page_budget", "foreign_project", "wrong_sort", "descending_sort_order", "descending_later_page", "overlapping_pages"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				point := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
				var lists, posts, deletes atomic.Int32
				var p *Provider
				p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := branch{ID: "br-target", ProjectID: "project-source", Name: p.restoreBranchName("logical-target"), ParentID: "br-source", ParentTimestamp: point.Format(time.RFC3339), InitSource: "parent-data"}
					switch {
					case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/project-source/branches":
						page := int(lists.Add(1))
						if r.URL.Query().Get("sort_by") != "created_at" || r.URL.Query().Get("sort_order") != "asc" || r.URL.Query().Get("search") != target.Name {
							t.Error("discovery did not pin an immutable sort and owner search")
						}
						rows, next := []branch{{ID: fmt.Sprintf("br-unrelated-%d", page), Name: "unrelated"}}, "next-page"
						pagination := map[string]any{"sort_by": "created_at", "sort_order": "asc"}
						switch mode {
						case "later_target", "uppercase_sort_order":
							if page == 2 {
								rows, next = []branch{target}, ""
							}
							if mode == "uppercase_sort_order" {
								pagination["sort_order"] = "ASC"
							}
						case "later_duplicate":
							rows = []branch{target}
							if page == 2 {
								next = ""
							}
						case "missing_list":
							rows, next = nil, ""
						case "missing_later_list":
							if page == 2 {
								rows, next = nil, ""
							}
						case "page_budget":
							next = fmt.Sprintf("cursor-%d", page)
						case "foreign_project":
							target.ProjectID = "project-other"
							rows, next = []branch{target}, ""
						case "wrong_sort":
							pagination["sort_by"] = "updated_at"
						case "descending_sort_order":
							pagination["sort_order"] = "DESC"
						case "descending_later_page":
							if page == 2 {
								rows, next = []branch{target}, ""
								pagination["sort_order"] = "desc"
							}
						case "overlapping_pages":
							rows[0].ID = "br-unrelated-repeated"
							if page == 2 {
								rows, next = append(rows, target), ""
							}
						}
						pagination["next"] = next
						writeResponse(t, w, http.StatusOK, map[string]any{"branches": rows, "pagination": pagination})
					case r.Method == http.MethodPost:
						posts.Add(1)
						writeResponse(t, w, http.StatusCreated, map[string]any{"branch": target})
					case r.Method == http.MethodDelete:
						deletes.Add(1)
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/project-source/branches/br-target":
						w.WriteHeader(http.StatusNotFound)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusInternalServerError)
					}
				}))
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				var err error
				switch operation {
				case "restore":
					var actual managedpostgres.ObservedDatabase
					actual, err = p.Restore(ctx, managedpostgres.RestoreRequest{ResourceID: "logical-target", SourceResourceID: "project-source/br-source", PointInTime: point, Spec: testDatabaseSpec(), IdempotencyKey: "restore"})
					if err == nil && actual.ProviderResourceID != "project-source/br-target" {
						t.Fatal("wrong target adopted")
					}
				case "cleanup":
					var actual managedpostgres.DeleteResult
					actual, err = p.Delete(ctx, managedpostgres.DeleteRequest{ResourceID: "logical-target", RestoreSourceResourceID: "project-source/br-source", RestorePointInTime: point, IdempotencyKey: "delete"})
					if err == nil && (!actual.Done || deletes.Load() != 1) {
						t.Fatal("cleanup reported absence without finding/deleting the later target")
					}
				case "discover":
					var id string
					id, err = p.Discover(ctx, managedpostgres.ResourceDiscoveryRequest{ResourceID: "logical-target", RestoreSourceResourceID: "project-source/br-source"})
					if err == nil && id != "project-source/br-target" {
						t.Fatal("wrong discovery identity")
					}
				}
				want := error(nil)
				if mode == "later_duplicate" || mode == "foreign_project" || mode == "overlapping_pages" {
					want = managedpostgres.ErrConflict
				} else if mode != "later_target" && mode != "uppercase_sort_order" {
					want = managedpostgres.ErrUnavailable
				}
				if !errors.Is(err, want) || posts.Load() != 0 || err != nil && deletes.Load() != 0 || lists.Load() > 10 {
					t.Fatalf("incomplete discovery: %v; want %v; lists=%d posts=%d deletes=%d", err, want, lists.Load(), posts.Load(), deletes.Load())
				}
			})
		}
	}
}
