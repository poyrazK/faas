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

func TestOperationRecoveryInspectionAndPreviewFence(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tenant-first" {
			t.Error("operator credentials missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/exports/operations/op/recovery-inspection":
			writeOperationJSON(w, 200, `{"operation_id":"op","inspection_revision":"`+revision+`"}`)
		case "POST /v1/apps/exports/operations/op/recovery-preview":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 2 || body["expected_generation"] != float64(1) || body["resolution"] != "safe_to_retry" {
				t.Error("preview wire contract", body, err)
			}
			writeOperationJSON(w, 200, `{"inspection":{"operation_id":"op","inspection_revision":"`+revision+`"},"eligible":false,"evidence_required":true,"blockers":["workflow_concurrency_limit"],"reopened_steps":["finish"]}`)
		case "POST /v1/apps/exports/operations/op/recover":
			var body faas.OperationRecoveryRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedInspectionRevision != revision {
				t.Error("apply lost revision fence", err)
			}
			writeOperationJSON(w, 200, `{"id":"op","generation":2}`)
		case "POST /v1/apps/exports/operations/op/recover-receipt":
			var body faas.OperationRecoveryRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedInspectionRevision != revision || body.RecoveryID != "decision" {
				t.Error("decision receipt lost intent", err)
			}
			writeOperationJSON(w, 200, `{"operation_id":"op","recovery_id":"decision","expected_generation":1,"generation":2,"resolution":"safe_to_retry","state":"accepted"}`)
		default:
			t.Errorf("unexpected recovery request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	c := operationClient(t, server)
	i, err := c.InspectOperationRecovery(context.Background(), "exports", "op")
	if err != nil || i.InspectionRevision != revision {
		t.Fatal("inspection DTO", err)
	}
	p, err := c.PreviewOperationRecovery(context.Background(), "exports", "op", faas.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry"})
	if err != nil || p.Eligible || !p.EvidenceRequired || len(p.ReopenedSteps) != 1 {
		t.Fatal("preview DTO", err)
	}
	if _, err := c.RecoverOperation(context.Background(), "exports", "op", faas.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider ledger checked", ExpectedInspectionRevision: i.InspectionRevision}); err != nil || calls != 3 {
		t.Fatal("revision fence wire", err)
	}
	decision, err := c.RecoverOperationWithReceipt(context.Background(), "exports", "op", faas.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider ledger checked", ExpectedInspectionRevision: i.InspectionRevision})
	if err != nil || decision.OperationID != "op" || decision.Generation != 2 || decision.State != faas.OperationAccepted || calls != 4 {
		t.Fatal("immutable decision wire", decision, err)
	}
}

func TestOperationExecutionControlUsesReadOnlyProof(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/v1/runtime/operations/operation%2Fid/control" || len(body) != 0 || r.Header.Get("Authorization") != "Bearer tenant-first" || r.Header.Get("X-Faas-Invocation-Id") != "invocation" || r.Header.Get(faas.OperationAttemptHeader) != "2" || r.Header.Get(faas.OperationCapabilityHeader) != "private-capability" {
			t.Error("control proof, path or method changed")
		}
		writeOperationJSON(w, http.StatusOK, `{"operation_id":"operation/id","invocation_id":"invocation","attempt":2,"cancellation_requested":true,"deadline_at":"2026-10-05T12:00:00Z","lease_expires_at":"2026-10-05T11:59:00Z","observed_at":"2026-10-05T11:58:00Z","poll_after_ms":1000}`)
	}))
	defer server.Close()
	control, err := operationClient(t, server).GetOperationExecutionControl(context.Background(), "operation/id", faas.OperationRuntimeProof{InvocationID: "invocation", Attempt: 2, Capability: "private-capability"})
	if err != nil || control.OperationID != "operation/id" || !control.CancellationRequested || !control.LeaseExpiresAt.Before(control.DeadlineAt) {
		t.Fatalf("control DTO: %+v %v", control, err)
	}
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
	if _, err := c.RecoverOperationWithReceipt(context.Background(), "exports", "op", faas.OperationRecoveryRequest{}); err == nil {
		t.Fatal("recovery receipt followed redirect")
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
		if r.Method != http.MethodGet || r.URL.Path != "/v1/platform-tenant-self/customer-operations" || q.Get("subject_type") != "order" || q.Get("subject_id") != "ord/42&é" || q.Get("app_id") != "app" || q.Get("scope") != "staging" || q.Get("name") != "export" || q.Get("state") != "succeeded" || q.Get("limit") != "2" || q.Get("cursor") != "opaque+/=" || r.Header.Get("Authorization") != "Bearer tenant-refreshed" {
			t.Errorf("wrong history request: %s", r.URL)
		}
		writeOperationJSON(w, http.StatusOK, `{"operations":[{"id":"export","state":"succeeded","completion_delivery":{"state":"failed","attempts":2}}],"next_cursor":"next"}`)
	}))
	defer server.Close()
	client := operationClient(t, server)
	client.SetToken("tenant-refreshed")
	page, err := client.ListPlatformTenantSelfOperations(context.Background(), faas.OperationListOptions{SubjectType: "order", SubjectID: "ord/42&é", AppID: "app", Scope: "staging", Name: "export", State: faas.OperationSucceeded, Limit: 2, Cursor: "opaque+/="})
	if err != nil || len(page.Operations) != 1 || page.Operations[0].State != faas.OperationSucceeded || page.Operations[0].CompletionDelivery.State != "failed" || page.NextCursor != "next" {
		t.Fatalf("history wire contract: %+v %v", page, err)
	}
}

