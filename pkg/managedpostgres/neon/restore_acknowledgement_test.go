// adr: 590
package neon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRestoreHydratesAsyncAcknowledgementWithoutRepeatingCreate(t *testing.T) {
	for _, mode := range []string{"created", "recovered", "lost_acknowledgement", "cleanup", "changed_identity", "wrong_point", "never_settles", "cancelled", "missing_identity"} {
		t.Run(mode, func(t *testing.T) {
			point := time.Date(2026, 10, 6, 19, 44, 10, 0, time.UTC)
			request := managedpostgres.RestoreRequest{ResourceID: "test-target", SourceResourceID: "project-source/br-source", PointInTime: point, Spec: testDatabaseSpec(), IdempotencyKey: "restore"}
			posts, reads, deletes := 0, 0, 0
			var provider *Provider
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := branch{ID: "br-target", Name: provider.restoreBranchName(request.ResourceID)}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/project-source/branches":
					branches := []branch{}
					if posts > 0 || mode == "recovered" || mode == "cleanup" {
						branches = append(branches, target)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": branches})
				case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/project-source/branches":
					posts++
					if mode == "lost_acknowledgement" {
						w.WriteHeader(http.StatusBadGateway)
						return
					}
					if mode == "cancelled" {
						cancel()
					}
					if mode == "missing_identity" {
						target.ID = ""
					}
					writeResponse(t, w, http.StatusCreated, map[string]any{"branch": target})
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/project-source/branches/br-target":
					reads++
					if deletes > 0 {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if mode == "never_settles" {
						cancel()
					} else if reads > 1 {
						target.ProjectID, target.ParentID = "project-source", "br-source"
						target.ParentTimestamp, target.InitSource = point.Format(time.RFC3339), "parent-data"
						if mode == "changed_identity" {
							target.ID = "br-substitute"
						}
						if mode == "wrong_point" {
							target.ParentTimestamp = point.Add(time.Second).Format(time.RFC3339)
						}
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branch": target})
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/projects/project-source/branches/br-target":
					deletes++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			if mode == "cleanup" {
				result, err := provider.Delete(ctx, managedpostgres.DeleteRequest{ResourceID: request.ResourceID, RestoreSourceResourceID: request.SourceResourceID, RestorePointInTime: point, IdempotencyKey: "delete"})
				if err != nil || !result.Done || deletes != 1 || posts != 0 {
					t.Fatalf("cleanup = %+v, %v; posts=%d deletes=%d", result, err, posts, deletes)
				}
				return
			}
			observed, err := provider.Restore(ctx, request)
			wantErr := error(nil)
			switch mode {
			case "changed_identity", "wrong_point":
				wantErr = managedpostgres.ErrConflict
			case "never_settles", "missing_identity":
				wantErr = managedpostgres.ErrUnavailable
			case "cancelled":
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) || (err == nil && (observed.RestoreLineage == nil || !observed.RestoreLineage.PointInTime.Equal(point))) ||
				(err != nil && (observed.ProviderResourceID != "" || observed.RestoreLineage != nil)) {
				t.Fatalf("restore = %+v, %v; want %v", observed, err, wantErr)
			}
			wantPosts := 1
			if mode == "recovered" {
				wantPosts = 0
			}
			if posts != wantPosts {
				t.Fatalf("non-idempotent create repeated: %d", posts)
			}
		})
	}
}
