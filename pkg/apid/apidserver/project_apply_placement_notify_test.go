package apidserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
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
// inserted, through the API and the dashboard preview apply alike; the
// dry-run scan must send nothing.
func TestProjectApplyFailureAfterReconcileStillPlacesAddedApps(t *testing.T) {
	apiCall := func(path string) func(*testing.T, testEnv) *httptest.ResponseRecorder {
		return func(t *testing.T, e testEnv) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, projectPlacementRequest(t, e.key, path))
			return rec
		}
	}
	for _, tc := range []struct {
		name        string
		do          func(*testing.T, testEnv) *httptest.ResponseRecorder
		wantStatus  int
		wantCreated bool
	}{
		{name: "apply fails at compose redis name taken", do: apiCall("/v1/projects"), wantStatus: http.StatusConflict, wantCreated: true},
		// The page re-renders with the problem, so the status is 200.
		{name: "dashboard apply fails at compose redis name taken", do: dashboardPreviewApply, wantStatus: http.StatusOK, wantCreated: true},
		{name: "dry-run scan", do: apiCall("/v1/projects/scan"), wantStatus: http.StatusOK},
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

			rec := tc.do(t, e)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d %s; want %d", rec.Code, rec.Body.String(), tc.wantStatus)
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

// dashboardPreviewApply scans through the API (which caches the source
// and sends nothing), then applies the plan the way the dashboard form
// does, with an apply CSRF envelope bound to the account.
func dashboardPreviewApply(t *testing.T, e testEnv) *httptest.ResponseRecorder {
	t.Helper()
	scan := httptest.NewRecorder()
	e.h.ServeHTTP(scan, projectPlacementRequest(t, e.key, "/v1/projects/scan"))
	if scan.Code != http.StatusOK {
		t.Fatalf("scan = %d %s", scan.Code, scan.Body.String())
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil || plan.PlanToken == "" {
		t.Fatalf("scan plan token: %v (%s)", err, scan.Body.String())
	}
	tok, err := middleware.IssueForAuthenticated(e.s.sessions, projectPreviewApplyAction, e.acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"plan_token": {plan.PlanToken}, middleware.FormFieldName: {tok}}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/projects/voting/preview/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
	rec := httptest.NewRecorder()
	e.s.applyProjectPreview(rec, req, e.s.log, e.acct, "voting")
	return rec
}

// cancelAwareNotifier refuses a notification whose context is done, as a
// real pg_notify on that context would.
type cancelAwareNotifier struct{ *capturingNotifier }

func (n cancelAwareNotifier) Notify(ctx context.Context, channel, payload string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return n.capturingNotifier.Notify(ctx, channel, payload)
}

// An apply can fail because its own context expired (ProjectApplyTimeout,
// the migration deadline) or the client went away. The rows reconcile
// committed still need their kind=created, so the notifications must not
// use that context.
func TestNotifyProjectAppsAppliedOutlivesRequestContext(t *testing.T) {
	e := setupSecrets(t, api.PlanPro)
	notif := cancelAwareNotifier{&capturingNotifier{}}
	e.s.notif = notif
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e.s.notifyProjectAppsApplied(ctx, "project-1", []state.App{{ID: "app-a"}, {ID: "app-b"}}, nil)
	if got := len(notif.emitted); got != 2 {
		t.Fatalf("sent %d notifications after the request context ended, want 2: %v", got, notif.emitted)
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
