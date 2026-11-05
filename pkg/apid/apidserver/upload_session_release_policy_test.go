package apidserver_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us rc.294: a plain `gregale deploy` of an app that already
// served traffic went straight to 100% with no canary and no first-wake 5xx
// rollback. The CLI deploys local source through a resumable upload session;
// its commit ran the ADR-1446 default on the request and then built the
// deployment from the saved upload options, dropping the canary, the
// rollback and a scheduled release's 0% traffic (Hunt 14 H14-9).
func TestUploadSessionCommitKeepsTheReleasePolicy(t *testing.T) {
	tests := []struct {
		name    string
		options api.UploadDeployOptions
		check   func(t *testing.T, d state.Deployment)
	}{
		{
			name: "safe default",
			check: func(t *testing.T, d state.Deployment) {
				if d.CanaryPreset != api.ReleasePolicyCanaryPreset(api.ReleasePolicySafe) || d.CanaryTotalSteps == 0 || d.TrafficPercent >= 100 {
					t.Errorf("rollout = preset %q steps %d traffic %d%%, want the safe canary's first stage", d.CanaryPreset, d.CanaryTotalSteps, d.TrafficPercent)
				}
				if !d.RollbackOn5xx {
					t.Error("rollback_on_5xx = false, want the safe default's first-wake 5xx rollback")
				}
			},
		},
		{
			name:    "scheduled release",
			options: api.UploadDeployOptions{ReleaseAt: time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)},
			check: func(t *testing.T, d state.Deployment) {
				if d.TrafficPercent != 0 {
					t.Errorf("scheduled release traffic = %d%%, want 0%% until its release time", d.TrafficPercent)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanPro)
			ctx := context.Background()
			app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "upload-policy"})
			if err != nil {
				t.Fatal(err)
			}
			live, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:live"})
			if err != nil {
				t.Fatal(err)
			}
			if err := (*e.Store()).MarkDeploymentLive(ctx, live.ID); err != nil {
				t.Fatal(err)
			}
			if err := (*e.Store()).StampSafeReleaseWorkerLease(ctx, time.Hour); err != nil {
				t.Fatal(err)
			}
			out := commitUploadSession(t, e, app.Slug, tt.options)
			d, err := (*e.Store()).DeploymentByID(ctx, out.ID)
			if err != nil {
				t.Fatalf("DeploymentByID: %v", err)
			}
			tt.check(t, d)
		})
	}
}

func commitUploadSession(t *testing.T, e apidserver.TestEnv, slug string, options api.UploadDeployOptions) api.DeploymentResponse {
	t.Helper()
	raw := apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{"index.js": []byte("console.log(1)\n")})
	body, err := json.Marshal(apidserver.StartUploadRequest{AppSlug: slug, TotalSize: int64(len(raw)), DeployOptions: &options})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var started apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if rec := appendChunk(t, e, started.UploadID, 0, raw); rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/uploads/"+started.UploadID+"/commit", nil)
	req.Header.Set("Authorization", "Bearer "+(*e.Key()))
	rec = httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusAccepted {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
