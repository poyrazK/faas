package durable

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func TestTenantAdmissionTranslatesGrantDenialWithoutProviderDetails(t *testing.T) {
	// ADR-989: a revoked connection grant fails closed as forbidden, rather
	// than reporting private storage details or a retryable admission error.
	w := httptest.NewRecorder()
	WriteTenantQuotaProblem(w, errors.Join(entitysocket.ErrGrantDenied, errors.New("private bucket detail")))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Session grant denied") || strings.Contains(w.Body.String(), "private bucket") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestGrantProviderFailureRemainsUnavailableAndPrivate(t *testing.T) {
	w := httptest.NewRecorder()
	WriteGrantProblem(w, errors.New("private bucket detail"))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private bucket") {
		t.Fatal(w.Code, w.Body.String())
	}
}
