package internalmail

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryControlFlagAndCredentialsFailClosed(t *testing.T) {
	for _, gate := range []string{"", "0", "true", " 1"} {
		if DeliveryControlsEnabled(func(key string) string {
			if key == EnableEnv {
				return "1"
			}
			return gate
		}) {
			t.Fatal("truthy controls flag enabled access")
		}
	}
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{DeliveryControlsPath, DeliveryControlsPath + "/pause", OperationsPath + "/messages/11111111-1111-4111-8111-111111111111/cancel", OperationsPath + "/recipients/11111111-1111-4111-8111-111111111111/cancel"} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled control route visible", w.Code)
		}
	}
	if _, err := s.ChangeDeliveryControl(t.Context(), "paused", DeliveryControlRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.CancelMail(t.Context(), "bad", true, OperatorAuditInput{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.DeliveryStatus(t.Context(), DeliveryScope{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	s.DeliveryControlsEnabled = func() bool { return true }
	for _, token := range []string{testToken, reportToken, feedbackToken, "customer"} {
		r := httptest.NewRequest(http.MethodPost, DeliveryControlsPath+"/pause", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator control access", w.Code)
		}
	}
}

func TestDeliveryControlScopeAndRevisionAreExplicit(t *testing.T) {
	revision := int64(0)
	valid := DeliveryControlRequest{OperatorAuditInput: auditInput(), DeliveryScope: DeliveryScope{Kind: "namespace"}, ExpectedRevision: &revision}
	if valid.Validate() != nil {
		t.Fatal("initial revision rejected")
	}
	for _, mutate := range []func(*DeliveryControlRequest){
		func(r *DeliveryControlRequest) { r.ExpectedRevision = nil }, func(r *DeliveryControlRequest) { n := int64(-1); r.ExpectedRevision = &n },
		func(r *DeliveryControlRequest) { r.Kind = "" }, func(r *DeliveryControlRequest) { r.DomainID = "11111111-1111-4111-8111-111111111111" },
		func(r *DeliveryControlRequest) { r.Kind = "domain" }, func(r *DeliveryControlRequest) { r.Actor = "" },
	} {
		r := valid
		mutate(&r)
		if r.Validate() == nil {
			t.Fatal("ambiguous scope/revision accepted")
		}
	}
	for _, query := range []string{"namespace=other", "domain_id=bad", "domain_id=", "domain_id=11111111-1111-4111-8111-111111111111&domain_id=11111111-1111-4111-8111-111111111111", "domain_id=%XX"} {
		if _, err := controlStatusScope(query); err == nil {
			t.Fatal("invalid status scope accepted", query)
		}
	}
}
