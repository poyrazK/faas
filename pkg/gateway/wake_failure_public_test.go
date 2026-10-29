// spec: §11
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

// Container qualification 2026-10-27: a public-routing wake answered 503
// "wake failed" in 17 ms and no daemon logged why. Every error writeWakeError
// answers with that generic detail must leave its cause in the gateway log;
// the errors it classifies, and a caller that went away, must not.
func TestLogWakeFailureRecordsTheCauseOfAGenericWakeFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		logged bool
	}{
		{"plain error", errors.New("resolve owner schedd: no client for node n-1"), true},
		{"deadline", context.DeadlineExceeded, true},
		{"caller went away", context.Canceled, false},
		{"queue full", ErrQueueFull, false},
		{"wake queue wait", ErrWakeQueueWaitTimeout, false},
		{"admission busy", ErrWakeAdmissionQueueFull, false},
		{"bootstrap aborted", ErrBootstrapAborted, false},
		{"concurrency queue full", ErrConcurrencyQueueFull, false},
		{"concurrency queue wait", ErrConcurrencyQueueWaitTimeout, false},
		{"concurrency admission unavailable", ErrConcurrencyQueueAdmissionUnavailable, false},
		{"concurrency drop", &WakeConcurrencyDropError{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeWakeError(rec, tc.err)
			generic := strings.Contains(rec.Body.String(), `"wake failed"`)
			if unclassifiedWakeError(tc.err) != generic {
				t.Fatalf("unclassifiedWakeError = %v, but writeWakeError's generic answer = %v (body %s)", !generic, generic, rec.Body)
			}
			var buf bytes.Buffer
			h := &Handler{log: slog.New(slog.NewJSONHandler(&buf, nil))}
			h.logWakeFailure("app-1", tc.err)
			if logged := strings.Contains(buf.String(), "gateway: wake failed"); logged != tc.logged {
				t.Fatalf("logged = %v, want %v: %s", logged, tc.logged, buf.String())
			}
			if tc.logged && !strings.Contains(buf.String(), tc.err.Error()) {
				t.Fatalf("log lacks the cause %q: %s", tc.err, buf.String())
			}
		})
	}
}
