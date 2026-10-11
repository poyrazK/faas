// adr: 521
package faas_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func operationClient(t *testing.T, server *httptest.Server) *faas.Client {
	t.Helper()
	c, err := faas.NewClient(server.URL, "tenant-first")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeOperationJSON(w http.ResponseWriter, status int, data string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, data)
}

func TestOperationSubmissionIdentityAndIndependentDelivery(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/v1/platform-tenant-self/customer-operations" || r.Header.Get("Idempotency-Key") != "customer-export-42" || r.Header.Get("Authorization") != "Bearer tenant-first" {
				t.Error("submission scope or stable key changed")
			}
			var body faas.OperationStartRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DefinitionID != "export-definition" {
				t.Error("typed submission changed", err)
			}
			if string(body.Input) != `{"customer":"42"}` {
				writeOperationJSON(w, http.StatusConflict, `{"status":409,"code":"operation_idempotency_conflict","detail":"different input"}`)
				return
			}
			writeOperationJSON(w, http.StatusAccepted, `{"id":"export-42","status_url":"/status","events_url":"/events"}`)
		case http.MethodGet:
			if r.URL.Path != "/v1/platform-tenant-self/customer-operations/export-42" || r.Header.Get("Authorization") != "Bearer tenant-refreshed" {
				t.Error("read did not use current scoped token")
			}
			writeOperationJSON(w, http.StatusOK, `{"id":"export-42","generation":1,"state":"succeeded","result":{"rows":9007199254740993},"completion_delivery":{"state":"pending","attempts":2},"latest_sequence":3}`)
		}
	}))
	defer server.Close()
	c := operationClient(t, server)
	body := faas.OperationStartRequest{DefinitionID: "export-definition", Input: json.RawMessage(`{"customer":"42"}`)}
	for _, key := range []string{"", strings.Repeat("x", 129), strings.Repeat("é", 65), "line\rbreak", "line\nbreak", "nul\x00key"} {
		if _, err := c.StartPlatformTenantSelfOperation(context.Background(), body, key); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid key reached the API")
	}
	for range 2 {
		receipt, err := c.StartPlatformTenantSelfOperation(context.Background(), body, "customer-export-42")
		if err != nil || receipt.ID != "export-42" || receipt.StatusURL != "/status" || receipt.EventsURL != "/events" {
			t.Fatal("receipt lost on replay", receipt, err)
		}
	}
	body.Input = json.RawMessage(`{"customer":"43"}`)
	_, err := c.StartPlatformTenantSelfOperation(context.Background(), body, "customer-export-42")
	var problem *faas.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != 409 || problem.Problem.Code != "operation_idempotency_conflict" {
		t.Fatal("payload conflict not preserved", err)
	}
	c.SetToken("tenant-refreshed")
	operation, err := c.GetPlatformTenantSelfOperation(context.Background(), "export-42")
	if err != nil || operation.ID != "export-42" || operation.State != faas.OperationSucceeded || !operation.State.Terminal() || operation.CompletionDelivery.State != "pending" || operation.CompletionDelivery.Attempts != 2 {
		t.Fatal("notification failure changed business outcome", operation, err)
	}
	if string(operation.Result) != `{"rows":9007199254740993}` {
		t.Fatal("result precision changed", string(operation.Result))
	}
}

type operationTransport func(*http.Request) (*http.Response, error)

func (f operationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOperationArtifactLengthDigestAndBounds(t *testing.T) {
	data := "export bytes"
	for _, tc := range []struct {
		name   string
		length int64
		digest string
		ok     bool
	}{
		{"valid", int64(len(data)), fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(data))), true},
		{"unknown", -1, "", false},
		{"oversized", 256<<20 + 1, "", false},
		{"truncated", int64(len(data) + 1), "", false},
		{"extra_bytes", int64(len(data) - 1), "", false},
		{"wrong_digest", int64(len(data)), "sha256:wrong", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := operationTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer scoped-current" || r.URL.EscapedPath() != "/v1/apps/app%2Fname/operations/op%2Fid/artifacts/file%2Fid" {
					t.Error("artifact scope or path changed")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Gregale-Artifact-Sha256": {tc.digest}}, ContentLength: tc.length, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
			})
			c, err := faas.NewClient("https://api.example.test", "scoped-current", faas.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			var dst bytes.Buffer
			n, err := c.DownloadOperationArtifact(context.Background(), "app/name", "op/id", "file/id", &dst)
			if tc.ok && (err != nil || dst.String() != data || n != int64(len(data))) {
				t.Fatal("valid download failed", n, err)
			}
			if !tc.ok && err == nil {
				t.Fatal("unverified artifact accepted")
			}
			if tc.name == "truncated" || tc.name == "extra_bytes" {
				var mismatch *faas.OperationArtifactTruncatedError
				if !errors.As(err, &mismatch) || mismatch.Expected != tc.length {
					t.Fatal("length error not typed", err)
				}
			}
		})
	}
}

func TestOperationCredentialsNeverFollowRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		writeOperationJSON(w, 200, `{"id":"stolen"}`)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := operationClient(t, server)
	if _, err := c.GetPlatformTenantSelfOperation(context.Background(), "op"); err == nil {
		t.Fatal("JSON redirect succeeded")
	}
	if _, err := c.DownloadPlatformTenantSelfOperationArtifact(context.Background(), "op", "file", io.Discard); err == nil {
		t.Fatal("artifact redirect succeeded")
	}
	if body, err := c.StreamPlatformTenantSelfOperationEvents(context.Background(), "op", 0); err == nil {
		_ = body.Close()
		t.Fatal("stream redirect succeeded")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("operation credential followed a redirect")
	}
}

func TestOperationStreamsRejectUnavailableAndInvalidProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, mediaType, data string
		status                int
	}{
		{"expired_auth", "application/json", `{"status":401,"code":"unauthorized"}`, 401},
		{"gone", "application/json", `{"status":410,"code":"operation_unavailable"}`, 410},
		{"wrong_media", "application/json", `{}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mediaType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.data)
			}))
			defer server.Close()
			c := operationClient(t, server)
			body, err := c.StreamPlatformTenantSelfOperationEvents(context.Background(), "op", 0)
			if err == nil || body != nil {
				t.Fatal("invalid stream accepted")
			}
			if tc.status != 200 {
				var problem *faas.APIError
				if !errors.As(err, &problem) || problem.Problem.Status != tc.status {
					t.Fatal("stream access error lost", err)
				}
			}
		})
	}
}

func TestOperationJSONRejectsMissingAndOversizedBodies(t *testing.T) {
	for _, body := range []string{"", "not JSON", strings.Repeat("x", 4<<20+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		c := operationClient(t, server)
		_, err := c.GetPlatformTenantSelfOperation(context.Background(), "op")
		server.Close()
		if err == nil {
			t.Fatal("invalid operation response accepted")
		}
	}
}

func TestOperationRuntimeProofIsPrivateAndForwardedOnlyToRuntime(t *testing.T) {
	proof := faas.OperationRuntimeProof{InvocationID: "invocation", Attempt: 2, Capability: strings.Repeat("a", 64)}
	raw, err := json.Marshal(proof)
	if err != nil || strings.Contains(string(raw), proof.Capability) || strings.Contains(fmt.Sprintf("%+v %#v", proof, proof), proof.Capability) {
		t.Fatal("runtime proof leaked")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runtime/operations/export/progress" || r.Header.Get("X-Faas-Invocation-Id") != proof.InvocationID || r.Header.Get("X-Gregale-Operation-Attempt") != "2" || r.Header.Get("X-Gregale-Operation-Capability") != proof.Capability {
			t.Error("runtime fence lost")
		}
		writeOperationJSON(w, 200, `{"id":"export","state":"running"}`)
	}))
	defer server.Close()
	out, err := operationClient(t, server).ReportOperationProgress(context.Background(), "export", proof, faas.OperationReportRequest{ReportID: "chunk-1", Stage: "generating", Completed: 1, Total: 1})
	if err != nil || out.ID != "export" || out.State != faas.OperationRunning {
		t.Fatal("runtime projection lost", out, err)
	}
}

func TestOperationHistoryScopedWireContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/v1/platform-tenant-self/customer-operations" || q.Get("app_id") != "app" || q.Get("scope") != "staging" || q.Get("name") != "export" || q.Get("state") != "succeeded" || q.Get("limit") != "2" || q.Get("cursor") != "opaque+/=" || r.Header.Get("Authorization") != "Bearer tenant-refreshed" {
			t.Errorf("wrong history request: %s", r.URL)
		}
		writeOperationJSON(w, http.StatusOK, `{"operations":[{"id":"export","state":"succeeded","completion_delivery":{"state":"failed","attempts":2}}],"next_cursor":"next"}`)
	}))
	defer server.Close()
	client := operationClient(t, server)
	client.SetToken("tenant-refreshed")
	page, err := client.ListPlatformTenantSelfOperations(context.Background(), faas.OperationListOptions{AppID: "app", Scope: "staging", Name: "export", State: faas.OperationSucceeded, Limit: 2, Cursor: "opaque+/="})
	if err != nil || len(page.Operations) != 1 || page.Operations[0].State != faas.OperationSucceeded || page.Operations[0].CompletionDelivery.State != "failed" || page.NextCursor != "next" {
		t.Fatalf("history wire contract: %+v %v", page, err)
	}
}
