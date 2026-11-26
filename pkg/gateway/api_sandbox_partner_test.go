// adr: 120 — API keys are scoped to one app and its consumer.
package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testSandboxPartnerStore struct {
	*fakeConsumerAuthStore
	active bool
	record chan string
}

func (s *testSandboxPartnerStore) APISandboxPartnerKeyActive(_ context.Context, appID, keyID string) (bool, error) {
	return s.active && appID == s.key.AppID && keyID == s.key.ID, nil
}

func (s *testSandboxPartnerStore) RecordAPISandboxPartnerSuccess(_ context.Context, appID, keyID string, _ time.Time) error {
	s.record <- appID + "/" + keyID
	return nil
}

func TestAPISandboxPartnerKeyGateAndSuccessfulProxyResponse(t *testing.T) {
	fixture, token := consumerAuthFixture()
	store := &testSandboxPartnerStore{fakeConsumerAuthStore: fixture, record: make(chan string, 4)}
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	app := App{ID: fixture.key.AppID, AccountID: fixture.key.AccountID, IsAPISandbox: true, APISandboxPartner: true, ConsumerAuthMode: api.ConsumerAuthModeRequired}
	gate := func() (*statusRecorder, bool) {
		req := httptest.NewRequest(http.MethodGet, "https://sandbox.example.test/notes", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		rec := &statusRecorder{ResponseWriter: w, request: req}
		return rec, h.enforceConsumerAuth(w, req, rec, app)
	}
	if _, ok := gate(); ok {
		t.Fatal("private copy allowed a generic consumer credential")
	}
	fixture.consumer.ExternalRef = api.APISandboxPartnerConsumerRef
	if _, ok := gate(); ok {
		t.Fatal("private copy allowed an unbound partner credential")
	}
	fixture.consumer.ExternalRef = api.APISandboxVerificationConsumerRef
	fixture.key.Scopes = []string{"read"}
	rec, ok := gate()
	if !ok || rec.sandboxPartnerKeyID != "" {
		t.Fatal("readiness probe was refused or counted as partner activity")
	}
	fixture.consumer.ExternalRef = api.APISandboxPartnerConsumerRef
	store.active = true
	rec, ok = gate()
	if !ok || rec.sandboxPartnerKeyID != fixture.key.ID {
		t.Fatal("active partner credential could not access its private copy")
	}
	rec.wroteHeader, rec.status = true, http.StatusNotFound
	h.recordSandboxPartnerSuccess(app.ID, rec)
	rec.status, rec.platformResponse = http.StatusOK, true
	h.recordSandboxPartnerSuccess(app.ID, rec)
	rec.platformResponse = false
	h.recordSandboxPartnerSuccess(app.ID, rec)
	h.recordSandboxPartnerSuccess(app.ID, rec)
	select {
	case got := <-store.record:
		if got != app.ID+"/"+fixture.key.ID {
			t.Fatalf("wrong success attribution: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no successful partner request recorded")
	}
	select {
	case got := <-store.record:
		t.Fatalf("duplicate or unsuccessful request recorded: %s", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAPISandboxRunRestrictsGuestCredentials(t *testing.T) {
	fixture, token := consumerAuthFixture()
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(fixture)
	app := App{ID: fixture.key.AppID, AccountID: fixture.key.AccountID, IsAPISandbox: true, APISandboxRun: true, ConsumerAuthMode: api.ConsumerAuthModeRequired}
	for _, test := range []struct {
		ref    string
		method string
		scopes []string
		allow  bool
	}{
		{"gregale-api-sandbox-invites", http.MethodGet, []string{"read", "write"}, false},
		{api.APISandboxPartnerConsumerRef, http.MethodGet, []string{"read", "write"}, false},
		{api.APISandboxRunConsumerRef, http.MethodPost, []string{"read", "write"}, true},
		{api.APISandboxVerificationConsumerRef, http.MethodGet, []string{"read"}, true},
		{api.APISandboxVerificationConsumerRef, http.MethodPost, []string{"read"}, false},
	} {
		fixture.consumer.ExternalRef, fixture.key.Scopes = test.ref, test.scopes
		_, _, allowed := runConsumerAuthGate(t, h, app, test.method, token)
		if allowed != test.allow {
			t.Errorf("ref %q and method %s allowed=%v, want %v", test.ref, test.method, allowed, test.allow)
		}
	}
}

type testSandboxPartnerRunStore struct {
	*testSandboxPartnerStore
	checkpoints []api.APISandboxPartnerStep
}

func (s *testSandboxPartnerRunStore) ClaimAPISandboxPartnerStep(_ context.Context, app, deployment, key, method, target string, _ time.Time) (api.APISandboxPartnerStep, error) {
	if key != s.key.ID || app != s.key.AppID || deployment != "revision" || method != "POST" || target != "/refunds?fixture=1" {
		return api.APISandboxPartnerStep{}, ErrConsumerAuthNotFound
	}
	return api.APISandboxPartnerStep{RunID: "run", Step: api.APISandboxRunStep{ExpectedStatus: http.StatusCreated}}, nil
}

func (s *testSandboxPartnerRunStore) CompleteAPISandboxPartnerStep(_ context.Context, step api.APISandboxPartnerStep, _ time.Time) error {
	s.checkpoints = append(s.checkpoints, step)
	return nil
}

func TestAPISandboxPartnerRunCheckpointsOnlyPinnedGuestResponses(t *testing.T) {
	fixture, _ := consumerAuthFixture()
	store := &testSandboxPartnerRunStore{testSandboxPartnerStore: &testSandboxPartnerStore{fakeConsumerAuthStore: fixture}}
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	app := App{ID: fixture.key.AppID}
	target := Target{DeploymentID: "revision"}
	for _, tt := range []struct {
		name, path, revision, platformCode string
		status, wantStatus                 int
		wantFailure                        string
	}{
		{name: "out of order", path: "/next", status: 409},
		{name: "pinned guest", path: "/refunds?fixture=1", revision: "revision", status: 201, wantStatus: 201},
		{name: "unexpected status", path: "/refunds?fixture=1", revision: "revision", status: 422, wantStatus: 422, wantFailure: "unexpected_http_status"},
		{name: "platform failure", path: "/refunds?fixture=1", revision: "revision", status: 503, platformCode: "capacity", wantFailure: "request_unavailable"},
		{name: "missing pin", path: "/refunds?fixture=1", status: 201, wantFailure: "deployment_mismatch"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(store.checkpoints)
			r := httptest.NewRequest(http.MethodPost, "https://sandbox.example.test"+tt.path, nil)
			r.Header.Set(api.RequestIDHeader, "private-caller-text")
			w := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: w, request: r, sandboxPartnerKeyID: fixture.key.ID}
			if !h.claimSandboxPartnerStep(rec, r, rec, app, target) {
				if w.Code != tt.status || tt.status != 409 || len(store.checkpoints) != before {
					t.Fatalf("unexpected reservation rejection: %d", w.Code)
				}
				return
			}
			rec.Header().Set(api.RevisionHeader, tt.revision)
			rec.Header().Set(api.ErrorCodeHeader, tt.platformCode)
			rec.WriteHeader(tt.status)
			if len(store.checkpoints) != before+1 || w.Code != tt.status {
				t.Fatalf("unexpected checkpoint count/status: %d / %d", len(store.checkpoints)-before, w.Code)
			}
			step := store.checkpoints[before].Step
			if step.HTTPStatus != tt.wantStatus || step.FailureCode != tt.wantFailure || step.RequestID != "" {
				t.Fatalf("unexpected checkpoint: %+v", step)
			}
		})
	}
}

func TestAPISandboxPartnerRunGatesActualGatewayDispatch(t *testing.T) {
	fixture, token := consumerAuthFixture()
	fixture.consumer.ExternalRef = api.APISandboxPartnerConsumerRef
	store := &testSandboxPartnerRunStore{testSandboxPartnerStore: &testSandboxPartnerStore{
		fakeConsumerAuthStore: fixture, active: true, record: make(chan string, 1),
	}}
	backend := &fakeBackend{
		host: "sandbox.example",
		app: App{ID: fixture.key.AppID, AccountID: fixture.key.AccountID, Plan: api.PlanPro,
			IsPreview: true, IsAPISandbox: true, APISandboxRun: true, APISandboxPartner: true, ConsumerAuthMode: api.ConsumerAuthModeRequired},
	}
	backend.AddTarget(Target{AppID: backend.app.ID, DeploymentID: "revision", InstanceID: "guest", NodeID: "node"})
	forwarded := 0
	h := NewHandlerWith(backend, NewMetrics(), slog.New(slog.DiscardHandler)).WithConsumerAuth(store).WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwarded++
			if r.Header.Get("Authorization") != "" {
				t.Error("partner key reached the guest")
			}
			w.WriteHeader(http.StatusCreated)
		})
	})
	send := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://sandbox.example"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if response := send("/unplanned"); response.Code != http.StatusConflict || forwarded != 0 {
		t.Fatalf("unplanned request reached the guest: status=%d forwarded=%d", response.Code, forwarded)
	}
	response := send("/refunds?fixture=1")
	if response.Code != http.StatusCreated || forwarded != 1 || len(store.checkpoints) != 1 {
		t.Fatalf("planned request: status=%d forwarded=%d checkpoints=%d", response.Code, forwarded, len(store.checkpoints))
	}
	if step := store.checkpoints[0].Step; step.HTTPStatus != http.StatusCreated || step.FailureCode != "" ||
		step.RequestID == "" || step.RequestID != response.Header().Get(api.RequestIDHeader) {
		t.Fatalf("gateway recorded the wrong guest response: %+v", step)
	}
}
