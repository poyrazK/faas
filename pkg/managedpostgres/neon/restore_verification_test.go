// adr: 1259
package neon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// TestRestoreVerificationRecordsTheProvenTarget pins what the first
// independent proof hands the service: the exact target and source, the
// requested point, and a normalized fingerprint of the immutable metadata.
func TestRestoreVerificationRecordsTheProvenTarget(t *testing.T) {
	point := time.Date(2026, 10, 14, 12, 15, 0, 0, time.UTC)
	target := branch{ID: "br-target", ProjectID: "project-source", Name: "restore", ParentID: "br-source", ParentLSN: "0/1e25788",
		InitSource: "parent-data", CurrentState: "ready", CreatedAt: "2026-10-14T12:20:59Z"}
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/projects/project-source":
			writeResponse(t, w, http.StatusOK, map[string]any{"project": project{ID: "project-source"}})
		case "/api/v2/projects/project-source/branches":
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}}})
		case "/api/v2/projects/project-source/endpoints":
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []endpoint{{ID: "ep-source", ProjectID: "project-source", BranchID: "br-source", RegionID: "aws-eu-central-1", Type: "read_write", CurrentState: "idle", Host: "ep-source.eu-central-1.aws.neon.tech"}}})
		case "/api/v2/projects/project-source/connection_uri":
			writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgresql://" + ownerLogin + ":test@ep-source.eu-central-1.aws.neon.tech/gregale?sslmode=require"})
		case "/api/v2/projects/project-source/branches/br-target":
			writeResponse(t, w, http.StatusOK, map[string]any{"branch": target})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	provider.historicalPoints = fakeHistoricalPointReader{read: func(context.Context, string) (string, error) { return "0/1E25788", nil }}
	request := managedpostgres.RestoreRequest{SourceResourceID: "project-source/br-source", PointInTime: point}
	observed, err := provider.observeRestoredBranch(t.Context(), "project-source", "br-source", "restore", target, request)
	v := observed.RestoreVerification
	if err != nil || v == nil || v.ProviderResourceID != "project-source/br-target" || v.SourceResourceID != request.SourceResourceID || !v.PointInTime.Equal(point) ||
		v.Evidence != "neon-branch-v1|project-source|br-target|br-source|0/1E25788|2026-10-14T12:20:59Z|parent-data" {
		t.Fatalf("verification = %+v, %v", v, err)
	}
	if strings.Contains(v.Evidence, "test") {
		t.Fatal("verification evidence carries credential material")
	}
}

// TestRecordedRestoreVerificationIsConfirmedWithoutRemapping is the
// production-us failure (2026-10-14): Neon resolves the requested time to a
// drifting LSN, so a recorded verification is confirmed against the target's
// immutable metadata and never remapped. A changed target is still refused.
func TestRecordedRestoreVerificationIsConfirmedWithoutRemapping(t *testing.T) {
	point := time.Date(2026, 10, 14, 12, 15, 0, 0, time.UTC)
	for _, mode := range []string{"inspect", "replay", "changed_lsn", "changed_creation", "replaced", "late_timestamp", "foreign_record", "pending"} {
		t.Run(mode, func(t *testing.T) {
			var provider *Provider
			request := managedpostgres.RestoreRequest{ResourceID: "logical-target", SourceResourceID: "project-source/br-source",
				Spec: testDatabaseSpec(), PointInTime: point, IdempotencyKey: "restore"}
			recorded := branch{ID: "br-target", ProjectID: "project-source", ParentID: "br-source", ParentLSN: "0/1E25788",
				ParentTimestamp: "2026-10-14T12:14:19Z", InitSource: "parent-data", CurrentState: "ready", CreatedAt: "2026-10-14T12:20:59Z"}
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := recorded
				current.Name = provider.restoreBranchName(request.ResourceID)
				switch mode {
				case "changed_lsn":
					current.ParentLSN = "0/1E257A0"
				case "changed_creation":
					current.CreatedAt = "2026-10-14T12:21:30Z"
				case "replaced":
					current.ID = "br-substitute"
				case "late_timestamp":
					current.ParentTimestamp = "2026-10-14T12:15:01Z"
				case "pending":
					current.PendingState = "resetting"
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v2/projects/project-source":
					writeResponse(t, w, http.StatusOK, map[string]any{"project": project{ID: "project-source"}})
				case "GET /api/v2/projects/project-source/branches":
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}, current}})
				case "GET /api/v2/projects/project-source/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []endpoint{{ID: "ep-target", ProjectID: "project-source", BranchID: "br-target", Type: "read_write", CurrentState: "init"}}})
				case "GET /api/v2/projects/project-source/operations":
					writeResponse(t, w, http.StatusOK, map[string]any{"operations": []any{}})
				case "GET /api/v2/projects/project-source/branches/br-target":
					writeResponse(t, w, http.StatusOK, map[string]any{"branch": current})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			provider.historicalPoints = fakeHistoricalPointReader{read: func(context.Context, string) (string, error) {
				t.Error("a recorded verification was remapped through a historical connection")
				return "0/1E257A0", nil
			}}
			recorded.Name = provider.restoreBranchName(request.ResourceID)
			evidence, err := restoreEvidence("project-source", recorded)
			if err != nil {
				t.Fatal(err)
			}
			request.Verification = &managedpostgres.RestoreVerification{ProviderResourceID: "project-source/br-target", SourceResourceID: request.SourceResourceID, PointInTime: point, Evidence: evidence}
			if mode == "foreign_record" {
				request.Verification.PointInTime = point.Add(-time.Second)
			}
			var observed managedpostgres.ObservedDatabase
			if mode == "replay" {
				observed, err = provider.RestoreWithCreationReceipt(t.Context(), request, nil, nil)
			} else {
				observed, err = provider.InspectRestore(t.Context(), "project-source/br-target", request)
			}
			switch mode {
			case "inspect", "replay":
				if err != nil || observed.RestoreLineage == nil || !observed.RestoreLineage.PointInTime.Equal(point) ||
					observed.RestoreLineage.SourceResourceID != request.SourceResourceID || observed.RestoreVerification == nil || observed.RestoreVerification.Evidence != evidence {
					t.Fatalf("confirmed restore = %+v, %v", observed, err)
				}
			case "pending":
				if !errors.Is(err, managedpostgres.ErrUnavailable) || errors.Is(err, managedpostgres.ErrConflict) || observed.RestoreLineage != nil {
					t.Fatalf("unsettled target: %+v, %v", observed, err)
				}
			default:
				if !errors.Is(err, managedpostgres.ErrConflict) || observed.RestoreLineage != nil {
					t.Fatalf("changed target adopted: %+v, %v", observed, err)
				}
			}
		})
	}
}
