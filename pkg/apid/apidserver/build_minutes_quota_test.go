package apidserver_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261103131742
// A source deploy is refused with plan_limit_build_minutes once the
// account's builds have run its plan's monthly build minutes; below the
// quota it is accepted.
func TestSourceDeployRefusedPastMonthlyBuildMinutes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		usedMinutes int
		wantRefused bool
	}{
		{"below the Free quota", 299, false},
		{"at the Free quota", 300, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanFree)
			if rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "build-quota"}, nil); rec.Code != http.StatusCreated {
				t.Fatalf("create app: %d %s", rec.Code, rec.Body)
			}
			store := *e.Store()
			ctx := t.Context()
			app, err := store.AppBySlug(ctx, "build-quota")
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
			if err != nil {
				t.Fatal(err)
			}
			build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 1, "")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			if err := store.SetBuildTimesForTest(build.ID, month, month.Add(time.Duration(tc.usedMinutes)*time.Minute)); err != nil {
				t.Fatal(err)
			}

			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			src, err := mw.CreateFormFile("tarball", "src.tar.gz")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = src.Write(nodeSourceTarGz(t))
			if err := mw.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/v1/apps/build-quota/deployments/source-tarball", &body)
			req.Header.Set("Authorization", "Bearer "+*e.Key())
			req.Header.Set("Content-Type", mw.FormDataContentType())
			rec := httptest.NewRecorder()
			(*e.H()).ServeHTTP(rec, req)

			if !tc.wantRefused {
				if rec.Code < 200 || rec.Code > 299 {
					t.Fatalf("status = %d body = %s, want the deploy accepted", rec.Code, rec.Body)
				}
				return
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode %s: %v", rec.Body, err)
			}
			if rec.Code != http.StatusForbidden || problem.Code != api.CodePlanLimitBuildMinutes {
				t.Fatalf("status = %d code = %q, want 403 %s", rec.Code, problem.Code, api.CodePlanLimitBuildMinutes)
			}
			if problem.Limit == nil || *problem.Limit != 300 || problem.Observed == nil || *problem.Observed != 300 {
				t.Fatalf("limit/observed = %v/%v, want 300/300", problem.Limit, problem.Observed)
			}
			if latest, err := store.LatestDeployment(ctx, app.ID); err != nil || latest.ID != dep.ID {
				t.Fatalf("latest deployment = %s, %v; the refused deploy must not create one", latest.ID, err)
			}
		})
	}
}
