package imagerun_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/imagerun"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod 2026-11-19: `sandbox run --image` on an installation without image
// runs answered 404 "Not found".
func TestDisabledImageRunsAnswerNotImplemented(t *testing.T) {
	h := &imagerun.Handler{}
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/v1/image-runs", strings.NewReader(`{}`)), state.Account{})
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "not enabled") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}
