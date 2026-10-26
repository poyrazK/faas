package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// rc.296 refused public requests with policy_unavailable from several
// branches of pinPublicRoutingPolicy, and nothing said which one.
func TestPublicRoutingRefusalRecordsStage(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		prepare    func(*App)
		pin        func(PublicRoutingSnapshot) (PublicRoutingSnapshot, error)
	}{
		{name: "owner", want: "routing_owner_unverified",
			pin: func(s PublicRoutingSnapshot) (PublicRoutingSnapshot, error) { return s, state.ErrNotFound }},
		{name: "timeout", want: "routing_read_timeout",
			pin: func(s PublicRoutingSnapshot) (PublicRoutingSnapshot, error) { return s, context.DeadlineExceeded }},
		{name: "read", want: "routing_read_error",
			pin: func(s PublicRoutingSnapshot) (PublicRoutingSnapshot, error) { return s, errors.New("private store failure") }},
		{name: "invalid", want: "routing_snapshot_invalid",
			pin: func(s PublicRoutingSnapshot) (PublicRoutingSnapshot, error) { s.AppID = uuid.NewString(); return s, nil }},
		{name: "project without a live deployment", want: "routing_empty_roster",
			prepare: func(app *App) { app.ProjectID = uuid.NewString() },
			pin:     func(s PublicRoutingSnapshot) (PublicRoutingSnapshot, error) { s.ReleaseVerdict = "allowed"; return s, nil }},
		{name: "host pin", want: "routing_host_pin_denied",
			prepare: func(app *App) { app.PinnedDeploymentID, app.PinnedDeploymentScope = uuid.NewString(), "default" },
			pin:     func(s PublicRoutingSnapshot) (PublicRoutingSnapshot, error) { s.HostChecked = true; return s, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			var logs bytes.Buffer
			h.log = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			if tc.prepare != nil {
				backend.mu.Lock()
				tc.prepare(&backend.app)
				backend.mu.Unlock()
			}
			h.backend = &publicSnapshotBackend{fakeBackend: backend}
			h.WithPublicRoutingPolicy(func(_ context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
				return tc.pin(publicSnapshotFixture(app, inputs))
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if got := decisionLogField(t, logs.Bytes(), "policy_stage"); got != tc.want {
				t.Fatalf("policy_stage=%q want %q; logs=%s", got, tc.want, logs.String())
			}
			if got := decisionLogField(t, logs.Bytes(), "rejection_reason"); got != "policy_unavailable" {
				t.Fatalf("rejection_reason=%q", got)
			}
		})
	}
}

func decisionLogField(t *testing.T, logs []byte, key string) string {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(logs), []byte("\n")) {
		var row struct {
			Decision map[string]any `json:"traffic_decision"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		if row.Decision != nil {
			value, _ := row.Decision[key].(string)
			return value
		}
	}
	return ""
}
