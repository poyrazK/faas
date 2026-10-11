package imaged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSelectedHostingContractRoutesUsesOnlyExplicitStaticGETs(t *testing.T) {
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"test","version":"1"},"paths":{
		"/v1/private":{"get":{"x-gregale-hosting-check":true,"responses":{"401":{"description":"auth required"}}}},
		"/v1/health":{"get":{"x-gregale-hosting-check":true,"responses":{"204":{"description":"ok"}}}},
		"/v1/unmarked":{"get":{"responses":{"200":{"description":"ok"}}}},
		"/v1/create":{"post":{"responses":{"201":{"description":"created"}}}}
	}}`)
	routes, err := selectedHostingContractRoutes(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []apihostingreceipt.APIRouteProbe{{Method: "GET", Path: "/v1/health"}, {Method: "GET", Path: "/v1/private"}}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes=%+v, want %+v", routes, want)
	}
}

func TestSelectedHostingContractRoutesRejectsUnsafeSelection(t *testing.T) {
	for name, paths := range map[string]string{
		"state changing": `"/v1/charge":{"post":{"x-gregale-hosting-check":true,"responses":{"200":{"description":"ok"}}}}`,
		"dynamic":        `"/v1/items/{id}":{"get":{"x-gregale-hosting-check":true,"responses":{"200":{"description":"ok"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := []byte(`{"openapi":"3.1.0","info":{"title":"test","version":"1"},"paths":{` + paths + `}}`)
			if _, err := selectedHostingContractRoutes(doc); err == nil {
				t.Fatal("unsafe selection accepted")
			}
		})
	}
}

func TestHostingContractChecksArePersistedBeforePromotion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		routeStatus int
		want        state.DeploymentStatus
		wantSet     string
	}{
		{name: "all checks pass", routeStatus: http.StatusUnauthorized, want: state.DeployLive, wantSet: apihostingreceipt.RouteCheckSetVerified},
		{name: "missing required route", routeStatus: http.StatusNotFound, want: state.DeployFailed, wantSet: apihostingreceipt.RouteCheckSetFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "hosting-contract@example.test", "pro")
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "contract-api", RAMMB: 256, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			previous, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:previous", Scope: state.DefaultEnvScope, Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: state.DefaultEnvScope})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			doc := []byte(`{"openapi":"3.1.0","info":{"title":"contract-api","version":"1"},"paths":{
				"/v1/health":{"get":{"x-gregale-hosting-check":true,"responses":{"204":{"description":"ready"}}}},
				"/v1/private":{"get":{"x-gregale-hosting-check":true,"responses":{"401":{"description":"auth required"}}}},
				"/v1/unmarked":{"get":{"responses":{"200":{"description":"not probed"}}}}
			}}`)
			if err := store.UpsertAppOpenAPIDoc(ctx, app.ID, account.ID, doc, 3, "3.1.0"); err != nil {
				t.Fatal(err)
			}

			probed := make([]string, 0, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probed = append(probed, r.Method+" "+r.URL.Path)
				status := http.StatusOK
				switch r.URL.Path {
				case "/v1/health":
					status = http.StatusNoContent
				case "/v1/private":
					status = tc.routeStatus
				}
				w.Header().Set(apihostingreceipt.ServedDeploymentHeader, candidate.ID)
				w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(candidate.ID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
				w.WriteHeader(status)
			}))
			defer server.Close()

			verifier := apihostingreceipt.Verifier{
				BaseURL: server.URL, Timeout: 2 * time.Second, RequestTimeout: time.Second,
				Authorize: func(context.Context, string, string, time.Time) error { return nil },
			}
			notifier := &fakeNotifier{}
			handler := New(store, notifier, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).
				WithHostingSmokeRequired(true).
				WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
					return VerifyHostingDeploymentWithContract(ctx, verifier, store, app, dep)
				})
			handler.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: fmt.Sprintf(`{"deployment_id":%q,"storage_key":"snap/%s/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`, candidate.ID, candidate.ID)})

			got, err := store.DeploymentByID(ctx, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want {
				t.Fatalf("candidate status=%s, want %s", got.Status, tc.want)
			}
			live, err := store.LiveDeploymentForScope(ctx, app.ID, state.DefaultEnvScope)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == state.DeployLive && live.ID != candidate.ID || tc.want == state.DeployFailed && live.ID != previous.ID {
				t.Fatalf("live deployment=%s, want candidate=%s previous=%s", live.ID, candidate.ID, previous.ID)
			}
			wantProbed := []string{"GET /", "GET /v1/health", "GET /v1/private"}
			if !reflect.DeepEqual(probed, wantProbed) {
				t.Fatalf("probed=%v, want %v", probed, wantProbed)
			}
			receipt, err := apihostingreceipt.Decode(got.APIHostingReceipt)
			if err != nil {
				t.Fatal(err)
			}
			checks := receipt.Smoke.RouteChecks
			if checks == nil || checks.Status != tc.wantSet || checks.Source != apihostingreceipt.RouteCheckSourceOpenAPI || len(checks.Checks) != 2 {
				t.Fatalf("route check receipt=%+v", checks)
			}
			digest := sha256.Sum256(doc)
			if checks.DocumentSHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("document hash=%q, want %x", checks.DocumentSHA256, digest)
			}
			if checks.Checks[0].Path != "/v1/health" || checks.Checks[0].Status != apihostingreceipt.SmokeVerified || checks.Checks[1].Path != "/v1/private" {
				t.Fatalf("per-route evidence=%+v", checks.Checks)
			}
			if tc.wantSet == apihostingreceipt.RouteCheckSetFailed && (checks.Checks[1].Status != apihostingreceipt.SmokeFailed || checks.Checks[1].StatusCode != http.StatusNotFound) {
				t.Fatalf("failed route evidence=%+v", checks.Checks[1])
			}
		})
	}
}