func TestOperationWorkflowArtifactProofAndPrivateReceipt(t *testing.T) {
	proof := faas.OperationWorkflowRuntimeProof{RunID: "run", StepName: "finish", Generation: 2, Attempt: 3, Capability: "private-native-nonce"}
	for _, formatted := range []string{fmt.Sprintf("%v", proof), fmt.Sprintf("%+v", proof), fmt.Sprintf("%#v", proof)} {
		if strings.Contains(formatted, proof.Capability) {
			t.Fatal("workflow proof leaked")
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get(faas.OperationWorkflowRunHeader) != proof.RunID || r.Header.Get(faas.OperationWorkflowStepHeader) != proof.StepName || r.Header.Get(faas.OperationGenerationHeader) != "2" || r.Header.Get(faas.OperationAttemptHeader) != "3" || r.Header.Get(faas.OperationWorkflowCapabilityHeader) != proof.Capability || r.Header.Get(faas.OperationExecutionKindHeader) != "workflow" || r.Header.Get(faas.OperationCapabilityHeader) != "" || r.Header.Get("X-Faas-Invocation-Id") != "" {
			t.Fatal("native proof headers changed")
		}
		if calls == 1 {
			if r.URL.RequestURI() != "/v1/runtime/workflow-operations/operation%2Fid/artifact-receipts" {
				t.Fatal("native preflight path changed", r.URL.RequestURI())
			}
			writeOperationJSON(w, http.StatusOK, `{"available":false}`)
			return
		}
		if r.URL.RequestURI() != "/v1/runtime/workflow-operations/operation%2Fid/artifacts" {
			t.Fatal("native prepare path changed", r.URL.RequestURI())
		}
		writeOperationJSON(w, http.StatusOK, `{"available":true,"artifact":{"id":"file","name":"export.csv","uri":"obj://app/bucket/key","size_bytes":3,"sha256":"digest"}}`)
	}))
	defer server.Close()
	client := operationClient(t, server)
	req := faas.OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: "obj://app/bucket/key", SizeBytes: 3, SHA256: "digest"}
	receipt, err := client.ReuseWorkflowOperationArtifact(context.Background(), "operation/id", proof, req)
	if err != nil || receipt.Available || receipt.Artifact != nil {
		t.Fatalf("missing receipt=%+v %v", receipt, err)
	}
	receipt, err = client.PrepareWorkflowOperationArtifact(context.Background(), "operation/id", proof, req)
	if err != nil || !receipt.Available || receipt.Artifact == nil || receipt.Artifact.ID != "file" {
		t.Fatalf("prepared receipt=%+v %v", receipt, err)
	}
}

func TestOperationWorkflowControlWireContract(t *testing.T) {
	proof := faas.OperationWorkflowRuntimeProof{RunID: "native-run", StepName: "collect", Generation: 2, Attempt: 3, Capability: "private-native-nonce"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/v1/runtime/workflow-operations/operation%2Fid/control" || r.ContentLength != 0 {
			t.Fatal("native control request changed", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get(faas.OperationWorkflowRunHeader) != proof.RunID || r.Header.Get(faas.OperationWorkflowStepHeader) != "collect" || r.Header.Get(faas.OperationGenerationHeader) != "2" || r.Header.Get(faas.OperationAttemptHeader) != "3" || r.Header.Get(faas.OperationWorkflowCapabilityHeader) != proof.Capability || r.Header.Get(faas.OperationExecutionKindHeader) != "workflow" || r.Header.Get(faas.OperationCapabilityHeader) != "" || r.Header.Get("X-Faas-Invocation-Id") != "" {
			t.Fatal("control changed native proof family")
		}
		writeOperationJSON(w, http.StatusOK, `{"operation_id":"operation/id","workflow_run_id":"native-run","workflow_step":"collect","generation":2,"attempt":3,"cancellation_requested":false,"deadline_at":"2026-10-06T10:01:00Z","lease_expires_at":"2026-10-06T10:00:30Z","observed_at":"2026-10-06T10:00:00Z","poll_after_ms":1000}`)
	}))
	defer server.Close()
	control, err := operationClient(t, server).GetWorkflowOperationExecutionControl(context.Background(), "operation/id", proof)
	if err != nil || control.WorkflowRunID != proof.RunID || control.WorkflowStep != proof.StepName || control.Generation != 2 || control.Attempt != 3 || control.CancellationRequested || control.DeadlineAt.IsZero() || control.LeaseExpiresAt.After(control.DeadlineAt) || control.PollAfterMS != 1000 {
		t.Fatalf("native control response=%+v %v", control, err)
	}
}

