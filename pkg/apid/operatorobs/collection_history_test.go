package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestAlertCollectionHistoryCoverageAndProspectiveUnknown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.Add(-5 * time.Minute)
	store := state.NewMemStore()
	if err := store.RecordOperatorAlertFailure(t.Context(), since.Add(time.Minute), "timeout"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := alertSnapshot(historyRules(since.Add(90*time.Second), false), since.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertFailure(t.Context(), since.Add(3*time.Minute), "snapshot_rejected"); err != nil {
		t.Fatal(err)
	}
	out, err := AlertCollectionHistory(t.Context(), store, true, since, now, false)
	if err != nil || len(out.Items) != 6 || out.Truncated || out.Coverage.State != "partial" || out.Coverage.Successes != 1 || out.Coverage.Failures != 2 || out.Coverage.SuccessfulObservationMicroseconds != int64(time.Minute/time.Microsecond) || out.Coverage.FailedObservationMicroseconds != int64(90*time.Second/time.Microsecond) || out.Coverage.UnknownMicroseconds != int64(150*time.Second/time.Microsecond) {
		t.Fatalf("coverage: %+v %v", out, err)
	}
	if !out.Items[0].Until.Equal(now) || out.Items[0].State != "unknown" || !out.Items[5].From.Equal(since) || out.Items[5].State != "unknown" {
		t.Fatalf("timeline: %+v", out.Items)
	}
	unknown, err := AlertCollectionHistory(t.Context(), state.NewMemStore(), false, since, now, false)
	if err != nil || unknown.Coverage.State != "unknown" || unknown.Coverage.UnknownMicroseconds != unknown.Coverage.WindowMicroseconds || len(unknown.Items) != 1 || unknown.Collection.CollectionState != "disabled" {
		t.Fatalf("pre-rollout: %+v %v", unknown, err)
	}
	// Carry-in evidence can cover the boundary without counting as a new
	// attempt in the requested window. It expires at its original deadline.
	from := since.Add(105 * time.Second)
	clipped, err := AlertCollectionHistory(t.Context(), store, true, from, from.Add(30*time.Second), false)
	if err != nil || clipped.Coverage.Successes != 0 || clipped.Coverage.Failures != 0 || clipped.Coverage.State != "observed" || clipped.TotalIntervals != 1 {
		t.Fatalf("carry-in: %+v %v", clipped, err)
	}
	// Failed collection does not affect retained episode recovery or triage.
	response, err := AlertResponseTimes(t.Context(), store, true, since, now)
	if err != nil || response.Coverage == nil || response.Coverage.UnknownMicroseconds != out.Coverage.UnknownMicroseconds {
		t.Fatalf("timing coverage: %+v %v", response, err)
	}
	recurrence, err := AlertRecurrence(t.Context(), store, true, since, now)
	if err != nil || recurrence.Coverage == nil || recurrence.Coverage.FailedObservationMicroseconds != out.Coverage.FailedObservationMicroseconds {
		t.Fatalf("recurrence coverage: %+v %v", recurrence, err)
	}
}

func TestAlertCollectionHistoryPreviewAndWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.Add(-time.Hour)
	store := state.NewMemStore()
	for i := 0; i < api.ObsAlertCollectionHistoryIntervalsMax+5; i++ {
		reason := "timeout"
		if i%2 == 0 {
			reason = "source_read_failed"
		}
		if err := store.RecordOperatorAlertFailure(t.Context(), since.Add(time.Duration(i)*time.Second), reason); err != nil {
			t.Fatal(err)
		}
	}
	out, err := AlertCollectionHistory(t.Context(), store, true, since, now, false)
	if err != nil || !out.Truncated || len(out.Items) != out.Limit || out.TotalIntervals != 206 || out.Coverage.Failures != 205 || out.Coverage.WindowMicroseconds != out.Coverage.SuccessfulObservationMicroseconds+out.Coverage.FailedObservationMicroseconds+out.Coverage.UnknownMicroseconds {
		t.Fatalf("bounded preview: %+v %v", out, err)
	}
	if def, err := CollectionHistorySince("", now); err != nil || !def.Equal(now.Add(-api.ObsAlertCollectionHistoryWindowDefault)) {
		t.Fatalf("default: %v %v", def, err)
	}
	for _, bad := range []string{"bad", now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano), now.Add(-api.ObsAlertCollectionHistoryRetention - time.Microsecond).Format(time.RFC3339Nano)} {
		if _, err := CollectionHistorySince(bad, now); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("bad window %s: %v", bad, err)
		}
	}
}

