// adr: 127

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordGuestExecutionEvidenceIsBoundedAndRedacted(t *testing.T) {
	r := httptestRequestWithGuestEvidence()
	ctx := r.Context()
	for _, header := range []struct{ name, value string }{
		{"X-Faas-Guest-Runtime", "node22"},
		{"X-Faas-Guest-Duration-Ms", "42"},
		{"X-Faas-Guest-Outcome", "ok"},
	} {
		if !recordGuestExecutionEvidence(ctx, header.name, header.value) {
			t.Fatalf("header %q was not consumed", header.name)
		}
	}
	if recordGuestExecutionEvidence(ctx, "X-Faas-Guest-Duration-Ms", "999999999") != true {
		t.Fatal("invalid platform header must still be redacted")
	}
	if recordGuestExecutionEvidence(ctx, "X-Customer-Header", "secret") {
		t.Fatal("ordinary response header was consumed")
	}
	evidence, ok := guestExecutionEvidenceFromContext(ctx)
	if !ok || evidence.Runtime != "node22" || evidence.DurationMS != 42 || evidence.Outcome != "ok" {
		t.Fatalf("evidence = %+v, ok=%v", evidence, ok)
	}
}

func TestForwardedResponseHeaderStripsEvidence(t *testing.T) {
	r := httptestRequestWithGuestEvidence()
	dst := make(http.Header)
	forwardedResponseHeader(r.Context(), dst, "X-Faas-Guest-Runtime", "go124")
	forwardedResponseHeader(r.Context(), dst, "X-Customer-Header", "safe")
	if dst.Get("X-Faas-Guest-Runtime") != "" || dst.Get("X-Customer-Header") != "safe" {
		t.Fatalf("forwarded headers = %v", dst)
	}
}

func TestStripGuestEvidenceResponseHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	r = withGuestExecutionEvidence(r)
	resp := &http.Response{Request: r, Header: make(http.Header)}
	resp.Header.Set("X-Faas-Guest-Runtime", "python312")
	resp.Header.Set("X-Faas-Guest-Duration-Ms", "31")
	stripGuestEvidenceResponseHeaders(resp)
	if resp.Header.Get("X-Faas-Guest-Runtime") != "" || resp.Header.Get("X-Faas-Guest-Duration-Ms") != "" {
		t.Fatalf("response headers = %v", resp.Header)
	}
	evidence, ok := guestExecutionEvidenceFromContext(r.Context())
	if !ok || evidence.Runtime != "python312" || evidence.DurationMS != 31 {
		t.Fatalf("evidence = %+v, ok=%v", evidence, ok)
	}
}

func TestDefaultProxyStripsGuestEvidenceHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Faas-Guest-Runtime", "node22")
		w.Header().Set("X-Faas-Guest-Duration-Ms", "19")
		w.Header().Set("X-Faas-Guest-Outcome", "ok")
		w.Header().Set("X-Customer-Header", "safe")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	r := httptest.NewRequest(http.MethodGet, upstream.URL+"/", nil)
	r = withGuestExecutionEvidence(r)
	rec := httptest.NewRecorder()
	defaultProxy(strings.TrimPrefix(upstream.URL, "http://"), 0).ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-Faas-Guest-Runtime") != "" || rec.Header().Get("X-Customer-Header") != "safe" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
	evidence, ok := guestExecutionEvidenceFromContext(r.Context())
	if !ok || evidence.Runtime != "node22" || evidence.DurationMS != 19 || evidence.Outcome != "ok" {
		t.Fatalf("evidence=%+v ok=%v", evidence, ok)
	}
}

func httptestRequestWithGuestEvidence() *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	return withGuestExecutionEvidence(r)
}
