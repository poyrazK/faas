package apidserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectPlacementCompose is a cut-down Docker example-voting-app: two image
// services that read Redis through REDIS_URL, so the apply provisions a
// managed cache named "voting-redis" after reconcile has inserted the apps.
const projectPlacementCompose = `services:
  vote:
    image: ghcr.io/example/vote:v1
    ports: ["8080:8080"]
    environment:
      REDIS_URL: redis://redis:6379
  worker:
    image: ghcr.io/example/worker:v1
    ports: ["8081:8080"]
    environment:
      REDIS_URL: redis://redis:6379
  redis:
    image: redis:7-alpine
`

// TestProjectApplyFailureAfterReconcileStillPlacesAddedApps pins the
// 2026-11-25 prod incident (project 8624ac80 on bhs-1): reconcile committed
// three apps, a later step failed, and the handler returned before sending
// kind=created. schedd claims a node only on that event, so the apps kept
// apps.node_id NULL and every gateway wake failed until schedd restarted.
// A failure after reconcile must still send kind=created for every app it
// inserted; the dry-run scan must send nothing.
func TestProjectApplyFailureAfterReconcileStillPlacesAddedApps(t *testing.T) {
	for _, tc := range []struct {
		name        string
		path        string
		wantStatus  int
		wantCreated bool
	}{
		{name: "apply fails at compose redis name taken", path: "/v1/projects", wantStatus: http.StatusConflict, wantCreated: true},
		{name: "dry-run scan", path: "/v1/projects/scan", wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spool := t.TempDir()
			t.Setenv("FAAS_SPOOL_ROOT", spool)
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
			e := setupSecrets(t, api.PlanPro)
			notif := &capturingNotifier{}
			e.s.notif = notif
			// Another account already owns the name the compose cache needs,
			// so creating it fails with "Name taken" after reconcile.
			other, err := e.store.CreateAccount(t.Context(), "squatter@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "voting-redis", RAMMB: 256, MaxConcurrency: 1}); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, projectPlacementRequest(t, e.key, tc.path))
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s = %d %s; want %d", tc.path, rec.Code, rec.Body.String(), tc.wantStatus)
			}

			type notice struct {
				Kind      string `json:"kind"`
				AppID     string `json:"app_id"`
				ProjectID string `json:"project_id"`
			}
			var created []notice
			notif.mu.Lock()
			for _, n := range notif.emitted {
				if n.Channel != db.NotifyAppChanged {
					continue
				}
				var got notice
				if err := json.Unmarshal([]byte(n.Payload), &got); err != nil {
					t.Fatalf("app_changed payload %q: %v", n.Payload, err)
				}
				if got.Kind == "created" {
					created = append(created, got)
				}
			}
			notif.mu.Unlock()

			project, err := e.store.ProjectBySlug(t.Context(), e.acct.ID, "voting")
			if !tc.wantCreated {
				if err == nil || len(notif.emitted) != 0 {
					t.Fatalf("dry-run wrote: project err=%v, notifications=%v", err, notif.emitted)
				}
				return
			}
			if err != nil {
				t.Fatalf("project after failed apply: %v", err)
			}
			apps, err := e.store.AppsForProject(t.Context(), e.acct.ID, project.ID)
			if err != nil {
				t.Fatal(err)
			}
			var wantIDs, gotIDs []string
			for _, a := range apps {
				if a.NodeID != "" {
					t.Fatalf("app %s already placed on %q; the test needs it unplaced", a.Slug, a.NodeID)
				}
				wantIDs = append(wantIDs, a.ID)
			}
			for _, n := range created {
				if n.ProjectID != project.ID {
					t.Fatalf("created notice %+v; want project_id %s", n, project.ID)
				}
				gotIDs = append(gotIDs, n.AppID)
			}
			slices.Sort(wantIDs)
			slices.Sort(gotIDs)
			if len(wantIDs) != 2 || !slices.Equal(gotIDs, wantIDs) {
				t.Fatalf("kind=created for %v; want one per inserted app %v (body %s)", gotIDs, wantIDs, rec.Body.String())
			}
		})
	}
}

func projectPlacementRequest(t *testing.T, key, path string) *http.Request {
	t.Helper()
	var src bytes.Buffer
	gz := gzip.NewWriter(&src)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "compose.yaml", Mode: 0o644, Size: int64(len(projectPlacementCompose)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(projectPlacementCompose)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("project_slug", "voting"); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile("source", "project.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(src.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}
