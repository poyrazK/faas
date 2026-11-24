package imaged

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r31: the deploy check sent GET / to queue and cron functions,
// running the handler on a message nobody sent and a cron job outside its
// schedule. A function's route check asks its runner's own /healthz and keeps
// the lenient route rules (a proven 4xx still shows the candidate is routed).
func TestHostingCheckNeverRunsAFunctionHandler(t *testing.T) {
	for _, tc := range []struct {
		name     string
		app      state.App
		dep      state.Deployment
		status   int
		wantPath string
		wantOK   bool
	}{
		{"queue function", state.App{Slug: "fn", Type: state.AppTypeFunction}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball}, http.StatusOK, defaultHealthzPath, true},
		{"seeded healthz", state.App{Slug: "fn", Type: state.AppTypeFunction, Manifest: state.AppManifest{Healthz: defaultHealthzPath}}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball}, http.StatusOK, defaultHealthzPath, true},
		{"edge answers 404 for healthz", state.App{Slug: "fn", Type: state.AppTypeFunction}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball}, http.StatusNotFound, defaultHealthzPath, true},
		{"own health path is strict", state.App{Slug: "fn", Type: state.AppTypeFunction, Manifest: state.AppManifest{Healthz: "/ready"}}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball}, http.StatusOK, "/ready", true},
		{"workflow function keeps GET /", state.App{Slug: "fn", Type: state.AppTypeFunction}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball, Workflows: json.RawMessage(`[{"name":"w","code":{"path":"/"}}]`)}, http.StatusMethodNotAllowed, "/", true},
		{"image function keeps strict healthz", state.App{Slug: "fn", Type: state.AppTypeFunction}, state.Deployment{ID: "d1", Kind: state.DeploymentKindImage}, http.StatusNotFound, defaultHealthzPath, false},
		{"configured healthz stays strict", state.App{Slug: "fn", Type: state.AppTypeFunction}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball, OverrideHealthcheck: json.RawMessage(`{"path":"/healthz"}`)}, http.StatusNotFound, defaultHealthzPath, false},
		{"http app keeps its health path", state.App{Slug: "web", Type: state.AppTypeApp, Manifest: state.AppManifest{Healthz: "/ready"}}, state.Deployment{ID: "d1", Kind: state.DeploymentKindTarball}, http.StatusOK, "/ready", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				w.Header().Set(apihostingreceipt.ServedDeploymentHeader, tc.dep.ID)
				w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(tc.dep.ID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			verifier := apihostingreceipt.Verifier{BaseURL: srv.URL, Client: srv.Client(), Timeout: 5 * time.Second, Authorize: func(context.Context, string, string, time.Time) error { return nil }}
			smoke, err := VerifyHostingDeployment(context.Background(), verifier, tc.app, tc.dep)
			mu.Lock()
			defer mu.Unlock()
			if len(paths) == 0 {
				t.Fatalf("no request sent (smoke=%+v err=%v)", smoke, err)
			}
			for _, p := range paths {
				if p != tc.wantPath {
					t.Fatalf("checked %q, want %q (all: %v)", p, tc.wantPath, paths)
				}
			}
			if ok := err == nil && smoke.Status == apihostingreceipt.SmokeVerified; ok != tc.wantOK {
				t.Fatalf("verified = %v, want %v (smoke=%+v err=%v)", ok, tc.wantOK, smoke, err)
			}
		})
	}
}
