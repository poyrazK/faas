// adr: 521
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

const httpTestID = "44444444-4444-4444-8444-444444444444"
const httpTestInvocation = "55555555-5555-4555-8555-555555555555"
const httpTestCapability = "6666666666666666666666666666666666666666666666666666666666666666"

type httpFixtureAPI struct {
	mu                                             sync.Mutex
	uploads, lookups, identities, entered, crashes int
	stages                                         []string
	directive                                      int
	cancelled                                      bool
	artifact                                       *api.OperationResultArtifact
	children                                       []api.OperationChildRequest
}

func httpRequest(t *testing.T, client *http.Client, endpoint, mode string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/exports", strings.NewReader(`{"mode":"`+mode+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(api.OperationIDHeader, httpTestID)
	request.Header.Set(api.InvocationIDHeader, httpTestInvocation)
	request.Header.Set(api.OperationAttemptHeader, "1")
	request.Header.Set(api.OperationCapabilityHeader, httpTestCapability)
	if mutate != nil {
		mutate(request)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func newHTTPFixtureTest(t *testing.T, state *httpFixtureAPI) *httptest.Server {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer workload" {
			t.Error("missing workload bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/fixture/") {
			if !strings.HasSuffix(r.URL.Path, "/"+httpTestID+"/"+httpTestInvocation) {
				t.Error("barrier lost the operation claim identity")
			}
			if strings.Contains(r.URL.Path, "/entered/") {
				state.entered++
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(state.directive)
			return
		}
		if r.Header.Get(api.OperationCapabilityHeader) != httpTestCapability || r.Header.Get(api.InvocationIDHeader) != httpTestInvocation || r.Header.Get(api.OperationAttemptHeader) != "1" {
			t.Error("HTTP claim proof changed")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/control"):
			now := time.Now()
			_ = json.NewEncoder(w).Encode(api.OperationExecutionControlResponse{OperationID: httpTestID, InvocationID: httpTestInvocation, Attempt: 1, ObservedAt: now, DeadlineAt: now.Add(time.Minute), LeaseExpiresAt: now.Add(10 * time.Second), CancellationRequested: state.cancelled})
		case strings.HasSuffix(r.URL.Path, "/progress"):
			var report api.OperationReportRequest
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil || report.ReportID != report.Stage {
				t.Error("progress report lost its stable identity")
			}
			state.stages = append(state.stages, report.Stage)
			_ = json.NewEncoder(w).Encode(api.OperationResponse{ID: httpTestID})
		case strings.HasSuffix(r.URL.Path, "/children"):
			var child api.OperationChildRequest
			if err := json.NewDecoder(r.Body).Decode(&child); err != nil {
				t.Error("child start body", err)
			}
			state.children = append(state.children, child)
			status := http.StatusCreated
			if len(state.children) > 1 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(api.OperationChildStartResponse{OperationChild: api.OperationChild{Key: child.Key, Operation: api.OperationResponse{ID: "77777777-7777-4777-8777-777777777777"}}, Replayed: status == http.StatusOK})
		case strings.HasSuffix(r.URL.Path, "/artifact-upload-receipts"):
			state.lookups++
			_ = json.NewEncoder(w).Encode(api.OperationArtifactUploadResponse{Available: state.artifact != nil, Artifact: state.artifact})
		case strings.HasSuffix(r.URL.Path, "/artifact-uploads"):
			state.uploads++
			if state.uploads > 1 {
				t.Error("fixture repeated a transfer")
			}
			state.artifact = &api.OperationResultArtifact{ID: httpTestID, Name: r.URL.Query().Get("name"), URI: "operation://" + httpTestID + "/artifacts/" + httpTestID, SizeBytes: int64(len(httpFixtureCSV)), SHA256: r.URL.Query().Get("sha256")}
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusBadGateway, Title: "lost acknowledgement"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	fixture := httpFixtureServer{version: "original", client: func(context.Context) (*api.Client, error) {
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

func TestCustomerHTTPFixtureLostAcknowledgementUsesReceipt(t *testing.T) {
	state := &httpFixtureAPI{directive: http.StatusNoContent}
	server := newHTTPFixtureTest(t, state)
	response := httpRequest(t, server.Client(), server.URL, "hold", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal("released fixture handler failed")
	}
	var result struct {
		Rows       int    `json:"rows"`
		ArtifactID string `json:"artifact_id"`
		Version    string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Rows != 1 || result.ArtifactID != httpTestID || result.Version != "original" {
		t.Fatal("typed HTTP fixture result changed", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.uploads != 1 || state.lookups != 2 || state.entered != 1 || strings.Join(state.stages, ",") != "generating,uploading" {
		t.Fatal("lost reply was not resolved through a receipt with ordered progress")
	}
}

func TestCustomerHTTPFixtureProcessDeathAfterRetention(t *testing.T) {
	state := &httpFixtureAPI{directive: http.StatusResetContent}
	server := newHTTPFixtureTest(t, state)
	response := httpRequest(t, server.Client(), server.URL, "hold", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatal("crash injection returned a successful result")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.crashes != 1 || state.artifact == nil || state.uploads != 1 {
		t.Fatal("process death did not occur after private retention")
	}
}

func TestCustomerHTTPFixtureObservesCancellation(t *testing.T) {
	state := &httpFixtureAPI{directive: http.StatusAccepted, cancelled: true}
	server := newHTTPFixtureTest(t, state)
	response := httpRequest(t, server.Client(), server.URL, "hold", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatal("cancelled fixture returned business success")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.uploads != 0 || state.entered != 0 {
		t.Fatal("cancelled handler entered business code")
	}
}

func TestCustomerHTTPFixtureRejectsUntrustedClaims(t *testing.T) {
	for name, mutate := range map[string]func(*http.Request){
		"missing claim":  func(r *http.Request) { r.Header.Del(api.OperationCapabilityHeader) },
		"short claim":    func(r *http.Request) { r.Header.Set(api.OperationCapabilityHeader, "abc") },
		"zero attempt":   func(r *http.Request) { r.Header.Set(api.OperationAttemptHeader, "0") },
		"native context": func(r *http.Request) { r.Header.Set(api.OperationExecutionKindHeader, "workflow") },
	} {
		t.Run(name, func(t *testing.T) {
			state := &httpFixtureAPI{directive: http.StatusNoContent}
			server := newHTTPFixtureTest(t, state)
			if response := httpRequest(t, server.Client(), server.URL, "complete", mutate); response.StatusCode != http.StatusBadRequest {
				t.Fatal("untrusted request accepted")
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.identities != 0 {
				t.Fatal("untrusted request obtained workload identity")
			}
		})
	}
}

// ADR-20261017102231 Stage 1: a webhook-submitted Operation carries the provider
// envelope and completes without a relay directive; the bounded definition
// shares the handler; any other path is not an operation route.
func TestCustomerHTTPFixtureRoutesWebhookAndBoundedDefinitions(t *testing.T) {
	state := &httpFixtureAPI{directive: http.StatusAccepted} // the relay would hold forever
	server := newHTTPFixtureTest(t, state)
	envelope := `{"webhook":{"endpoint":"stripe","provider":"stripe","event_id":"evt_1","event_type":"invoice.paid"},"payload":{"id":"evt_1"}}`
	post := func(path, body string) *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(api.OperationIDHeader, httpTestID)
		request.Header.Set(api.InvocationIDHeader, httpTestInvocation)
		request.Header.Set(api.OperationAttemptHeader, "1")
		request.Header.Set(api.OperationCapabilityHeader, httpTestCapability)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	response := post("/exports/webhook", envelope)
	var output map[string]any
	if err := json.NewDecoder(response.Body).Decode(&output); err != nil || response.StatusCode != http.StatusOK || output["rows"] != float64(1) {
		t.Fatalf("webhook export HTTP %d %v %v", response.StatusCode, output, err)
	}
	if post("/exports/other", `{"mode":"complete"}`).StatusCode != http.StatusNotFound {
		t.Fatal("an undeclared path reached the export handler")
	}
	if post("/exports/bounded", `{"mode":"complete","extra":1}`).StatusCode != http.StatusBadRequest {
		t.Fatal("the bounded route accepted unknown input fields")
	}
}

func TestCustomerHTTPFixtureFanoutStartsAndReplaysOneChild(t *testing.T) {
	state := &httpFixtureAPI{directive: http.StatusAccepted}
	server := newHTTPFixtureTest(t, state)
	response := httpRequest(t, server.Client(), server.URL, "fanout", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fanout parent answered %d", response.StatusCode)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.children) != 2 || state.children[0].Key != state.children[1].Key || string(state.children[0].Input) != string(state.children[1].Input) {
		t.Fatalf("children = %+v", state.children)
	}
	if c := state.children[0]; c.Operation != "export" || c.Key != httpFixtureChildKey || string(c.Input) != `{"mode":"complete"}` {
		t.Fatalf("child request = %+v", c)
	}
	if state.entered != 1 {
		t.Fatal("a fanout parent waited for a release directive")
	}
}
