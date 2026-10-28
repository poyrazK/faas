package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// routeAlertStore keeps the store in a named field (statetest) and answers
// the route alert reads.
type routeAlertStore struct {
	MemStore  *state.MemStore
	incidents []state.RouteMonitorIncidentAlert
	holds     []state.RouteHealthHoldAlert
	total     int64
	err       error
	since     []time.Time
}

func (s *routeAlertStore) RouteMonitorIncidentsSince(_ context.Context, _ string, since time.Time, limit int) ([]state.RouteMonitorIncidentAlert, int64, error) {
	s.since = append(s.since, since)
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.incidents[:min(limit, len(s.incidents))], s.total, nil
}

func (s *routeAlertStore) RouteHealthHoldsSince(_ context.Context, _ string, since time.Time, limit int) ([]state.RouteHealthHoldAlert, int64, error) {
	s.since = append(s.since, since)
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.holds[:min(limit, len(s.holds))], s.total, nil
}

// seedRouteAlertRule is a 15m rule on metric with no webhook that delivers
// to an email channel.
func seedRouteAlertRule(t *testing.T, store *state.MemStore, metric state.AlertMetric) (state.AlertRule, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, metric, state.AlertGt, 0)
	email, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: base.AccountID, Name: "me", Kind: "email", Email: "me@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.WindowSpec, base.WebhookURL, base.WebhookSecretSealed = "", state.AlertWindow15m, "", []byte{}
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlertRuleChannels(ctx, rule.ID, []string{email.ID}); err != nil {
		t.Fatal(err)
	}
	return rule, ident
}

// adr: 20261028110044 — route monitor incidents and route health holds reach
// notification channels: a firing rule names what it counted.
func TestEvaluatorRouteAlerts(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-5 * time.Minute)
	for _, tc := range []struct {
		name      string
		metric    state.AlertMetric
		total     int64
		err       error
		wantFire  bool
		wantNote  string
		wantState state.AlertState
	}{
		{name: "open incident fires", metric: state.AlertMetricRouteMonitorIncidents, total: 1, wantFire: true,
			wantNote: "Incident inc-1 (open, opened 2026-10-28 11:55 UTC): POST /checkout error_rate — error budget exceeded"},
		{name: "held canary fires", metric: state.AlertMetricRouteHealthHolds, total: 2, wantFire: true,
			wantNote: "Canary dep-1 held by route health (2026-10-28 11:55 UTC) — selected route lacks evidence"},
		{name: "no incident stays quiet", metric: state.AlertMetricRouteMonitorIncidents},
		{name: "unreadable store degrades", metric: state.AlertMetricRouteHealthHolds, err: errors.New("database unavailable"), wantState: state.AlertStateDegraded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := state.NewMemStore()
			rule, ident := seedRouteAlertRule(t, base, tc.metric)
			store := &routeAlertStore{MemStore: base, total: tc.total, err: tc.err,
				incidents: []state.RouteMonitorIncidentAlert{{ID: "inc-1", Status: "open", Reason: "error budget exceeded", Method: "POST", Path: "/checkout", Signal: "error_rate", OpenedAt: opened}},
				holds: []state.RouteHealthHoldAlert{
					{DeploymentID: "dep-1", Status: "blocked_unknown", Reason: "selected route lacks evidence", UpdatedAt: opened},
					{DeploymentID: "dep-2", Status: "aborted", Reason: strings.Repeat("é", 400), UpdatedAt: opened},
				}}
			channels := &recordingChannels{}
			ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
				Store: statetest.NewMem(store), Audit: audit.New(base, discardLog(), nil, "meterd"),
				Identity: func() *age.X25519Identity { return ident }, Dispatcher: &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}},
				Channels: channels, Now: func() time.Time { return now }, Log: discardLog(),
			})
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if fired := stats.Fired == 1; fired != tc.wantFire {
				t.Fatalf("fired=%v want %v (stats=%+v)", fired, tc.wantFire, stats)
			}
			if len(store.since) == 0 || !store.since[0].Equal(now.Add(-15*time.Minute)) {
				t.Fatalf("window start = %v, want %v", store.since, now.Add(-15*time.Minute))
			}
			if tc.wantState != "" {
				stored, err := base.AlertRuleByID(context.Background(), rule.ID)
				if err != nil || stored.State != tc.wantState {
					t.Fatalf("rule=%+v err=%v", stored, err)
				}
			}
			if !tc.wantFire {
				if len(channels.sent) != 0 {
					t.Fatalf("sent %+v", channels.sent)
				}
				return
			}
			if len(channels.sent) != 1 {
				t.Fatalf("sent %d notifications, want 1", len(channels.sent))
			}
			msg := channels.sent[0].msg
			if msg.Event != alertchannels.EventFire || msg.Observed != float64(tc.total) || len(msg.Notes) == 0 || msg.Notes[0] != tc.wantNote {
				t.Fatalf("message = %+v", msg)
			}
			for _, note := range msg.Notes {
				if len(note) > 500 {
					t.Fatalf("note is %d bytes, want at most 500", len(note))
				}
			}
		})
	}
}
