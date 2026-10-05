package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/internalsvc"
)

func TestHandleInvocationDispatch_WorkflowRequiresAuthenticatedActiveStep(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tok, err := internalsvc.Mint("schedd", 30*time.Second, nil, priv, internalsvc.KidFromPub(pub))
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}

	newRequest := func(platformTenantID string) *http.Request {
		headers := map[string]string{
			"X-Faas-Internal-Wake":    "workflow",
			"X-Faas-Workflow-Run-Id":  "run-1",
			"X-Faas-Workflow-Step":    "charge",
			"X-Faas-Workflow-Attempt": "1",
		}
		body, marshalErr := json.Marshal(map[string]any{
			"invocation_id":      "workflow-inv-1",
			"app_id":             "app-1",
			"platform_tenant_id": platformTenantID,
			"source":             "workflow",
			"method":             "POST",
			"path":               "/charge",
			"headers":            headers,
		})
		if marshalErr != nil {
			t.Fatalf("marshal request: %v", marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+tok)
		return req
	}

	t.Run("active step dispatches", func(t *testing.T) {
		srv, dispatcher := newSynthServer(t)
		srv.internalSvcVerifier = &testInternalSvcVerifier{allowed: map[string]ed25519.PublicKey{"schedd": pub}}
		srv.workflowAdmission = func(_ context.Context, appID, runID, tenantID, stepName string, attempt int) error {
			if appID != "app-1" || runID != "run-1" || tenantID != "" || stepName != "charge" || attempt != 1 {
				t.Fatalf("admission args = %s/%s/%s/%s/%d", appID, runID, tenantID, stepName, attempt)
			}
			return nil
		}
		w := httptest.NewRecorder()
		srv.handleInvocationDispatch(w, newRequest(""))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if len(dispatcher.invs) != 1 {
			t.Fatalf("dispatcher calls = %d, want 1", len(dispatcher.invs))
		}
	})

	t.Run("replay is rejected before dispatch", func(t *testing.T) {
		srv, dispatcher := newSynthServer(t)
		srv.internalSvcVerifier = &testInternalSvcVerifier{allowed: map[string]ed25519.PublicKey{"schedd": pub}}
		srv.workflowAdmission = func(context.Context, string, string, string, string, int) error {
			return errors.New("workflow step is no longer active")
		}
		w := httptest.NewRecorder()
		srv.handleInvocationDispatch(w, newRequest(""))
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
		}
		if len(dispatcher.invs) != 0 {
			t.Fatalf("dispatcher calls = %d, want 0", len(dispatcher.invs))
		}
	})

	t.Run("tenant identity reaches admission and dispatch", func(t *testing.T) {
		srv, dispatcher := newSynthServer(t)
		srv.internalSvcVerifier = &testInternalSvcVerifier{allowed: map[string]ed25519.PublicKey{"schedd": pub}}
		srv.workflowAdmission = func(_ context.Context, appID, runID, tenantID, stepName string, attempt int) error {
			if appID != "app-1" || runID != "run-1" || tenantID != "tenant-1" || stepName != "charge" || attempt != 1 {
				t.Fatalf("admission args = %s/%s/%s/%s/%d", appID, runID, tenantID, stepName, attempt)
			}
			return nil
		}
		w := httptest.NewRecorder()
		srv.handleInvocationDispatch(w, newRequest("tenant-1"))
		if w.Code != http.StatusOK || len(dispatcher.invs) != 1 || dispatcher.invs[0].PlatformTenantID != "tenant-1" {
			t.Fatalf("status=%d dispatched=%+v body=%s", w.Code, dispatcher.invs, w.Body.String())
		}
	})
}

func TestHandleInvocationDispatch_WorkflowRejectsMissingToken(t *testing.T) {
	srv, dispatcher := newSynthServer(t)
	srv.internalSvcVerifier = &testInternalSvcVerifier{}
	srv.workflowAdmission = func(context.Context, string, string, string, string, int) error { return nil }
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader(`{"invocation_id":"workflow-inv-2","app_id":"app-1","source":"workflow","headers":{"X-Faas-Internal-Wake":"workflow","X-Faas-Workflow-Run-Id":"run-1","X-Faas-Workflow-Step":"charge","X-Faas-Workflow-Attempt":"1"}}`))
	srv.handleInvocationDispatch(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if len(dispatcher.invs) != 0 {
		t.Fatalf("dispatcher calls = %d, want 0", len(dispatcher.invs))
	}
}
