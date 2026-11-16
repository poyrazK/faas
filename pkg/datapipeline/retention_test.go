package datapipeline

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRetentionUsesSourceHeadroomAndRunway(t *testing.T) {
	for _, tc := range []struct {
		name             string
		remaining        int64
		seconds          *int64
		pressure, urgent bool
	}{
		{"healthy", 60 << 20, nil, false, false},
		{"small_source_near_limit", 10 << 20, nil, true, false},
		{"slot_has_no_headroom", 0, nil, true, true},
		{"fast_source", 60 << 20, retentionSeconds(20), true, true},
		{"early_runway_warning", 60 << 20, retentionSeconds(180), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := api.DataPipeline{WALRetention: &api.DataPipelineWALRetention{LimitBytes: 64 << 20, RemainingBytes: tc.remaining, EstimatedSecondsRemaining: tc.seconds}}
			if retentionPressure(p) != tc.pressure || retentionUrgent(p) != tc.urgent {
				t.Fatalf("pressure=%v urgent=%v", retentionPressure(p), retentionUrgent(p))
			}
		})
	}
}

func retentionSeconds(n int64) *int64 { return &n }

func TestRetentionEstimateRejectsStaleAndIdleSamples(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name      string
		elapsed   time.Duration
		generated int64
		known     bool
	}{
		{"observed", 15 * time.Second, 150 << 20, true},
		{"idle", 15 * time.Second, 0, false},
		{"rewound_wal", 15 * time.Second, -1, false},
		{"stale", 2 * time.Minute, 150 << 20, false},
		{"future", -time.Second, 150 << 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &walRetentionSample{SampledAt: now, DataPipelineWALRetention: api.DataPipelineWALRetention{RemainingBytes: 200 << 20}}
			s.estimate(&walRetentionSample{SampledAt: now.Add(-tc.elapsed)}, pgtype.Int8{Valid: true, Int64: tc.generated})
			if (s.EstimatedSecondsRemaining != nil) != tc.known {
				t.Fatalf("estimate=%v", s.EstimatedSecondsRemaining)
			}
			if tc.known && *s.EstimatedSecondsRemaining != 20 {
				t.Fatal(*s.EstimatedSecondsRemaining)
			}
		})
	}
}

func TestRetentionUrgencySurvivesCatalogAndSkipsDebounce(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertLive(t, store, p)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(t.Context(), "retention-test")
	if err != nil {
		t.Fatal(err)
	}
	sample := &walRetentionSample{Generation: p.Generation, SourceLSN: "0/1234", SampledAt: time.Now().UTC(), DataPipelineWALRetention: api.DataPipelineWALRetention{LimitBytes: 64 << 20, RemainingBytes: 32 << 20, EstimatedSecondsRemaining: retentionSeconds(20)}}
	retained := int64(32 << 20)
	if err = store.Finish(t.Context(), claimed, Observation{State: "live", RetainedWALBytes: &retained, WALSample: sample}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil || saved.WALRetention == nil || !retentionUrgent(saved.DataPipeline) {
		t.Fatal("lost retention evidence", saved.WALRetention, err)
	}
	public, _ := json.Marshal(saved.DataPipeline)
	if strings.Contains(string(public), "source_lsn") || strings.Contains(string(public), "0/1234") {
		t.Fatal("private cursor escaped")
	}
	alertDue(t, t.Context(), w, p, false)
	if history := alertHistory(t, t.Context(), store, p); len(history) != 1 || history[0].Code != "retention_pressure" {
		t.Fatal("urgent incident waited for debounce", history)
	}
}
