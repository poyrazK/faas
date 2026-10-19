package internalmail

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

const operatorToken = "private-operator-token-at-least-32-bytes"

func auditInput() OperatorAuditInput {
	return OperatorAuditInput{RequestID: uuid.NewString(), Actor: "operator.test", Reason: "relay_queue_reviewed", Evidence: "ticket:mail-123"}
}

func TestOperationsDefaultOffAndCredentialSeparation(t *testing.T) {
	base := map[string]string{EnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	for _, gate := range []string{"", "0", "true", " 1"} {
		base[OperationsEnableEnv] = gate
		cfg, err := LoadConfig(func(key string) string {
			if key == "FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN" {
				t.Fatal("disabled operations read secret")
			}
			return base[key]
		})
		if err != nil || cfg.Operations {
			t.Fatal("operations enabled by default", err)
		}
	}
	base[OperationsEnableEnv] = "1"
	for _, token := range []string{"", "short", testToken, operatorToken + "\n"} {
		base["FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN"] = token
		if _, err := LoadConfig(func(key string) string { return base[key] }); err == nil {
			t.Fatal("invalid operator token accepted")
		}
	}
	base["FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN"] = operatorToken
	if cfg, err := LoadConfig(func(key string) string { return base[key] }); err != nil || !cfg.Operations {
		t.Fatal(err)
	}
	if validateOperationsToken(operatorToken, testToken, operatorToken, nil) == nil || validateOperationsToken(operatorToken, testToken, "", map[string]FeedbackSource{"source": {Token: operatorToken}}) == nil {
		t.Fatal("operator credential shared with telemetry or feedback")
	}
}

func TestOperatorRoutesGateBeforeAuthenticationAndInput(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	var operations atomic.Bool
	s := &Service{Enabled: enabled.Load, OperationsEnabled: operations.Load, OperationsToken: operatorToken, RetentionEnabled: func() bool { return true }}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{OperationsPath + "/messages", OperationsPath + "/recipients/" + uuid.NewString() + "/reconcile", OperationsPath + "/retention/apply"}
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled route visible", w.Code)
		}
	}
	operations.Store(true)
	for _, credential := range []string{"", testToken, reportToken, feedbackToken, "customer-token"} {
		r := httptest.NewRequest(http.MethodPost, paths[1], forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator credential authorized", w.Code)
		}
	}
	for _, path := range []string{APIPath + "/messages", APIPath + "/reports", RetentionPath, "/metrics", "/healthz"} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("operator credential crossed route boundary", w.Code)
		}
	}
	enabled.Store(false)
	if _, err := s.ReconcileUncertain(t.Context(), "bad", ReconcileRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.OperatorRetainPayloads(t.Context(), OperatorAuditInput{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestReconcileRequiresAuditedEvidenceAndMatchingConfirmation(t *testing.T) {
	valid := ReconcileRequest{OperatorAuditInput: auditInput(), Outcome: "retry", ConfirmedNotAccepted: true}
	if valid.Validate() != nil {
		t.Fatal("valid retry rejected")
	}
	for _, change := range []func(*ReconcileRequest){
		func(r *ReconcileRequest) { r.ConfirmedNotAccepted = false },
		func(r *ReconcileRequest) { r.ConfirmedRelayAccepted = true },
		func(r *ReconcileRequest) { r.Outcome = "accepted" },
		func(r *ReconcileRequest) { r.Outcome = "submitted" },
		func(r *ReconcileRequest) { r.Actor = "" },
		func(r *ReconcileRequest) { r.RequestID = uuid.Nil.String() },
		func(r *ReconcileRequest) { r.Reason = "free form" },
		func(r *ReconcileRequest) { r.Evidence = "private\ntranscript" },
	} {
		r := valid
		change(&r)
		if r.Validate() == nil {
			t.Fatal("unsafe reconciliation accepted")
		}
	}
	valid.Outcome = "submitted"
	valid.ConfirmedNotAccepted = false
	valid.ConfirmedRelayAccepted = true
	if valid.Validate() != nil {
		t.Fatal("relay custody rejected")
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?after=bad", "?namespace=other", "?status=invalid", "?limit=abc", "?limit=3&after=" + strings.Repeat("a", 321)} {
		r := httptest.NewRequest(http.MethodGet, OperationsPath+"/messages"+query, nil)
		if _, err := operatorList(r, true, true); err == nil {
			t.Fatal("invalid bounds accepted", query)
		}
	}
}
