// spec: §4.9

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestWriteSynthDispatchFailure pins H9-153: a capacity refusal behind a
// workflow or async dispatch reached schedd as a bare 502, so the step error
// never said why. Problems travel as problem+json under the same retryable
// 502; other errors keep their internal plain-text body.
func TestWriteSynthDispatchFailure(t *testing.T) {
	limit := api.ErrPlanLimitConcurrencyAt(api.Limits{Plan: api.PlanScale}, 2, 2)
	rec := httptest.NewRecorder()
	writeSynthDispatchFailure(rec, fmt.Errorf("synth invoke wake app-1: %w", limit))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (schedd retries 5xx)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("content type = %q, want problem+json", ct)
	}
	var got struct{ Code, Detail string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Code != limit.Code || got.Detail != limit.Detail {
		t.Fatalf("body = %s (err %v), want code %q detail %q", rec.Body, err, limit.Code, limit.Detail)
	}

	rec = httptest.NewRecorder()
	writeSynthDispatchFailure(rec, errors.New("dial unix /run/faas/vmmd.sock: connection refused"))
	if rec.Code != http.StatusBadGateway || strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("plain error: status %d content type %q, want plain-text 502", rec.Code, rec.Header().Get("Content-Type"))
	}
}
