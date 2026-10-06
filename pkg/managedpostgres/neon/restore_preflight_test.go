// adr: 625 — recovery metadata must not wake or substitute the pinned source.
package neon

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRestoreSourceObservationReadsOnlyPinnedMetadata(t *testing.T) {
	for _, lifecycle := range []string{"quiet-river-12345678", "quiet-river-12345678/br-pinned"} {
		t.Run(lifecycle, func(t *testing.T) {
			calls := 0
			p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.RawQuery != "" {
					t.Error("mutating metadata read", r.Method, r.URL)
				}
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678":
					writeResponse(t, w, 200, map[string]any{"project": map[string]any{"id": "quiet-river-12345678", "org_id": "org-gregale-12345678", "region_id": "aws-eu-central-1", "pg_version": 17, "history_retention_seconds": 3600, "created_at": "2026-10-01T00:00:00Z"}})
				case "/api/v2/projects/quiet-river-12345678/branches/br-pinned":
					writeResponse(t, w, 200, map[string]any{"branch": map[string]any{"id": "br-pinned", "project_id": "quiet-river-12345678", "current_state": "ready", "created_at": "2026-10-05T00:00:00Z", "last_reset_at": "2026-10-06T11:00:00Z", "parent_id": "br-original", "parent_timestamp": "2026-10-04T12:00:00Z", "init_source": "parent-data"}})
				default:
					t.Error("unneeded metadata or default discovery", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			p.now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
			d := managedpostgres.RestoreSourceDefinition{Spec: testDatabaseSpec(), ProviderResourceID: lifecycle, DataResourceID: "quiet-river-12345678/br-pinned"}
			o, err := p.ObserveRestoreSource(t.Context(), d)
			if err != nil || calls != 2 || o.ProviderResourceID != lifecycle || o.DataResourceID != d.DataResourceID || o.Status != managedpostgres.ProviderStatusReady || o.RetentionSeconds != 3600 || o.HistoryBounds != nil || o.HistoryNotBefore.Format(time.RFC3339) != "2026-10-01T00:00:00Z" || o.Lineage == nil || o.Lineage.SourceResourceID != "quiet-river-12345678/br-original" {
				t.Fatal(o, err, calls)
			}
		})
	}
}

func TestRestoreSourceObservationRejectsMissingPinsBeforeHTTP(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid source reached HTTP") }))
	for _, data := range []string{"", "quiet-river-12345678", "another-project/br-pinned", "quiet-river-12345678/br-replacement"} {
		_, err := p.ObserveRestoreSource(t.Context(), managedpostgres.RestoreSourceDefinition{Spec: testDatabaseSpec(), ProviderResourceID: "quiet-river-12345678/br-pinned", DataResourceID: data})
		if !errors.Is(err, managedpostgres.ErrUnsupported) {
			t.Fatal(data, err)
		}
	}
}

func TestRestoreSourceObservationRejectsAmbiguousEvidence(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP") }))
	p.now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	for _, fault := range []string{"missing retention", "wrong owner", "wrong region", "wrong major", "wrong project", "wrong branch", "branch project", "missing project time", "missing branch time", "future branch time", "branch before project", "invalid lineage"} {
		t.Run(fault, func(t *testing.T) {
			retention := int64(3600)
			project := recoveryProject{ID: "quiet-river-12345678", OrganizationID: p.organizationID, RegionID: p.regionID, PostgresMajor: 17, CreatedAt: "2026-10-01T00:00:00Z", RetentionSeconds: &retention}
			b := branch{ID: "br-pinned", ProjectID: project.ID, CreatedAt: "2026-10-05T00:00:00Z", CurrentState: "ready"}
			switch fault {
			case "missing retention":
				project.RetentionSeconds = nil
			case "wrong owner":
				project.OrganizationID = "other"
			case "wrong region":
				project.RegionID = "other"
			case "wrong major":
				project.PostgresMajor = 16
			case "wrong project":
				project.ID = "other"
			case "wrong branch":
				b.ID = "other"
			case "branch project":
				b.ProjectID = "other"
			case "missing project time":
				project.CreatedAt = ""
			case "missing branch time":
				b.CreatedAt = ""
			case "future branch time":
				b.CreatedAt = "2027-01-01T00:00:00Z"
			case "branch before project":
				b.CreatedAt = "2025-01-01T00:00:00Z"
			case "invalid lineage":
				b.ParentID = "br-parent"
				b.ParentTimestamp = "invalid"
				b.InitSource = "parent-data"
			}
			_, err := p.restoreSourceObservation(managedpostgres.RestoreSourceDefinition{Spec: testDatabaseSpec(), ProviderResourceID: "quiet-river-12345678", DataResourceID: "quiet-river-12345678/br-pinned"}, resourceRef{projectID: "quiet-river-12345678", branchID: "br-pinned"}, project, b)
			if err == nil {
				t.Fatal("ambiguous recovery metadata accepted")
			}
		})
	}
}
