// adr: 609
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const workflowTestID = "11111111-1111-4111-8111-111111111111"
const workflowTestRun = "22222222-2222-4222-8222-222222222222"
const workflowTestCapability = "33333333-3333-4333-8333-333333333333"

type workflowFixtureAPI struct {
	mu                                             sync.Mutex
	uploads, lookups, identities, entered, crashes int
	cancelled, hold                                bool
	deadline                                       time.Time
	artifact                                       *api.OperationResultArtifact
}

func workflowRequest(t *testing.T, client *http.Client, endpoint, step, mode string, generation int) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/"+step, strings.NewReader(`{"mode":"`+mode+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(api.OperationIDHeader, workflowTestID)
	request.Header.Set(api.OperationExecutionKindHeader, "workflow")
	request.Header.Set(api.OperationWorkflowRunHeader, workflowTestRun)
	request.Header.Set(api.OperationWorkflowStepHeader, step)
	request.Header.Set(api.OperationWorkflowCapabilityHeader, workflowTestCapability)
	if generation == 1 {
		request.Header.Set(api.OperationGenerationHeader, "1")
		request.Header.Set(api.OperationAttemptHeader, "1")
	} else {
		request.Header.Set(api.OperationGenerationHeader, "2")
		request.Header.Set(api.OperationAttemptHeader, "2")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func newWorkflowFixtureTest(t *testing.T, state *workflowFixtureAPI) *httptest.Server {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer workload" {
			t.Error("missing workload bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/fixture/") {
			if strings.Contains(r.URL.Path, "/entered/") {
				state.entered++
				w.WriteHeader(http.StatusNoContent)
			} else if state.hold {
				w.WriteHeader(http.StatusAccepted)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		if r.Header.Get(api.OperationWorkflowCapabilityHeader) != workflowTestCapability || r.Header.Get(api.OperationWorkflowRunHeader) != workflowTestRun || r.Header.Get(api.InvocationIDHeader) != "" {
			t.Error("native proof changed")
		}
		if strings.HasSuffix(r.URL.Path, "/control") {
			bound := state.deadline
			if bound.IsZero() {
				bound = time.Now().Add(time.Minute)
			}
			_ = json.NewEncoder(w).Encode(api.OperationWorkflowControlResponse{OperationID: workflowTestID, WorkflowRunID: workflowTestRun, WorkflowStep: r.Header.Get(api.OperationWorkflowStepHeader), Generation: 1, Attempt: 1, ObservedAt: time.Now(), DeadlineAt: bound, LeaseExpiresAt: bound, CancellationRequested: state.cancelled})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/artifact-upload-receipts") {
			state.lookups++
			_ = json.NewEncoder(w).Encode(api.OperationWorkflowArtifactResponse{Available: state.artifact != nil, Artifact: state.artifact})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/artifact-uploads") {
			state.uploads++
			if state.uploads > 1 {
				t.Error("fixture repeated a transfer")
			}
			state.artifact = &api.OperationResultArtifact{ID: workflowTestID, Name: r.URL.Query().Get("name"), URI: "operation://" + workflowTestID + "/artifacts/" + workflowTestID, SizeBytes: int64(len(workflowFixtureCSV)), SHA256: r.URL.Query().Get("sha256")}
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusBadGateway, Title: "lost acknowledgement"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(backend.Close)
	fixture := workflowFixtureServer{version: "original", client: func(context.Context) (*api.Client, error) {
		state.mu.Lock()
		state.identities++
		state.mu.Unlock()
		return api.NewClient(backend.URL, "workload"), nil
	}, crash: func(code int) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if code != 73 {
			t.Error("unexpected process exit")
		}
		state.crashes++
	}}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	return server
}

func TestCustomerWorkflowFixtureReceiptAndApprovedResume(t *testing.T) {
	state := &workflowFixtureAPI{}
	server := newWorkflowFixtureTest(t, state)
	for _, step := range []string{"collect", "transform", "finish"} {
		response := workflowRequest(t, server.Client(), server.URL, step, "complete", 1)
		if response.StatusCode != http.StatusOK {
			t.Fatal("fixture action failed")
		}
	}
	response := workflowRequest(t, server.Client(), server.URL, "finish", "complete", 2)
	var result struct {
		Rows       int    `json:"rows"`
		ArtifactID string `json:"artifact_id"`
		Version    string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Rows != 1 || result.ArtifactID != workflowTestID || result.Version != "original" {
		t.Fatal("typed native fixture result changed", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.uploads != 1 || state.lookups != 3 || state.entered != 4 || state.identities < 8 {
		t.Fatal("lost reply/resume did not reuse the receipt with fresh identities")
	}
}

func TestCustomerWorkflowFixtureProcessDeathAfterRetention(t *testing.T) {
	state := &workflowFixtureAPI{}
	server := newWorkflowFixtureTest(t, state)
	response := workflowRequest(t, server.Client(), server.URL, "finish", "crash", 1)
	if response.StatusCode != http.StatusConflict {
		t.Fatal("crash injection returned a successful result")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.crashes != 1 || state.artifact == nil || state.uploads != 1 {
		t.Fatal("process death did not occur after private retention")
	}
}

func TestCustomerWorkflowFixtureCancellationAndDeadline(t *testing.T) {
	for _, kind := range []string{"cancelled", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			state := &workflowFixtureAPI{cancelled: kind == "cancelled", hold: true}
			if kind == "deadline" {
				state.deadline = time.Now().Add(200 * time.Millisecond)
			}
			server := newWorkflowFixtureTest(t, state)
			response := workflowRequest(t, server.Client(), server.URL, "finish", "hold", 1)
			if response.StatusCode != http.StatusConflict {
				t.Fatal("stopped fixture returned business success")
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if kind == "cancelled" && (state.uploads != 0 || state.entered != 0) {
				t.Fatal("cancelled action entered business code")
			}
			if kind == "deadline" && state.uploads != 1 {
				t.Fatal("deadline did not interrupt a pending retained-file result")
			}
		})
	}
}

func TestCustomerWorkflowFixtureRequiresNativeProof(t *testing.T) {
	state := &workflowFixtureAPI{}
	server := newWorkflowFixtureTest(t, state)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/finish", strings.NewReader(`{"mode":"complete"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("untrusted request accepted")
	}
	response, err = server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("health needed native proof")
	}
}

func TestCustomerWorkflowFixtureUsesLoopbackIdentity(t *testing.T) {
	var calls int
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("audience") != "gregale:operations" {
			t.Error("wrong native audience")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "host-minted-token"})
	}))
	defer identity.Close()
	t.Setenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT", identity.URL+"/oidc/token")
	for i := 0; i < 2; i++ {
		client, err := nativeWorkflowFixtureClient(t.Context())
		if err != nil || client.Token() != "host-minted-token" {
			t.Fatal("guest workload exchange failed", err)
		}
	}
	if calls != 2 {
		t.Fatal("workload identity was not refreshed")
	}
	t.Setenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT", "https://identity.example.test/oidc/token")
	if _, err := nativeWorkflowFixtureClient(t.Context()); err == nil {
		t.Fatal("nonloopback identity endpoint accepted")
	}
}
