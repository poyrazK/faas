package securitytxt

import (
	"net/http"
	"net/http/httptest"
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
	for _, field := range []string{"Contact:", "Expires:", "Preferred-Languages:", "Canonical:", "Encryption:", "Acknowledgments:"} {
		if !strings.Contains(rec.Body.String(), field) {
			t.Errorf("body missing required RFC 9116 field %q:\n%s", field, rec.Body.String())
		}
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
