package apidserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/legal"
)

// Until pkg/legal/legal.json names the operator, the legal routes answer
// exactly like an unknown path: no half-filled document reaches anyone.
func TestLegalPagesUnpublishedAnswerNotFound(t *testing.T) {
	cfg, err := legal.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Published() {
		t.Skip("legal.json is complete; TestLegalPagesServePublishedDocuments covers the live path")
	}
	e := setup(t, api.PlanPro)
	for _, doc := range legal.Documents {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+doc, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET /%s unpublished: got %d, want 404", doc, rec.Code)
		}
	}
}

// A published document is public: no session, no API key.
func TestLegalPagesServePublishedDocuments(t *testing.T) {
	e := setup(t, api.PlanPro)
	cfg, err := legal.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OperatorName, cfg.OperatorAddress, cfg.OperatorCountry = "Test Operator", "1 Example Street", "Exampleland"
	cfg.GoverningLaw, cfg.Courts, cfg.EffectiveDate = "Exampleland", "Exampletown", "2026-12-01"
	for _, doc := range legal.Documents {
		rec := httptest.NewRecorder()
		e.s.legalPage(cfg, true, doc)(rec, httptest.NewRequest(http.MethodGet, "/"+doc, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /%s: got %d %s", doc, rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET /%s: content type %q", doc, ct)
		}
		if !strings.Contains(rec.Body.String(), legal.Title(doc)) {
			t.Errorf("GET /%s: title missing", doc)
		}
	}
}
