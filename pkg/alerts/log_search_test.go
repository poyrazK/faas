package alerts_test

// adr: 20261018083536 — log_search_matches counts the app's matching ledger
// events in the rule window, occurrences included.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/state"
)

func seedLogSearchRule(t *testing.T, store *state.MemStore, query string, threshold float64) (state.AlertRule, *age.X25519Identity, func(time.Time) int) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, state.AlertMetricLogSearchMatches, state.AlertGt, threshold)
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.Name, base.LogQuery, base.WindowSpec = "", base.Name+"-logs", query, state.AlertWindow15m
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	return rule, ident, func(now time.Time) int { return evaluateSynthetic(t, store, ident, now).Fired }
}

func insertLogEvent(t *testing.T, store *state.MemStore, rule state.AlertRule, at time.Time, level, message string, occurrences int) {
	t.Helper()
	if _, err := store.InsertLogEvent(context.Background(), state.LogEvent{
		OccurredAt: at, AccountID: rule.AccountID, AppID: rule.AppID, Source: state.LogEventSourceRuntime,
		SourceEventID: fmt.Sprintf("%s-%d", message, at.UnixNano()), Level: level, Message: message, Occurrences: occurrences,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluator_LogSearchMatches(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name      string
		seed      func(*testing.T, *state.MemStore, state.AlertRule)
		wantFired int
	}{
		{"matches over the threshold fire", func(t *testing.T, s *state.MemStore, r state.AlertRule) {
			insertLogEvent(t, s, r, now.Add(-time.Minute), "error", "db timeout", 2)
			insertLogEvent(t, s, r, now.Add(-2*time.Minute), "error", "DB TIMEOUT again", 1)
		}, 1},
		{"other levels and text do not count", func(t *testing.T, s *state.MemStore, r state.AlertRule) {
			insertLogEvent(t, s, r, now.Add(-time.Minute), "warn", "db timeout", 5)
			insertLogEvent(t, s, r, now.Add(-time.Minute), "error", "disk full", 5)
		}, 0},
		{"matches outside the window do not count", func(t *testing.T, s *state.MemStore, r state.AlertRule) {
			insertLogEvent(t, s, r, now.Add(-time.Hour), "error", "db timeout", 5)
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, _, evaluate := seedLogSearchRule(t, store, "level=error&q=timeout", 2)
			tt.seed(t, store, rule)
			if fired := evaluate(now); fired != tt.wantFired {
				t.Fatalf("fired = %d, want %d", fired, tt.wantFired)
			}
		})
	}
}
