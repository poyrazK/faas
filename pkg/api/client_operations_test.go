// ADR-521: retries preserve logical operation identity and decoded public results.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperationExecutionControlClientProof(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/v1/runtime/operations/operation%2Fid/control" || len(body) != 0 || r.Header.Get("Authorization") != "Bearer workload" || r.Header.Get(InvocationIDHeader) != "invocation" || r.Header.Get(OperationAttemptHeader) != "2" || r.Header.Get(OperationCapabilityHeader) != "private-capability" {
			t.Error("control proof, path or method changed")
		}
		_, _ = w.Write([]byte(`{"operation_id":"operation/id","invocation_id":"invocation","attempt":2,"cancellation_requested":true,"deadline_at":"2026-10-05T12:00:00Z","lease_expires_at":"2026-10-05T11:59:00Z","observed_at":"2026-10-05T11:58:00Z","poll_after_ms":1000}`))
	}))
	defer srv.Close()
	control, err := NewClient(srv.URL, "workload").GetOperationExecutionControl(t.Context(), "operation/id", OperationRuntimeProof{InvocationID: "invocation", Attempt: 2, Capability: "private-capability"})
	if err != nil || control.OperationID != "operation/id" || !control.CancellationRequested || !control.LeaseExpiresAt.Before(control.DeadlineAt) {
		t.Fatalf("control DTO: %+v %v", control, err)
	}
}

func TestOperationClientContract(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tenant-key" {
			t.Error("lost bearer")
		}
		switch calls {
		case 1:
			if r.Method != "POST" || r.URL.Path != "/v1/platform-tenant-self/customer-operations" || r.Header.Get("Idempotency-Key") != "stable-export" {
				t.Errorf("submission changed: %s %s %v", r.Method, r.URL.Path, r.Header)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"export-id","status_url":"/status","events_url":"/events"}`))
		case 2:
			if r.URL.RequestURI() != "/v1/apps/customer%20app/operations/operation%2Fid" {
				t.Errorf("escaped path: %s", r.URL.RequestURI())
			}
			_, _ = w.Write([]byte(`{"id":"export-id","state":"succeeded"}`))
		case 3:
			if r.URL.RequestURI() != "/v1/platform-tenant-self/customer-operations/operation%2Fid/events?after=42" {
				t.Errorf("resume cursor: %s", r.URL.RequestURI())
			}
			_, _ = w.Write([]byte(`{"events":[],"latest_sequence":42,"resync_required":false}`))
		default:
			t.Error("unexpected request")
		}
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "tenant-key")
	ctx := context.Background()
	if _, err := client.StartPlatformTenantSelfOperation(ctx, OperationStartRequest{}, ""); err == nil || calls != 0 {
		t.Fatal("empty key sent a request")
	}
	receipt, err := client.StartPlatformTenantSelfOperation(ctx, OperationStartRequest{}, "stable-export")
	if err != nil || receipt.ID != "export-id" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	status, err := client.GetOperation(ctx, "customer app", "operation/id")
	if err != nil || status.State != "succeeded" {
		t.Fatalf("status: %+v %v", status, err)
	}
	page, err := client.GetPlatformTenantSelfOperationEvents(ctx, "operation/id", 42)
	if err != nil || page.LatestSequence != 42 {
		t.Fatalf("page: %+v %v", page, err)
	}
}

func TestOperationRecoveryDecisionClient(t *testing.T) {
	req := OperationRecoveryRequest{RecoveryID: "stable-decision", ExpectedGeneration: 2, Resolution: "succeeded", Evidence: "provider receipt checked\n", Result: []byte(`{"wide":9007199254740993}`), ExpectedInspectionRevision: "sha256:" + strings.Repeat("a", 64)}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/v1/apps/customer%20app/operations/operation%2Fid/recover-receipt" || r.Header.Get("Authorization") != "Bearer account-key" {
			t.Error("recovery receipt transport changed")
		}
		var got OperationRecoveryRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.RecoveryID != req.RecoveryID || got.Evidence != req.Evidence || !bytes.Equal(got.Result, req.Result) || got.ExpectedInspectionRevision != req.ExpectedInspectionRevision {
			t.Error("recovery intent changed", got, err)
		}
		_ = json.NewEncoder(w).Encode(OperationRecoveryDecision{OperationID: "operation/id", RecoveryID: req.RecoveryID, ExpectedGeneration: 2, Generation: 2, Resolution: "succeeded", State: OperationSucceeded})
	}))
	defer srv.Close()
	decision, err := NewClient(srv.URL, "account-key").RecoverOperationWithReceipt(t.Context(), "customer app", "operation/id", req)
	if err != nil || decision.RecoveryID != req.RecoveryID || decision.Generation != 2 || decision.State != OperationSucceeded || calls != 1 {
		t.Fatal("immutable decision DTO", decision, err)
	}
}

func TestOperationSubmissionLookupClient(t *testing.T) {
	identity := &OperationTenantIdentity{AccountID: "11111111-1111-4111-8111-111111111111", PlatformTenantID: "22222222-2222-4222-8222-222222222222"}
	scope := OperationSubmissionScope{AppID: "33333333-3333-4333-8333-333333333333", Scope: "default", Name: "export"}
	lookup := OperationSubmissionLookupRequest{AppID: scope.AppID, Scope: scope.Scope, Name: scope.Name, IdempotencyKey: "private-stable-key", ExpectedIdentity: identity}
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
			var got OperationSubmissionLookupRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.IdempotencyKey != lookup.IdempotencyKey || got.ExpectedIdentity == nil || *got.ExpectedIdentity != *identity {
				t.Error("lookup intent", got, err)
			}
			_, _ = w.Write([]byte(`{"state":"accepted","accepted_at":"2026-10-06T12:00:00Z","idempotency_expires_at":"2026-11-06T12:00:00Z","receipt":{"id":"accepted","status_url":"/status","events_url":"/events"}}`))
		} else {
			var got OperationStartRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ExpectedIdentity == nil || *got.ExpectedIdentity != *identity || got.ExpectedScope == nil || *got.ExpectedScope != scope || r.Header.Get("Idempotency-Key") != lookup.IdempotencyKey {
				t.Error("submission fences", got, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"accepted","status_url":"/status","events_url":"/events"}`))
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "tenant-key")
	got, err := client.LookupPlatformTenantSelfOperationSubmission(t.Context(), lookup)
	if err != nil || got.State != "accepted" || got.Receipt == nil || got.Receipt.ID != "accepted" || got.AcceptedAt == nil || got.IdempotencyExpiresAt == nil || !got.AcceptedAt.Before(*got.IdempotencyExpiresAt) {
		t.Fatal("observation", got, err)
	}
	_, err = client.StartPlatformTenantSelfOperation(t.Context(), OperationStartRequest{DefinitionID: scope.AppID, Input: []byte(`{"count":1}`), ExpectedIdentity: identity, ExpectedScope: &scope}, lookup.IdempotencyKey)
	if err != nil || calls != 2 {
		t.Fatal("fenced start", calls, err)
	}
}
