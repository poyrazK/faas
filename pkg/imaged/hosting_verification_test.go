// adr: 433 — the effective readiness contract governs deployment cutover.
package imaged

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingVerificationContractControlsCutover(t *testing.T) {
	for _, tc := range []struct {
		name, healthPath string
		status           int
		verified         bool
	}{
		{"root not found", "", 404, true},
		{"root auth required", "", 401, true},
		{"explicit health good", "/health", 200, true},
		{"explicit root is health", "/", 404, false},
		{"explicit health missing", "/health", 404, false},
		{"explicit health unavailable", "/health", 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, _ := store.CreateAccount(ctx, "hosting@example.test", "pro")
			app, _ := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "api", RAMMB: 256, IdleTimeoutS: 60})
			previous, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:previous", Scope: state.DefaultEnvScope, Status: state.DeployLive})
			var override json.RawMessage
			if tc.healthPath != "" {
				override = json.RawMessage(`{"path":"` + tc.healthPath + `"}`)
			}
			candidate, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: state.DefaultEnvScope, OverrideHealthcheck: override})
			_ = store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, "")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := tc.healthPath
				if path == "" {
					path = "/"
				}
				if r.URL.Path != path {
					t.Errorf("probe path=%s want=%s", r.URL.Path, path)
				}
				w.Header().Set(apihostingreceipt.ServedDeploymentHeader, candidate.ID)
				w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(candidate.ID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			verifier := apihostingreceipt.Verifier{BaseURL: srv.URL, Authorize: func(context.Context, string, string, time.Time) error { return nil }}
			handler := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmokeRequired(true).WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
				return VerifyHostingDeployment(ctx, verifier, app, dep)
			})
			handler.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + candidate.ID + `","storage_key":"snap/` + candidate.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`})
			got, err := store.DeploymentByID(ctx, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := state.DeployFailed
			wantLive := previous.ID
			if tc.verified {
				wantStatus = state.DeployLive
				wantLive = candidate.ID
			}
			if got.Status != wantStatus {
				t.Fatalf("candidate=%s want=%s", got.Status, wantStatus)
			}
			live, err := store.LiveDeploymentForScope(ctx, app.ID, state.DefaultEnvScope)
			if err != nil || live.ID != wantLive {
				t.Fatalf("live=%s want=%s err=%v", live.ID, wantLive, err)
			}
			receipt, err := apihostingreceipt.Decode(got.APIHostingReceipt)
			if err != nil {
				t.Fatal(err)
			}
			verification := apihostingreceipt.VerificationHTTPHealth
			if tc.healthPath == "" {
				verification = apihostingreceipt.VerificationRouteConnectivity
			}
			if receipt.Smoke.Verification != verification || receipt.Smoke.StatusCode != tc.status || receipt.Smoke.Authentication != apihostingreceipt.AuthenticationPlatformChallenge {
				t.Fatalf("receipt=%+v", receipt.Smoke)
			}
		})
	}
}
