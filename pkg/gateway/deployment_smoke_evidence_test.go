// adr: 433 — proof is authored only after a candidate upstream response.
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeploymentRouteSmokeAcceptsGuestRoot404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader) != "" {
			t.Error("challenge reached guest")
		}
		w.Header().Set(api.DeploymentIDHeader, "forged")
		w.Header().Set(apihostingreceipt.ServedResponseHeader, "forged")
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	fake := &fakeBackend{
		app:  App{ID: "app", AccountID: "account", Plan: api.PlanFree, RequireAuthn: true},
		host: "demo.apps.test", upstream: upstream.Listener.Addr().String(),
	}
	backend := &deploymentSmokeRoutingBackend{fakeBackend: fake, deploymentID: "candidate"}
	backend.resolved = Target{NodeID: fake.upstream, InstanceID: "instance", DeploymentID: "candidate"}
	handler := NewHandlerWith(backend, NewMetrics(), nil)
	edge := httptest.NewServer(handler)
	defer edge.Close()
	verifier := apihostingreceipt.Verifier{
		BaseURL: edge.URL, AppsDomain: "apps.test",
		Authorize: func(_ context.Context, id, token string, _ time.Time) error { backend.token = token; return nil },
	}
	got, err := verifier.VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	if err != nil || got.Status != apihostingreceipt.SmokeVerified || got.StatusCode != 404 || got.DeploymentID != "candidate" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestBridgeSmokeProofRequiresUpstreamHeadersAndAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		authorized, answered bool
	}{
		{"candidate response", true, true}, {"bridge unavailable", true, false}, {"ordinary guest forgery", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &failAfterFramesStream{err: io.EOF}
			if tc.answered {
				stream.frames = []*vmmdpb.ForwardHTTPStreamResponse{{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
					Status: 404, Headers: []*vmmdpb.Header{{Name: apihostingreceipt.ServedResponseHeader, Value: "forged"}},
				}}}}
			} else {
				stream.err = status.Error(codes.Unavailable, "bridge unavailable")
			}
			request := httptest.NewRequest(http.MethodGet, "http://demo/", nil)
			request.Header.Set("x-faas-instance", "instance")
			if tc.authorized {
				request = request.WithContext(withDeploymentSmokeResponse(request.Context(), "candidate", "challenge"))
			}
			recorder := httptest.NewRecorder()
			ForwardingReverseProxy(singleClientLookup{cli: &failAfterFramesClient{stream: stream}}, nil)(Target{NodeID: "node", InstanceID: "instance"}).ServeHTTP(recorder, request)
			wantProof := ""
			if tc.authorized && tc.answered {
				wantProof = apihostingreceipt.CandidateResponseProof("candidate", "challenge")
			}
			if recorder.Header().Get(apihostingreceipt.ServedResponseHeader) != wantProof {
				t.Fatalf("code=%d headers=%v", recorder.Code, recorder.Header())
			}
		})
	}
}

func TestGuestCannotForgeSmokeResponseProof(t *testing.T) {
	headers := make(http.Header)
	forwardedResponseHeader(context.Background(), headers, apihostingreceipt.ServedResponseHeader, "forged")
	if headers.Get(apihostingreceipt.ServedResponseHeader) != "" {
		t.Fatal("bridge forwarded guest proof")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(api.DeploymentIDHeader, "forged")
		w.Header().Set(apihostingreceipt.ServedResponseHeader, "forged")
		w.WriteHeader(404)
	}))
	defer upstream.Close()
	request := httptest.NewRequest(http.MethodGet, upstream.URL, nil)
	request.Header.Set(apihostingreceipt.ServedResponseHeader, "forged")
	recorder := httptest.NewRecorder()
	defaultProxy(upstream.Listener.Addr().String(), 0).ServeHTTP(recorder, request)
	if recorder.Header().Get(apihostingreceipt.ServedResponseHeader) != "" || recorder.Header().Get(api.DeploymentIDHeader) != "" {
		t.Fatal("reverse proxy forwarded guest proof")
	}
}

func TestUnavailableUpstreamDoesNotProveSmokeResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := upstream.Listener.Addr().String()
	upstream.Close()
	request := httptest.NewRequest(http.MethodGet, "http://demo/", nil)
	request = request.WithContext(withDeploymentSmokeResponse(request.Context(), "candidate", "challenge"))
	recorder := httptest.NewRecorder()
	defaultProxy(address, 0).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || recorder.Header().Get(apihostingreceipt.ServedResponseHeader) != "" {
		t.Fatalf("code=%d headers=%v", recorder.Code, recorder.Header())
	}
}

func TestCandidateSmokeCannotRetryOntoServingRevision(t *testing.T) {
	backend := &fakeBackend{}
	backend.AddTarget(Target{InstanceID: "serving", DeploymentID: "previous"})
	handler := NewHandlerWith(backend, NewMetrics(), nil).WithRetryEnabled(true).WithRetryDefault(testPolicy())
	request := httptest.NewRequest(http.MethodGet, "http://demo/", nil)
	request = request.WithContext(withDeploymentSmokeResponse(request.Context(), "candidate", "challenge"))
	recorder := httptest.NewRecorder()
	calls := 0
	handler.proxyAttempt(recorder, request, Target{InstanceID: "candidate-instance", DeploymentID: "candidate"}, false,
		func(Target) {}, func(w http.ResponseWriter, r *http.Request, target Target) {
			calls++
			if target.DeploymentID != "candidate" {
				t.Fatalf("candidate smoke replayed onto %s", target.DeploymentID)
			}
			deadTargetAttempt(w, r, target)
		}, App{ID: "app"})
	if calls != 1 || backend.pickCalls.Load() != 0 || recorder.Code != http.StatusBadGateway || recorder.Header().Get(apihostingreceipt.ServedResponseHeader) != "" {
		t.Fatalf("calls=%d picks=%d status=%d headers=%v", calls, backend.pickCalls.Load(), recorder.Code, recorder.Header())
	}
}
