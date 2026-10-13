// spec: §11
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #9: a crashing app answered every visitor with
// "500 vmmd operation failed" and the internal wake detail; with the startup
// output tail (#4511) that detail also carries the app's last log lines.
func TestWriteWakeErrorHidesStartupFailureDetailFromVisitors(t *testing.T) {
	secret := "postgres://app:hunter2@db.internal/app"
	for _, tc := range []struct {
		name     string
		problem  *api.Problem
		wantCode string
		hidden   bool
	}{
		{name: "startup crash", problem: api.NewProblem(422, api.CodeAppStartupCrash, "app exited during startup", "workload stopped during startup (guest 9e57 exited before becoming ready): Error: connect ECONNREFUSED "+secret), wantCode: api.CodeAppStartupCrash, hidden: true},
		{name: "vmmd internal", problem: api.NewProblem(500, "internal", "vmmd operation failed", "wake eb9b08b1: cold boot: vmm: readiness: "+secret), wantCode: "internal", hidden: true},
		{name: "not listening", problem: api.NewProblem(422, api.CodeAppNotListening, "no process listening on $PORT", "readiness probe dialed :8080 instance=ae80 "+secret), wantCode: api.CodeAppNotListening, hidden: true},
		{name: "snapshot backoff passes through", problem: api.NewProblem(503, api.CodeSnapshotBackoff, "Snapshot temporarily unavailable", "The deployment snapshot is temporarily unavailable"), wantCode: api.CodeSnapshotBackoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeWakeError(rec, tc.problem)
			var got api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("body %q: %v", rec.Body, err)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", got.Code, tc.wantCode)
			}
			if !tc.hidden {
				if got.Detail != tc.problem.Detail {
					t.Fatalf("detail = %q, want the original", got.Detail)
				}
				return
			}
			if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "vmm") || strings.Contains(rec.Body.String(), "instance=") {
				t.Fatalf("visitor response = %d %s, want a generic 503", rec.Code, rec.Body)
			}
		})
	}
}
