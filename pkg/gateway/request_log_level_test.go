package gateway

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The request path builds a request's log attributes only when the logger
// will write the line; a warm 2xx is a Debug line production drops.
func TestObserveWritesTheRequestLogOnlyAtItsLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		level  slog.Level
		status int
		cold   bool
		want   bool
	}{
		{"warm 2xx at info", slog.LevelInfo, http.StatusOK, false, false},
		{"warm 2xx at debug", slog.LevelDebug, http.StatusOK, false, true},
		{"cold 2xx at info", slog.LevelInfo, http.StatusOK, true, true},
		{"4xx at info", slog.LevelInfo, http.StatusNotFound, false, true},
		{"5xx at info", slog.LevelInfo, http.StatusServiceUnavailable, false, true},
		{"5xx at warn", slog.LevelWarn, http.StatusServiceUnavailable, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &Handler{log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: tc.level}))}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(context.WithValue(r.Context(), effectiveTrafficPolicyKey{}, "traffic-v1:abc"))
			h.observe(r, tc.status, "app-1", string(api.PlanPro), tc.cold, Target{})
			got := buf.String()
			if wrote := strings.Contains(got, `"msg":"gateway_request"`); wrote != tc.want {
				t.Fatalf("wrote the request line = %t, want %t: %s", wrote, tc.want, got)
			}
			if tc.want && !strings.Contains(got, `"traffic_policy_revision":"traffic-v1:abc"`) {
				t.Fatalf("request line lost its policy revision: %s", got)
			}
		})
	}
}

func TestRequestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		code string
		cold bool
		want slog.Level
	}{
		{"200", false, slog.LevelDebug},
		{"204", false, slog.LevelDebug},
		{"200", true, slog.LevelInfo},
		{"301", false, slog.LevelInfo},
		{"503", false, slog.LevelInfo},
		{"2", false, slog.LevelInfo},
		{"", false, slog.LevelInfo},
	} {
		if got := requestLogLevel(tc.code, tc.cold); got != tc.want {
			t.Errorf("requestLogLevel(%q, %t) = %v, want %v", tc.code, tc.cold, got, tc.want)
		}
	}
}
