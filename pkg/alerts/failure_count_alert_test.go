package alerts_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func runFailureCountEvaluator(t *testing.T, store state.Store, ident *age.X25519Identity, now time.Time) (alerts.Stats, *recordingChannels) {
	t.Helper()
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
	return stats, channels
}

// boot_failures counts the rule's app's wake.boot_failed events inside the
// window; support saw these as boot_failing attention signals only.
func TestEvaluatorBootFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name     string
		failures []time.Duration // ago
		wantFire bool
	}{
		{name: "failed boots inside the window fire", failures: []time.Duration{time.Minute, 5 * time.Minute}, wantFire: true},
		{name: "a failed boot before the window stays quiet", failures: []time.Duration{time.Hour}},
		{name: "no failed boots stay quiet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident := seedRouteAlertRule(t, store, state.AlertMetricBootFailures)
			for _, ago := range tc.failures {
				data, _ := json.Marshal(map[string]any{"app_id": rule.AppID, "reason": "vmm_boot_failed"})
				if err := store.AppendEventAt(context.Background(), "schedd", "wake.boot_failed", nil, data, now.Add(-ago)); err != nil {
					t.Fatal(err)
				}
			}
			// None of these are this rule's: another app's failed boot, a
			// mirror-mode boot of a canary candidate, and schedd failing to
			// publish runtime state (a platform cause).
			for _, payload := range []map[string]any{
				{"app_id": "other-app", "reason": "vmm_boot_failed"},
				{"app_id": rule.AppID, "reason": "vmm_boot_failed", "mirror": true},
				{"app_id": rule.AppID, "reason": "record_runtime_failed"},
			} {
				data, _ := json.Marshal(payload)
				if err := store.AppendEventAt(context.Background(), "schedd", "wake.boot_failed", nil, data, now.Add(-time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			stats, channels := runFailureCountEvaluator(t, store, ident, now)
			if fired := stats.Fired == 1; fired != tc.wantFire {
				t.Fatalf("fired=%v want %v (stats=%+v)", fired, tc.wantFire, stats)
			}
			if tc.wantFire && (len(channels.sent) != 1 || channels.sent[0].msg.Observed != float64(len(tc.failures))) {
				t.Fatalf("sent %+v, want one notification observing %d failed boots", channels.sent, len(tc.failures))
			}
		})
	}
}

// failedJobRunStore answers job_failures from a fixed count, so the test
// does not drive a job run through the scheduler.
type failedJobRunStore struct {
	MemStore  *state.MemStore
	n         int64
	err       error
	account   string
	since     time.Time
	until     time.Time
	callCount int
}

func (s *failedJobRunStore) CountAccountFailedJobRuns(_ context.Context, accountID string, since, until time.Time) (int64, error) {
	s.callCount++
	s.account, s.since, s.until = accountID, since, until
	return s.n, s.err
}

// job_failures counts the account's failed and dead-lettered job runs in the
// window; a read error degrades the rule instead of reporting zero.
func TestEvaluatorJobFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name      string
		n         int64
		err       error
		wantFire  bool
		wantState state.AlertState
	}{
		{name: "a failed run fires", n: 2, wantFire: true, wantState: state.AlertStateFiring},
		{name: "no failed runs stay quiet", wantState: state.AlertStateOk},
		{name: "a read error degrades", err: errors.New("boom"), wantState: state.AlertStateDegraded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := state.NewMemStore()
			rule, ident := seedRouteAlertRule(t, mem, state.AlertMetricJobFailures)
			store := &failedJobRunStore{MemStore: mem, n: tc.n, err: tc.err}
			stats, channels := runFailureCountEvaluator(t, statetest.NewMem(store), ident, now)
			if fired := stats.Fired == 1; fired != tc.wantFire {
				t.Fatalf("fired=%v want %v (stats=%+v)", fired, tc.wantFire, stats)
			}
			if store.callCount != 1 || store.account != rule.AccountID || !store.until.Equal(now) || !store.since.Equal(now.Add(-15*time.Minute)) {
				t.Fatalf("counted account=%q [%s, %s] x%d, want account %q over the 15m window", store.account, store.since, store.until, store.callCount, rule.AccountID)
			}
			if tc.wantFire && (len(channels.sent) != 1 || channels.sent[0].msg.Observed != float64(tc.n)) {
				t.Fatalf("sent %+v, want one notification observing %d failed runs", channels.sent, tc.n)
			}
			got, err := mem.AlertRuleByID(context.Background(), rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.wantState {
				t.Fatalf("state=%q want %q", got.State, tc.wantState)
			}
		})
	}
}

// The in-memory counts match the PgStore queries' filters.
func TestMemStoreFailureCounts(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	store := state.NewMemStore()
	data, _ := json.Marshal(map[string]any{"app_id": "app-1", "reason": "vmm_boot_failed"})
	for _, at := range []time.Time{now.Add(-time.Minute), now.Add(-2 * time.Hour)} {
		if err := store.AppendEventAt(ctx, "schedd", "wake.boot_failed", nil, data, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendEventAt(ctx, "vmmd", "wake.boot_complete", nil, data, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	n, err := store.CountAppBootFailures(ctx, "app-1", now.Add(-time.Hour), now)
	if err != nil || n != 1 {
		t.Fatalf("CountAppBootFailures = %d, %v; want 1", n, err)
	}
	if n, err := store.CountAccountFailedJobRuns(ctx, "acct-1", now.Add(-time.Hour), now); err != nil || n != 0 {
		t.Fatalf("CountAccountFailedJobRuns = %d, %v; want 0 with no runs", n, err)
	}
}