func TestOperationSubmissionLookupClient(t *testing.T) {
	identity := &faas.OperationTenantIdentity{AccountID: "11111111-1111-4111-8111-111111111111", PlatformTenantID: "22222222-2222-4222-8222-222222222222"}
	scope := faas.OperationSubmissionScope{AppID: "33333333-3333-4333-8333-333333333333", Scope: "default", Name: "export"}
	lookup := faas.OperationSubmissionLookupRequest{AppID: scope.AppID, Scope: scope.Scope, Name: scope.Name, IdempotencyKey: "private-stable-key", ExpectedIdentity: identity}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer tenant-key" {
			t.Error("lookup lost bearer or bounded-body selectors")
		}
		if calls == 1 {
			if r.URL.Path != "/v1/platform-tenant-self/customer-operations/submissions/lookup" {
				t.Error(r.URL.Path)
			}
			var got faas.OperationSubmissionLookupRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.IdempotencyKey != lookup.IdempotencyKey || got.ExpectedIdentity == nil || *got.ExpectedIdentity != *identity {
				t.Error("lookup intent", got, err)
			}
			_, _ = w.Write([]byte(`{"state":"accepted","accepted_at":"2026-10-06T12:00:00Z","idempotency_expires_at":"2026-11-06T12:00:00Z","receipt":{"id":"accepted","status_url":"/status","events_url":"/events"}}`))
		} else {
			var got faas.OperationStartRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ExpectedIdentity == nil || *got.ExpectedIdentity != *identity || got.ExpectedScope == nil || *got.ExpectedScope != scope || r.Header.Get("Idempotency-Key") != lookup.IdempotencyKey {
				t.Error("submission fences", got, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"accepted","status_url":"/status","events_url":"/events"}`))
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "tenant-key")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.LookupPlatformTenantSelfOperationSubmission(context.Background(), lookup)
	if err != nil || got.State != "accepted" || got.Receipt == nil || got.Receipt.ID != "accepted" || got.AcceptedAt == nil || got.IdempotencyExpiresAt == nil || !got.AcceptedAt.Before(*got.IdempotencyExpiresAt) {
		t.Fatal("observation", got, err)
	}
	_, err = client.StartPlatformTenantSelfOperation(context.Background(), faas.OperationStartRequest{DefinitionID: scope.AppID, Input: []byte(`{"count":1}`), ExpectedIdentity: identity, ExpectedScope: &scope}, lookup.IdempotencyKey)
	if err != nil || calls != 2 {
		t.Fatal("fenced start", calls, err)
	}
}
func TestOperationMilestoneFeedsPreserveOwnershipAndReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer refreshed" {
			t.Error("milestone credential did not refresh")
		}
		if r.URL.Path == "/v1/platform-tenant-self/customer-operation-milestones" {
			if r.URL.Query().Get("subject_id") != "ord/42&é" || r.URL.Query().Get("app_id") != "app" || r.URL.Query().Get("tenant_id") != "" {
				t.Errorf("reference ownership/query: %s", r.URL)
			}
		} else if r.URL.Path != "/v1/platform-tenant-self/customer-operations/op/milestones" {
			t.Errorf("unexpected route: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"milestones":[]}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "original")
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("refreshed")
	if _, err := client.ListPlatformTenantSelfBusinessMilestones(context.Background(), faas.OperationMilestoneListOptions{AppID: "app", Scope: "default", SubjectType: "order", SubjectID: "ord/42&é", TenantID: "untrusted-owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetPlatformTenantSelfOperationMilestones(context.Background(), "op", faas.OperationMilestoneListOptions{TenantID: "untrusted-owner"}); err != nil {
		t.Fatal(err)
	}
}
