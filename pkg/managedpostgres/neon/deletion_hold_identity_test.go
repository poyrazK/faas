package neon

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestDeletionHoldKeepsPinnedBranchWhenDefaultChanges(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete", true: "undelete"}[release], func(t *testing.T) {
			var mu sync.Mutex
			const project = "/api/v2/projects/quiet-river-12345678"
			disabled := map[string]bool{"ep-original-123": release, "ep-copy-123": release}
			var touched []string
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				endpoint := func(id, branch string) map[string]any {
					return map[string]any{"id": id, "branch_id": branch, "type": "read_write", "current_state": "idle", "disabled": disabled[id]}
				}
				switch {
				case r.URL.Path == project:
					writeResponse(t, w, 200, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
				case r.URL.Path == project+"/branches":
					writeResponse(t, w, 200, map[string]any{"branches": []map[string]any{{"id": "br-original-123", "default": false, "current_state": "ready"}, {"id": "br-copy-123", "default": true, "current_state": "ready"}}})
				case r.URL.Path == project+"/endpoints":
					writeResponse(t, w, 200, map[string]any{"endpoints": []map[string]any{endpoint("ep-original-123", "br-original-123"), endpoint("ep-copy-123", "br-copy-123")}})
				case strings.HasPrefix(r.URL.Path, project+"/endpoints/"):
					id := strings.TrimPrefix(r.URL.Path, project+"/endpoints/")
					if r.Method == http.MethodPatch {
						var patch struct {
							Endpoint struct {
								Disabled bool `json:"disabled"`
							} `json:"endpoint"`
						}
						if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
							t.Error(err)
						}
						disabled[id] = patch.Endpoint.Disabled
						touched = append(touched, id)
					}
					branch := "br-original-123"
					if id == "ep-copy-123" {
						branch = "br-copy-123"
					}
					writeResponse(t, w, 200, map[string]any{"endpoint": endpoint(id, branch)})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					writeResponse(t, w, 404, nil)
				}
			}))
			var err error
			if release {
				err = provider.ReleaseDeletionHold(t.Context(), "quiet-river-12345678/br-original-123")
			} else {
				err = provider.HoldForDeletion(t.Context(), "quiet-river-12345678/br-original-123")
			}
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			t.Logf("provider accepted operation; touched=%v, disabled=%v", touched, disabled)
			if disabled["ep-original-123"] == release || disabled["ep-copy-123"] != release {
				t.Fatal("operation followed mutable default onto the copy and left original unchanged")
			}
		})
	}
}
