package neon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Neon returns a last-item cursor even when the next page is empty.
func TestDiscoverAcceptsEmptyTerminalPageWithRetainedCursor(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Query().Get("limit") != "400" {
			t.Errorf("unexpected discovery request: %s %s", r.Method, r.URL)
		}
		rows := []project{}
		if r.URL.Query().Get("cursor") == "" {
			rows = append(rows, project{ID: "project-found", Name: r.URL.Query().Get("search")})
		}
		writeResponse(t, w, http.StatusOK, map[string]any{"projects": rows, "pagination": map[string]any{"cursor": "last-project"}})
	}))
	id, err := p.Discover(context.Background(), managedpostgres.ResourceDiscoveryRequest{ResourceID: "logical-project"})
	if err != nil || id != "project-found" || calls.Load() != 2 {
		t.Fatalf("discovery id=%q err=%v calls=%d", id, err, calls.Load())
	}
}

func TestInspectCompletesOperationPaginationBeforeReadiness(t *testing.T) {
	for _, fault := range []string{"terminal", "pending_later_page", "cycle", "duplicate", "wrong_project", "missing_list", "page_limit", "failed_page"} {
		t.Run(fault, func(t *testing.T) {
			var calls, posts atomic.Int32
			base := readyProjectHandler(t, &posts)
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/projects/quiet-river-12345678/operations" {
					base.ServeHTTP(w, r)
					return
				}
				page := int(calls.Add(1))
				if r.URL.Query().Get("limit") != "1000" || page > 1 && r.URL.Query().Get("cursor") == "" {
					t.Errorf("unexpected operation query: %s", r.URL)
				}
				ops := []operation{{ID: fmt.Sprintf("op-%d", page), Status: "finished", ProjectID: "quiet-river-12345678"}}
				cursor := "last-operation"
				if page > 1 {
					switch fault {
					case "terminal":
						ops = []operation{}
					case "pending_later_page":
						ops[0].Status, cursor = "running", ""
					case "duplicate":
						ops[0].ID, cursor = "op-1", ""
					case "wrong_project":
						ops[0].ProjectID, cursor = "another-project", ""
					case "missing_list":
						ops, cursor = nil, ""
					case "page_limit":
						cursor = fmt.Sprintf("cursor-%d", page)
					case "failed_page":
						writeResponse(t, w, http.StatusForbidden, nil)
						return
					}
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"operations": ops, "pagination": map[string]any{"cursor": cursor}})
			}))
			got, err := p.Inspect(context.Background(), "quiet-river-12345678")
			switch fault {
			case "terminal":
				if err != nil || got.Status != managedpostgres.ProviderStatusReady {
					t.Fatalf("terminal page observation=%+v err=%v", got, err)
				}
			case "pending_later_page":
				if err != nil || got.Status != managedpostgres.ProviderStatusPending {
					t.Fatalf("later operation skipped: status=%s err=%v", got.Status, err)
				}
			default:
				if err == nil || got.Status == managedpostgres.ProviderStatusReady {
					t.Fatalf("incomplete operations accepted: status=%s err=%v", got.Status, err)
				}
			}
			if calls.Load() < 2 || calls.Load() > maximumOperationPages || posts.Load() != 0 {
				t.Fatalf("unbounded or mutating inspection: calls=%d posts=%d", calls.Load(), posts.Load())
			}
		})
	}
}

func TestProjectDiscoveryRejectsIncompleteAndCyclicPages(t *testing.T) {
	for _, fault := range []string{"cycle", "unavailable", "missing_list", "duplicate"} {
		t.Run(fault, func(t *testing.T) {
			var calls atomic.Int32
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := calls.Add(1)
				result := projectsResponse{Projects: []project{{ID: fmt.Sprintf("project-%d", page), Name: "unrelated"}}}
				result.Pagination.Cursor = fmt.Sprintf("cursor-%d", page%2)
				switch fault {
				case "unavailable":
					result.Projects = []project{}
					result.UnavailableProjectIDs = []string{"unavailable-project"}
				case "missing_list":
					result.Projects = nil
				case "duplicate":
					result.Projects[0].Name = r.URL.Query().Get("search")
				}
				writeResponse(t, w, http.StatusOK, result)
			}))
			id, err := p.Discover(context.Background(), managedpostgres.ResourceDiscoveryRequest{ResourceID: "logical-project"})
			if id != "" || err == nil || errors.Is(err, managedpostgres.ErrNotFound) || calls.Load() > 3 {
				t.Fatalf("incomplete search id=%q err=%v calls=%d", id, err, calls.Load())
			}
		})
	}
}
