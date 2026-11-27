package alerts_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// Observe audit 2026-11-21 (finding 18): OOM kills were visible only in
// `gregale debug status`; no alert rule could watch them.
func TestEvaluatorOOMKills(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name     string
		kills    []time.Duration // ago
		wantFire bool
	}{
		{name: "a kill inside the window fires", kills: []time.Duration{5 * time.Minute, 10 * time.Minute}, wantFire: true},
		{name: "a kill before the window stays quiet", kills: []time.Duration{time.Hour}},
		{name: "no kills stay quiet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident := seedRouteAlertRule(t, store, state.AlertMetricOOMKills)
			for _, ago := range tc.kills {
				data, _ := json.Marshal(map[string]any{"app_id": rule.AppID, "peak_mb": 270, "plan_mb": 256})
				if err := store.AppendEventAt(context.Background(), "vmmd", "instances.workload_oom_failed", nil, data, now.Add(-ago)); err != nil {
					t.Fatal(err)
				}
			}
			// Another app's kill is not this rule's.
			other, _ := json.Marshal(map[string]any{"app_id": "other-app"})
			if err := store.AppendEventAt(context.Background(), "vmmd", "instances.workload_oom_failed", nil, other, now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			channels := &recordingChannels{}
			ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
				Store: store, Audit: audit.New(store, discardLog(), nil, "meterd"),
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
			if tc.wantFire && (len(channels.sent) != 1 || channels.sent[0].msg.Observed != float64(len(tc.kills))) {
				t.Fatalf("sent %+v, want one notification observing %d kills", channels.sent, len(tc.kills))
			}
		})
	}
}
