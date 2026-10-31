package securitytxt

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHandlerServesRFC9116Document(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	// Contact and Expires are required by RFC 9116; Canonical and Policy are
	// the fields researchers follow.
	for _, field := range []string{"Contact:", "Expires:", "Preferred-Languages:", "Canonical:", "Policy:"} {
		if !strings.Contains(rec.Body.String(), field) {
			t.Errorf("body missing RFC 9116 field %q:\n%s", field, rec.Body.String())
		}
	}
	// Issue #2518: docs.gregale.dev was never deployed, and a field that
	// points at a page that does not exist is worse than no field.
	if strings.Contains(rec.Body.String(), "docs.gregale.dev") {
		t.Errorf("security.txt links the retired docs host:\n%s", rec.Body.String())
	}
	if got := rec.Body.String(); got != Content {
		t.Errorf("body = %q, want %q", got, Content)
	}
}

func TestHandlerSupportsHeadAndRejectsOtherMethods(t *testing.T) {
	headReq := httptest.NewRequest(http.MethodHead, "/.well-known/security.txt", nil)
	headRec := httptest.NewRecorder()
	Handler().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 {
		t.Fatalf("HEAD = status %d body %q, want 200 with no body", headRec.Code, headRec.Body.String())
	}

	postReq := httptest.NewRequest(http.MethodPost, "/.well-known/security.txt", nil)
	postRec := httptest.NewRecorder()
	Handler().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", postRec.Code)
	}
	if got := postRec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}
}

// The repository copy at .well-known/security.txt is the served document.
func TestRepositoryCopyMatchesServedDocument(t *testing.T) {
	raw, err := os.ReadFile("../../.well-known/security.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != Content {
		t.Fatalf(".well-known/security.txt = %q, want the served Content %q", raw, Content)
	}
}
