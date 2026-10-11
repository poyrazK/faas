// adr: 040 — request-rate 429s carry the limit-error fields.

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// assertRateLimitedProblem pins the CLAUDE.md limit-error contract on a
// request-rate 429 (H5-10): the burst limit, the spent burst and a docs link.
func assertRateLimitedProblem(t *testing.T, rec *httptest.ResponseRecorder, wantLimit int64) {
	t.Helper()
	var body struct {
		Code     string `json:"code"`
		Limit    *int64 `json:"limit"`
		Observed *int64 `json:"observed"`
		DocsURL  string `json:"docs_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("429 body is not a problem document: %v (%s)", err, rec.Body.String())
	}
	if body.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", body.Code)
	}
	if body.Limit == nil || *body.Limit != wantLimit {
		t.Errorf("limit = %v, want %d (body %s)", body.Limit, wantLimit, rec.Body.String())
	}
	if body.Observed == nil || *body.Observed < 1 || (body.Limit != nil && *body.Observed > *body.Limit) {
		t.Errorf("observed = %v, want the spent burst in [1, limit] (body %s)", body.Observed, rec.Body.String())
	}
	if body.DocsURL == "" {
		t.Errorf("429 lacks docs_url (body %s)", rec.Body.String())
	}
}
