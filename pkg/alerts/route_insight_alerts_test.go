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

// insightAlertStore keeps the store in a named field (statetest) and answers
// the route_insights_detected reads.
type insightAlertStore struct {
	MemStore *state.MemStore
	found    []state.RouteInsightDetection
	total    int64
	err      error
	since    []time.Time
}

func (s *insightAlertStore) RouteInsightsDetectedSince(_ context.Context, _ string, since time.Time, limit int) ([]state.RouteInsightDetection, int64, error) {
	s.since = append(s.since, since)
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.found[:min(limit, len(s.found))], s.total, nil
}

// seedInsightRule is a 24h route_insights_detected rule with no webhook that
// delivers to an email channel.
func seedInsightRule(t *testing.T, store *state.MemStore) (state.AlertRule, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, state.AlertMetricRouteInsightsDetected, state.AlertGt, 0)
	email, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: base.AccountID, Name: "me", Kind: "email", Email: "me@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.WindowSpec, base.WebhookURL, base.WebhookSecretSealed = "", state.AlertWindow24h, "", []byte{}
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlertRuleChannels(ctx, rule.ID, []string{email.ID}); err != nil {
		t.Fatal(err)
	}
	return rule, ident
}

// adr: 20261020161310 — route_insights_detected counts the insights that
// became active in the window, and the firing notification carries their
// messages, bounded per note.
func TestEvaluatorRouteInsightsDetected(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("é", 400) // 800 bytes
	for _, tc := range []struct {
		name      string
		total     int64
		err       error
		wantFire  bool
		wantState state.AlertState
	}{
		{name: "new insights fire", total: 7, wantFire: true},
		{name: "none stays quiet", total: 0},
		{name: "unreadable store degrades", err: errors.New("database unavailable"), wantState: state.AlertStateDegraded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := state.NewMemStore()
			rule, ident := seedInsightRule(t, base)
			store := &insightAlertStore{MemStore: base, total: tc.total, err: tc.err, found: []state.RouteInsightDetection{
				{Kind: "kept_awake", Method: "GET", Route: "/health", Message: "GET /health keeps the app awake."},
				{Kind: "cacheable", Method: "GET", Route: "/catalog", Message: long},
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
			if len(store.since) == 0 || !store.since[0].Equal(now.Add(-24*time.Hour)) {
				t.Fatalf("window start = %v, want %v", store.since, now.Add(-24*time.Hour))
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
			if msg.Event != alertchannels.EventFire || msg.Observed != 7 || len(msg.Notes) != 2 || msg.Notes[0] != "GET /health keeps the app awake." {
				t.Fatalf("message = %+v", msg)
			}
			if len(msg.Notes[1]) > 500 || !strings.HasSuffix(msg.Notes[1], "…") || !strings.HasPrefix(msg.Notes[1], "éé") {
				t.Fatalf("long note = %d bytes %q", len(msg.Notes[1]), msg.Notes[1][:20])
			}
		})
	}
}
