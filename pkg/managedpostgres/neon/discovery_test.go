// adr: 581 — read-only identity recovery before catalog persistence and deletion.

package neon

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestDiscoverIsReadOnlyAndUsesStableNames(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "project"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			var provider *Provider
			calls := 0
			logical := "11111111-1111-1111-1111-111111111111"
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Errorf("discovery mutation: %s", r.Method)
				}
				if restore {
					if r.URL.Path != "/api/v2/projects/source-project/branches" {
						t.Errorf("wrong restore parent: %s", r.URL.Path)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{
						{"id": "br-target", "name": provider.restoreBranchName(logical)},
						{"id": "br-unrelated", "name": "unrelated"},
					}})
				} else {
					if r.URL.Path != "/api/v2/projects" || r.URL.Query().Get("org_id") != provider.organizationID || r.URL.Query().Get("search") != provider.projectName(logical) {
						t.Errorf("wrong namespace/name: %s", r.URL)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"projects": []map[string]any{{"id": "discovered-project", "name": provider.projectName(logical)}}})
				}
			}))
			request := managedpostgres.ResourceDiscoveryRequest{ResourceID: logical}
			want := "discovered-project"
			if restore {
				request.RestoreSourceResourceID = (resourceRef{projectID: "source-project", branchID: "br-main"}).String()
				want = (resourceRef{projectID: "source-project", branchID: "br-target"}).String()
			}
			identity, err := provider.Discover(context.Background(), request)
			if err != nil || identity != want || calls != 1 {
				t.Fatalf("identity=%q err=%v calls=%d", identity, err, calls)
			}
		})
	}
}

func TestDiscoverRejectsAbsentOrAmbiguousProjects(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(map[int]string{0: "absent", 2: "ambiguous"}[count], func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("discovery mutation: %s", r.Method)
				}
				projects := make([]map[string]any, 0, count)
				for range count {
					projects = append(projects, map[string]any{"id": "discovered-project", "name": r.URL.Query().Get("search")})
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"projects": projects})
			}))
			identity, err := provider.Discover(context.Background(), managedpostgres.ResourceDiscoveryRequest{ResourceID: "logical-database"})
			want := managedpostgres.ErrNotFound
			if count == 2 {
				want = managedpostgres.ErrConflict
			}
			if identity != "" || !errors.Is(err, want) {
				t.Fatalf("identity=%q err=%v want=%v", identity, err, want)
			}
		})
	}
}
