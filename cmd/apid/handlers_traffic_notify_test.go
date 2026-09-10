package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// captureNotifier records every Notify call so the test can assert
// the exact channel + payload the handler emits. Mirrors
// stubNotifier (server_test.go) but with capture semantics; only
// the Notify path is exercised here (PATCH /traffic), so Subscribe
// returns a closed channel and WaitFor returns ErrWaitTimeout —
// matching stubNotifier's defaults.
type captureNotifier struct {
	mu    sync.Mutex
	notif []captured
}

type captured struct {
	channel string
	payload string
}

func (c *captureNotifier) Notify(_ context.Context, channel, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notif = append(c.notif, captured{channel: channel, payload: payload})
	return nil
}

func (c *captureNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}

func (c *captureNotifier) WaitFor(_ context.Context, _ string, _ func(payload string) bool, _ time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func (c *captureNotifier) byChannel(channel string) []captured {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []captured
	for _, n := range c.notif {
		if n.channel == channel {
			out = append(out, n)
		}
	}
	return out
}

// TestPatchDeploymentTraffic_EmitsTrafficNotify is the PR-C handler
// pin for S1 (issue #556 PR-A defect: updateDeploymentTraffic emitted
// no pg_notify, so PR-B's gateway refresh subscriber was dead code on
// the traffic-set path). Fires a PATCH and asserts the notifier sees
// a deployment_changed event with kind="traffic", the right app_id +
// deployment_id, and the new traffic_percent. Without the C2 emit
// the captured list is empty — failure message points at the emit
// site at cmd/apid/handlers_ext.go:1262.
func TestPatchDeploymentTraffic_EmitsTrafficNotify(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "traffic-notify@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "traffic-notify-app", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
		Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	// Second live deployment so the canary 25% stamp has a sibling
	// to redistribute the residual across (Σ=100 contract).
	depB, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def",
		Status: state.DeployPending, Scope: "traffic-b",
	})
	if err != nil {
		t.Fatalf("CreateDeployment (B): %v", err)
	}
	if err := store.MarkDeploymentLive(context.Background(), depB.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (B): %v", err)
	}
	// Establish a valid 0/100 two-live-row fixture before stamping dep to 25.
	if _, err := store.UpdateDeploymentTraffic(context.Background(), dep.ID, 0); err != nil {
		t.Fatalf("zero dep traffic: %v", err)
	}
	apiKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesDeployWriteSurface); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	notif := &captureNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	handler := srv.handler()

	body := strings.NewReader(`{"traffic_percent":25}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/deployments/"+dep.ID+"/traffic", body)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	calls := notif.byChannel(db.NotifyDeploymentChanged)
	if len(calls) == 0 {
		t.Fatalf("no deployment_changed notify emitted; C2 emit missing at handlers_ext.go")
	}
	// Find the kind=traffic frame.
	var trafficPayload string
	for _, call := range calls {
		var p struct {
			Kind           string `json:"kind"`
			AppID          string `json:"app_id"`
			DeploymentID   string `json:"deployment_id"`
			TrafficPercent int    `json:"traffic_percent"`
		}
		if err := json.Unmarshal([]byte(call.payload), &p); err != nil {
			continue
		}
		if p.Kind == "traffic" {
			trafficPayload = call.payload
			if p.AppID != app.ID {
				t.Errorf("notify app_id = %q, want %q", p.AppID, app.ID)
			}
			if p.DeploymentID != dep.ID {
				t.Errorf("notify deployment_id = %q, want %q", p.DeploymentID, dep.ID)
			}
			if p.TrafficPercent != 25 {
				t.Errorf("notify traffic_percent = %d, want 25", p.TrafficPercent)
			}
			break
		}
	}
	if trafficPayload == "" {
		t.Fatalf("no kind=traffic deployment_changed payload found; calls=%v", calls)
	}
}

// TestPatchDeploymentTraffic_RejectsFreePlan_NoNotify pins that the
// plan-tier gate (issue #556) fires before the notify emit — a Free
// account that PATCHes any value sees the 403 plan_traffic_split_not_allowed
// and no deployment_changed notify is emitted. Without this gate a
// Free plan could spam the gateway with weight-change events it has
// no business triggering.
func TestPatchDeploymentTraffic_RejectsFreePlan_NoNotify(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "traffic-free@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "traffic-free-app", Type: state.AppTypeApp,
		RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def",
		Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	apiKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesDeployWriteSurface); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	notif := &captureNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	handler := srv.handler()

	body := strings.NewReader(`{"traffic_percent":25}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/deployments/"+dep.ID+"/traffic", body)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 plan_traffic_split_not_allowed; body=%s", rec.Code, rec.Body.String())
	}
	if calls := notif.byChannel(db.NotifyDeploymentChanged); len(calls) != 0 {
		t.Errorf("deployment_changed emitted on Free plan: %v", calls)
	}
}