func TestAlertCollectionCoveragePartition(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.Add(-10 * time.Minute)
	for seed := 1; seed <= 25; seed++ {
		rows := []state.OperatorAlertCollectionOutcome{}
		for i := 1; i < 10; i++ {
			status, reason := "success", ""
			if i%seed == 0 {
				status, reason = "failure", "timeout"
			}
			rows = append(rows, state.OperatorAlertCollectionOutcome{AttemptedAt: since.Add(time.Duration(i*53+seed) * time.Second), Outcome: status, Reason: reason})
		}
		c, items, err := collectionCoverage(rows, since, now)
		if err != nil || c.WindowMicroseconds != c.SuccessfulObservationMicroseconds+c.FailedObservationMicroseconds+c.UnknownMicroseconds {
			t.Fatalf("seed %d: %+v %v", seed, c, err)
		}
		cursor := since
		for _, item := range items {
			if !item.From.Equal(cursor) || !item.Until.After(item.From) {
				t.Fatalf("timeline gap: %+v", items)
			}
			cursor = item.Until
		}
		if !cursor.Equal(now) {
			t.Fatal("tail lost")
		}
	}
	bad := []state.OperatorAlertCollectionOutcome{{AttemptedAt: since, Outcome: "failure", Reason: "private-error"}}
	if _, _, err := collectionCoverage(bad, since, now); err == nil {
		t.Fatal("raw error accepted")
	}
}

func TestAlertCollectionOutcomeSanitizedFailuresAndTimeout(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, mode := range []string{"source_read_failed", "snapshot_rejected", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "timeout":
					<-r.Context().Done()
					return
				case "source_read_failed":
					http.Error(w, "private-body", 503)
				default:
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": []promql.AlertRuleGroup{}}})
				}
			}))
			defer srv.Close()
			// Exercise an expired poll context, while its parent remains live.
			err := CollectAlertsOnce(t.Context(), store, promql.NewClient(srv.URL, srv.Client()), now)
			if err == nil || strings.Contains(err.Error(), "private-body") {
				t.Fatalf("failure: %v", err)
			}
			h, err := store.OperatorAlertCollectionHistory(t.Context(), state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now})
			if err != nil || len(h.Outcomes) != 1 || h.Outcomes[0].Reason != mode {
				t.Fatalf("receipt %s: %+v %v", mode, h, err)
			}
		})
	}
}

type failedSnapshotCollector struct{ Store state.Store }

func (p *failedSnapshotCollector) RecordOperatorAlertSnapshot(context.Context, state.OperatorAlertSnapshot) error {
	return errors.New("private database failure")
}

func TestAlertCollectionPersistenceFailureReceipt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := state.NewMemStore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": historyRules(now, true)}})
	}))
	defer srv.Close()
	adapter := statetest.NewStore(&failedSnapshotCollector{Store: store})
	if err := CollectAlertsOnce(t.Context(), adapter, promql.NewClient(srv.URL, srv.Client()), now); err == nil {
		t.Fatal("persistence failure ignored")
	}
	h, err := store.OperatorAlertCollectionHistory(t.Context(), state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now})
	if err != nil || len(h.Outcomes) != 1 || h.Outcomes[0].Reason != "persistence_failed" || !h.Collection.LastSuccessAt.IsZero() {
		t.Fatalf("failed write: %+v %v", h, err)
	}
}
