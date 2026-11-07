package apidserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/legal"
	"github.com/onebox-faas/faas/pkg/state"
)

// Until pkg/legal/legal.json is complete, the legal routes answer exactly
// like an unknown path: no half-filled document reaches anyone.
func TestLegalPagesUnpublishedAnswerNotFound(t *testing.T) {
	if _, ok := publishedLegal(); ok {
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
	cfg := publishedTestConfig(t)
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

func publishedTestConfig(t *testing.T) legal.Config {
	t.Helper()
	cfg, err := legal.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.EffectiveDate = "2026-12-01"
	if !cfg.Published() {
		t.Fatalf("test config unpublished: %v", cfg.Missing())
	}
	return cfg
}

// publishLegalForTest makes the legal config published for one test. Not
// parallel-safe: loadLegal is process-wide.
func publishLegalForTest(t *testing.T) legal.Config {
	t.Helper()
	cfg := publishedTestConfig(t)
	prev := loadLegal
	loadLegal = func() (legal.Config, error) { return cfg, nil }
	t.Cleanup(func() { loadLegal = prev })
	return cfg
}

// Sign-up stores the published Terms version with the new account; while
// the Terms are unpublished it stores nothing.
func TestSignupRecordsTermsAcceptance(t *testing.T) {
	if _, ok := publishedLegal(); ok {
		t.Skip("legal.json is published; the unpublished case needs an unpublished config")
	}
	srv, _, st := v1AuthTestHarness(t)
	mem := st.(*state.MemStore)
	signup := func(email string) state.Account {
		t.Helper()
		rec := v1AuthJSONRequest(t, srv.handler(), "/v1/auth/signup", `{"email":"`+email+`","password":"signup-password-long-123!"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("signup %s: %d %s", email, rec.Code, rec.Body)
		}
		acct, err := st.AccountByEmail(t.Context(), email)
		if err != nil {
			t.Fatal(err)
		}
		return acct
	}

	before := signup("before@example.com")
	if got, _ := mem.LegalAcceptances(t.Context(), before.ID); len(got) != 0 {
		t.Fatalf("unpublished Terms recorded an acceptance: %+v", got)
	}

	cfg := publishLegalForTest(t)
	after := signup("after@example.com")
	got, err := mem.LegalAcceptances(t.Context(), after.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Document != state.LegalDocumentTerms || got[0].Version != cfg.Version() || got[0].Method != signupMethodPassword {
		t.Fatalf("acceptance = %+v, want terms %s via %s", got, cfg.Version(), signupMethodPassword)
	}
}

// The sign-in page links the Terms only once they are published.
func TestLoginPageShowsTermsNoticeWhenPublished(t *testing.T) {
	e := setup(t, api.PlanPro)
	get := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /login: %d", rec.Code)
		}
		return rec.Body.String()
	}
	if _, ok := publishedLegal(); !ok && strings.Contains(get(), `href="/terms"`) {
		t.Fatal("unpublished Terms linked from the login page")
	}
	publishLegalForTest(t)
	if body := get(); !strings.Contains(body, `href="/terms"`) || !strings.Contains(body, `href="/privacy"`) {
		t.Fatal("login page lacks the Terms notice")
	}
}
