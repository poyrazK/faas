package alerts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// aiBudgetAlertStore keeps the store in a named field (statetest) and
// answers the AI budget alert read.
type aiBudgetAlertStore struct {
	MemStore  *state.MemStore
	crossings []state.AIBudgetAlert
	total     int64
	err       error
	since     []time.Time
	accounts  []string
}

func (s *aiBudgetAlertStore) AIBudgetAlertsSince(_ context.Context, accountID, _ string, since time.Time, limit int) ([]state.AIBudgetAlert, int64, error) {
	s.since, s.accounts = append(s.since, since), append(s.accounts, accountID)
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.crossings[:min(limit, len(s.crossings))], s.total, nil
}

// adr: 20261027132936 — amendment 2: AI budget threshold crossings reach
// notification channels; a firing rule names each crossing.
func TestEvaluatorAIBudgetAlerts(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	crossed := now.Add(-5 * time.Minute)
	for _, tc := range []struct {
		name      string
		total     int64
		err       error
		wantFire  bool
		wantNotes []string
		wantState state.AlertState
	}{
		{name: "crossings fire", total: 2, wantFire: true, wantNotes: []string{
			"llm daily_cost budget at 80% (USD 20.00000 of USD 25.00000, 2026-11-12 11:55 UTC)",
			"llm user_daily_tokens budget at 100% (5000 tokens of 5000 tokens, 2026-11-12 11:55 UTC) for end user user-1",
		}},
		{name: "no crossing stays quiet"},
		{name: "unreadable store degrades", err: errors.New("database unavailable"), wantState: state.AlertStateDegraded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := state.NewMemStore()
			rule, ident := seedRouteAlertRule(t, base, state.AlertMetricAIBudgetAlerts)
			store := &aiBudgetAlertStore{MemStore: base, total: tc.total, err: tc.err, crossings: []state.AIBudgetAlert{
				{ID: "a1", IntegrationName: "llm", Budget: "daily_cost", ThresholdPercent: 80, Limit: 2_500_000, Used: 2_000_000, CrossedAt: crossed},
				{ID: "a2", IntegrationName: "llm", Budget: "user_daily_tokens", ThresholdPercent: 100, Limit: 5_000, Used: 5_000, EndUser: "user-1", CrossedAt: crossed},
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
			if len(store.since) == 0 || !store.since[0].Equal(now.Add(-15*time.Minute)) || store.accounts[0] != rule.AccountID {
				t.Fatalf("read since %v for %v, want %v for %s", store.since, store.accounts, now.Add(-15*time.Minute), rule.AccountID)
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
			if msg.Event != alertchannels.EventFire || msg.Observed != float64(tc.total) || len(msg.Notes) != len(tc.wantNotes) {
				t.Fatalf("message = %+v", msg)
			}
			for i, note := range tc.wantNotes {
				if msg.Notes[i] != note {
					t.Fatalf("note %d = %q, want %q", i, msg.Notes[i], note)
				}
			}
		})
	}
}
