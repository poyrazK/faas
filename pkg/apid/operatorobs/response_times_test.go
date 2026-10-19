package operatorobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertResponseTimesAggregationAndCoverage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := now.Add(-time.Minute)
	ack := start
	recovered := start.Add(30 * time.Second)
	negative := start.Add(-time.Microsecond)
	future := now.Add(time.Microsecond)
	episodes := []state.OperatorAlertResponseTimesEpisode{
		{Severity: "critical", Component: "apid", StartedAt: start, FirstAcknowledgedAt: &ack, ResolvedAt: &recovered, ReceiptCoverage: "complete", Status: "resolved"},
		{Severity: "critical", Component: "apid", StartedAt: start, ReceiptCoverage: "gaps", MissingRevisions: 1, Status: "unknown"},
		{Severity: "warning", Component: "gateway", StartedAt: start, ReceiptCoverage: "invalid", Status: "firing"},
		{Severity: "warning", Component: "gateway", StartedAt: start, ReceiptCoverage: "complete", AckClockInvalid: true, ResolvedAt: &negative, Status: "resolved"},
		{Severity: "warning", Component: "gateway", StartedAt: start, ReceiptCoverage: "complete", ResolvedAt: &future, Status: "resolved"},
	}
	reader := &responseTimingProbe{snapshot: state.OperatorAlertResponseTimesSnapshot{Collection: state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}, Episodes: episodes}}
	out, err := AlertResponseTimes(context.Background(), reader, true, now.Add(-time.Hour), now)
	if err != nil || out.Totals == nil || len(out.Groups) != 2 || !reader.bounded {
		t.Fatalf("aggregate: %+v %v", out, err)
	}
	a := out.Totals
	if a.Episodes != 5 || a.Unresolved != 2 || a.UnknownObservation != 1 || a.Acknowledgment.Samples != 1 || a.Acknowledgment.Unknown != 2 || a.Acknowledgment.InvalidClock != 1 || a.Acknowledgment.NotObserved != 1 || a.Acknowledgment.P50Seconds == nil || *a.Acknowledgment.P50Seconds != 0 || a.Resolution.Samples != 1 || *a.Resolution.P95Seconds != 30 || a.Resolution.NotObserved != 2 || a.Resolution.InvalidClock != 2 || a.ReceiptCoverage.MissingRevisions != 1 {
		t.Fatalf("denominators: %+v", a)
	}
	for _, m := range []api.ObsAlertResponseMetric{a.Acknowledgment, a.Resolution} {
		if m.Samples+m.NotObserved+m.Unknown+m.InvalidClock != a.Episodes {
			t.Fatal("metric partition lost episodes")
		}
	}
	if out.Groups[0].Severity != "critical" || out.Groups[0].Metrics.Episodes != 2 || out.Groups[1].Metrics.Episodes != 3 || len(out.Limitations) == 0 || out.PercentileMethod != "nearest_rank_whole_seconds" {
		t.Fatalf("groups: %+v", out)
	}
	reader.snapshot.Collection = state.OperatorAlertCollection{}
	never, err := AlertResponseTimes(context.Background(), reader, true, out.Since, now)
	if err != nil || never.Totals != nil || never.Groups == nil {
		t.Fatalf("unknown: %+v %v", never, err)
	}
	// A successful quiet cohort is known zero, with absent percentile samples.
	reader.snapshot.Collection = state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}
	reader.snapshot.Episodes = nil
	quiet, err := AlertResponseTimes(context.Background(), reader, true, out.Since, now)
	if err != nil || quiet.Totals == nil || quiet.Totals.Episodes != 0 || quiet.Totals.Acknowledgment.P50Seconds != nil {
		t.Fatalf("quiet: %+v %v", quiet, err)
	}
}

type responseTimingProbe struct {
	snapshot state.OperatorAlertResponseTimesSnapshot
	bounded  bool
}

func (p *responseTimingProbe) OperatorAlertResponseTimes(ctx context.Context, _ state.OperatorAlertResponseTimesFilter) (state.OperatorAlertResponseTimesSnapshot, error) {
	deadline, ok := ctx.Deadline()
	p.bounded = ok && time.Until(deadline) <= api.ObsAlertResponseTimesBudget
	return p.snapshot, ctx.Err()
}

func TestAlertResponseTimesWindowAndPercentiles(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	since, err := ResponseTimesSince("", now)
	if err != nil || !since.Equal(now.Add(-api.ObsAlertResponseTimesWindowDefault)) {
		t.Fatalf("default: %v %v", since, err)
	}
	if _, err := ResponseTimesSince(now.Add(-api.ObsAlertResponseTimesWindowMax).Format(time.RFC3339Nano), now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"garbage", now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano), now.Add(-api.ObsAlertResponseTimesWindowMax - time.Microsecond).Format(time.RFC3339Nano)} {
		if _, err := ResponseTimesSince(bad, now); err == nil {
			t.Fatalf("bad window accepted: %s", bad)
		}
	}
	p50, p95 := responsePercentiles([]int64{30, 0, 20, 10})
	if p50 == nil || *p50 != 10 || *p95 != 30 {
		t.Fatalf("nearest rank: %v %v", p50, p95)
	}
	if a, b := responsePercentiles(nil); a != nil || b != nil {
		t.Fatal("empty sample manufactured zero")
	}
}

func TestAlertResponseTimesRealStoreSanitizes(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now().UTC().Truncate(time.Microsecond)
	snapshot, err := alertSnapshot(historyRules(now, true), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateOperatorAlertTriage(context.Background(), state.OperatorAlertTriageMutation{EpisodeID: snapshot.Episodes[0].ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: "acknowledge", Now: now}); err != nil {
		t.Fatal(err)
	}
	out, err := AlertResponseTimes(context.Background(), store, false, now.Add(-time.Hour), now)
	if err != nil || out.Totals.Acknowledgment.Samples != 1 || *out.Totals.Acknowledgment.P50Seconds != 60 || out.Totals.UnknownObservation != 1 {
		t.Fatalf("real store: %+v %v", out, err)
	}
	body, err := json.Marshal(out)
	if err != nil || strings.Contains(string(body), "11111111-1111-4111-8111-111111111111") || strings.Contains(string(body), "owner_account_id") {
		t.Fatalf("identity leak: %s %v", body, err)
	}
}
