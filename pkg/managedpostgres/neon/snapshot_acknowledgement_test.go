// adr: 590
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSnapshotHydratesAsyncAcknowledgementBeforeRetention(t *testing.T) {
	for _, fault := range []string{"settles", "missing_expiry", "wrong_point", "wrong_source", "wrong_owner", "cancelled", "missing_identity"} {
		t.Run(fault, func(t *testing.T) {
			point := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			r := managedpostgres.SnapshotCaptureRequest{ResourceID: "capture", SourceResourceID: "project-source/br-source", PointInTime: point, IdempotencyKey: "capture"}
			posts, reads, patches := 0, 0, 0
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var p *Provider
			p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				row := snapshot{ID: "snap-owned", Name: p.snapshotName(r.ResourceID), SourceBranchID: "br-source", Manual: true, CreatedAt: point.Add(time.Minute).Format(time.RFC3339)}
				switch req.Method {
				case http.MethodPost:
					posts++
					if fault == "missing_identity" {
						row.ID = ""
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"snapshot": row})
				case http.MethodGet:
					if posts == 0 {
						writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": []snapshot{}})
						return
					}
					reads++
					if fault == "cancelled" || fault == "missing_expiry" {
						cancel()
					} else if reads > 1 {
						row.Timestamp = point.Format(time.RFC3339)
						row.ExpiresAt = json.RawMessage("null")
						switch fault {
						case "wrong_point":
							row.Timestamp = point.Add(time.Second).Format(time.RFC3339)
						case "wrong_source":
							row.SourceBranchID = "br-unrelated"
						case "wrong_owner":
							row.Name = "unrelated-owner"
						}
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": []snapshot{row}})
				case http.MethodPatch:
					patches++
					w.WriteHeader(http.StatusInternalServerError)
				default:
					t.Errorf("unexpected mutation: %s", req.Method)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			actual, err := p.CaptureSnapshot(ctx, r)
			if posts != 1 || patches != 0 {
				t.Fatalf("repeated create or premature retention: posts=%d patches=%d", posts, patches)
			}
			if fault == "settles" {
				if err != nil || actual.ProviderSnapshotID != "project-source/snapshots/snap-owned" || !actual.PointInTime.Equal(point) || actual.ExpiresAt != nil {
					t.Fatalf("snapshot hydration: %+v, %v", actual, err)
				}
			} else if err == nil || actual.ProviderSnapshotID != "" {
				t.Fatalf("unproven snapshot returned: %+v, %v", actual, err)
			} else if (fault == "wrong_point" || fault == "wrong_source" || fault == "wrong_owner") && !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}
