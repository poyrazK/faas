package internalmail

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProducerOutboxAdmissionLookupHasSeparateGateAndOperatorAuthorization(t *testing.T) {
	var gate atomic.Bool
	s := &Service{Enabled: func() bool { return true }, ProducerOutboxEnabled: func() bool { return true },
		OutboxReconciliationEnabled: gate.Load, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	path := ProducerOutboxPath + "/items/11111111-1111-4111-8111-111111111111/admission"
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatal("disabled feature accessed auth/body/store", w.Code)
		}
	}
	if _, err := s.ProducerOutboxAdmission(t.Context(), "invalid"); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled direct lookup accessed store", err)
	}
	gate.Store(true)
	for _, token := range []string{testToken, producerToken, reportToken, feedbackToken, ""} {
		r := httptest.NewRequest(http.MethodGet, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatal("nonoperator lookup reached store", w.Code)
		}
	}
	for _, suffix := range []string{"?limit=1", "?id=private", "?token=private"} {
		r := httptest.NewRequest(http.MethodGet, path+suffix, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatal("lookup accepted query inputs", w.Code)
		}
	}
}
