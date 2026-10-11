// spec: §4.1 — gatewayd edge answers for a refused wake.

package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #5 (H5-30): fifteen parked apps woken at once filled the
// two-node fleet, and every refused caller read schedd's operator detail
// (node UUIDs, compute_nodes columns, vCPU budgets) with no Retry-After.
func TestWriteWakeErrorHidesFleetCapacityDetail(t *testing.T) {
	for _, detail := range []string{
		`vCPU headroom: node "7190377d-5f4a-45e9-ad17-578f2d8ef7d2" busy 32 + 4 requested exceeds the 32 per-node vCPU budget`,
		"placement: no active compute_node fits 1032 MB billable (per-node ceilings: see compute_nodes.admission_ceiling_mb) / 4 guest vCPU across 2 candidates",
	} {
		rec := httptest.NewRecorder()
		writeWakeError(rec, api.ErrCapacity(detail))
		body := rec.Body.String()
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(body, api.CodeCapacity) {
			t.Fatalf("status=%d body=%s, want 503 %s", rec.Code, body, api.CodeCapacity)
		}
		for _, leak := range []string{"7190377d", "compute_node", "vCPU", "candidates"} {
			if strings.Contains(body, leak) {
				t.Fatalf("body %s leaks %q", body, leak)
			}
		}
		if rec.Header().Get("Retry-After") != "5" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("headers = %v, want Retry-After 5 and no-store", rec.Header())
		}
	}
}

func TestLogFleetCapacityRefusalKeepsOperatorDetail(t *testing.T) {
	var buf bytes.Buffer
	h := &Handler{log: slog.New(slog.NewJSONHandler(&buf, nil))}
	h.logFleetCapacityRefusal("app-1", api.ErrCapacity(`vCPU headroom: node "n-1" busy 32`))
	h.logFleetCapacityRefusal("app-1", api.NewProblem(http.StatusTooManyRequests, api.CodePlanLimitConcur, "Concurrency limit", "1 already live"))
	if got := strings.Count(buf.String(), "wake refused for fleet capacity"); got != 1 || !strings.Contains(buf.String(), `busy 32`) {
		t.Fatalf("log = %s, want one capacity line with the detail", buf.String())
	}
}
