package alerts_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
)

type previewAnomalyProm struct {
	anomalyProm
	calls    int
	misalign bool
}

func (p *previewAnomalyProm) QueryRangeStrict(_ context.Context, query, start, end, step string) ([]promql.QueryRangeSeries, error) {
	p.calls++
	first, firstErr := strconv.ParseInt(start, 10, 64)
	last, lastErr := strconv.ParseInt(end, 10, 64)
	const perDay = 24
	const sampleCount = api.AnomalyPreviewChecks + api.AnomalyBaselineDays*perDay
	if firstErr != nil || lastErr != nil || last-first != (sampleCount-1)*3600 || step != "3600" ||
		!strings.Contains(query, `gateway_request_duration_seconds_count{app="orders"}[15m]`) ||
		!strings.Contains(query, "count_over_time(") || !strings.Contains(query, "vector(-1)") {
		return nil, fmt.Errorf("incorrect preview range or coverage query: %s %s %s %s", start, end, step, query)
	}
	samples := make([]promql.QueryRangeSample, sampleCount)
	for i := range samples {
		samples[i] = promql.QueryRangeSample{Timestamp: first + int64(i)*3600, Value: 100}
	}
	const firstCheck = api.AnomalyBaselineDays * perDay
	for day := 0; day < api.AnomalyBaselineDays; day++ {
		if day < 3 {
			samples[day*perDay+2].Value = -1
		}
		samples[day*perDay+3].Value = 0
		samples[day*perDay+4].Value = 40
	}
	samples[firstCheck+1].Value = 300
	samples[firstCheck+2].Value = 600
	samples[firstCheck+3].Value = 600
	samples[firstCheck+4].Value = 0
	samples[firstCheck+5].Value = 0
	samples[firstCheck+6].Value = -1
	samples[firstCheck+6*perDay+7].Value = 40
	if p.misalign {
		samples[firstCheck].Timestamp++
	}
	return []promql.QueryRangeSeries{{Values: samples}}, nil
}

func TestPreviewRequestCountAnomaly(t *testing.T) {
	now := time.Date(2026, 11, 22, 11, 34, 0, 0, time.UTC)
	last := now.Truncate(time.Hour)
	first := last.Add(-(7*24 - 1) * time.Hour)
	for _, tc := range []struct {
		comparison        string
		multiplier        float64
		candidate         float64
		candidateBreaches int
		hour              int
		observed, trigger float64
	}{
		{api.AlertComparisonAboveBaseline, 3, 4, 0, 1, 300, 300},
		{api.AlertComparisonBelowBaseline, 0.25, 0.5, 2, 5, 0, 25},
	} {
		t.Run(tc.comparison, func(t *testing.T) {
			prom := &previewAnomalyProm{}
			config := alerts.AnomalyPreviewComparison{Comparison: tc.comparison, Threshold: tc.multiplier, CandidateThreshold: &tc.candidate}
			got, err := alerts.PreviewAnomaly(t.Context(), prom, "orders", "15m", config, now)
			if err != nil || prom.calls != 1 || got.Evaluated != 161 || got.Skipped != 7 ||
				len(got.SkippedWindows) != 7 || len(got.Breaches) != 1 ||
				!got.FirstCheckAt.Equal(first) || !got.LastCheckAt.Equal(last) || got.StepSeconds != 3600 {
				t.Fatalf("preview=%+v queries=%d err=%v, want 161 evaluated, 7 skipped, and one breach", got, prom.calls, err)
			}
			want := api.AlertAnomalyPreviewBreach{EvaluatedAt: first.Add(time.Duration(tc.hour) * time.Hour), Observed: tc.observed, Baseline: 100, TriggerValue: tc.trigger}
			if got.Breaches[0] != want {
				t.Fatalf("breach=%+v, want %+v", got.Breaches[0], want)
			}
			wantHistory := api.AlertAnomalyPreviewSkippedWindow{EvaluatedAt: first.Add(2 * time.Hour), Reason: api.AlertAnomalyInsufficientHistory}
			wantTelemetry := api.AlertAnomalyPreviewSkippedWindow{EvaluatedAt: first.Add(6 * time.Hour), Reason: api.AlertAnomalyTelemetryMissing}
			if got.SkippedWindows[0] != wantHistory || got.SkippedWindows[2] != wantTelemetry {
				t.Fatalf("skipped windows=%+v, want first=%+v and third=%+v", got.SkippedWindows, wantHistory, wantTelemetry)
			}
			if got.Candidate == nil || got.Candidate.Threshold != tc.candidate || got.Candidate.Breaches == nil || len(got.Candidate.Breaches) != tc.candidateBreaches {
				t.Fatalf("candidate=%+v, want %g with %d breaches", got.Candidate, tc.candidate, tc.candidateBreaches)
			}
			if tc.comparison == api.AlertComparisonBelowBaseline {
				wantShared := want
				wantShared.TriggerValue = 50
				wantLate := api.AlertAnomalyPreviewBreach{
					EvaluatedAt: first.Add((6*24 + 7) * time.Hour), Observed: 40, Baseline: 100, TriggerValue: 50,
				}
				if got.Candidate.Breaches[0] != wantShared || got.Candidate.Breaches[1] != wantLate {
					t.Fatalf("candidate breaches=%+v, want %+v and %+v", got.Candidate.Breaches, wantShared, wantLate)
				}
			}
		})
	}
	config := alerts.AnomalyPreviewComparison{Comparison: api.AlertComparisonAboveBaseline, Threshold: 3}
	if _, err := alerts.PreviewAnomaly(t.Context(), &previewAnomalyProm{misalign: true}, "orders", "15m", config, now); err == nil {
		t.Fatal("accepted a time series with a misaligned checkpoint")
	}
}

type incidentReplayRange struct {
	first time.Time
	step  time.Duration
	count int
}

type incidentReplayProm struct {
	anomalyProm
	ranges   []incidentReplayRange
	countAt  func(time.Time) float64
	rateAt   func(time.Time) float64
	failedAt func(time.Time) float64
	p95At    func(time.Time) float64
}

func (p *incidentReplayProm) QueryRangeStrict(_ context.Context, query, start, end, step string) ([]promql.QueryRangeSeries, error) {
	first, firstErr := strconv.ParseInt(start, 10, 64)
	last, lastErr := strconv.ParseInt(end, 10, 64)
	seconds, stepErr := strconv.ParseInt(step, 10, 64)
	valueAt := p.countAt
	validQuery := strings.Contains(query, `gateway_request_duration_seconds_count{app="orders"}[`) && strings.Contains(query, "vector(-1)")
	if p.rateAt != nil {
		switch {
		case query == appmetrics.ErrorRatePctQuery("orders", "5m"):
			valueAt, validQuery = p.rateAt, true
		case p.failedAt != nil && query == appmetrics.FailedRequestsQuery("increase", "orders", "5m")+" or vector(0)":
			valueAt, validQuery = p.failedAt, true
		default:
			validQuery = strings.Contains(query, appmetrics.EligibleRequestsQuery("increase", "orders", "5m")) && strings.Contains(query, "vector(-1)")
		}
	}
	if p.p95At != nil {
		switch {
		case strings.Contains(query, "histogram_quantile(0.95,") &&
			strings.Contains(query, `gateway_request_duration_seconds_bucket{app="orders",class="2xx"}[5m]`):
			valueAt, validQuery = p.p95At, true
		default:
			validQuery = strings.Contains(query, `increase(gateway_request_duration_seconds_count{app="orders",class="2xx"}[5m])`) &&
				strings.Contains(query, "vector(-1)")
		}
	}
	if firstErr != nil || lastErr != nil || stepErr != nil || seconds <= 0 || !validQuery {
		return nil, fmt.Errorf("incorrect incident replay query: %s %s %s %s", start, end, step, query)
	}
	at := time.Unix(first, 0).UTC()
	duration := time.Duration(seconds) * time.Second
	count := int((last-first)/seconds) + 1
	p.ranges = append(p.ranges, incidentReplayRange{at, duration, count})
	samples := make([]promql.QueryRangeSample, count)
	for i := range samples {
		check := at.Add(time.Duration(i) * duration)
		samples[i] = promql.QueryRangeSample{Timestamp: check.Unix(), Value: valueAt(check)}
	}
	return []promql.QueryRangeSeries{{Values: samples}}, nil
}

func TestReplayP95AnomalyUsesSuccessfulRequestsAndLatencyFloor(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(minute int) time.Time { return start.Add(time.Duration(minute) * time.Minute) }
	fake := &incidentReplayProm{
		countAt: func(check time.Time) float64 {
			switch check {
			case at(14):
				return 49
			case at(15):
				return -1
			}
			for day := 1; day <= 3; day++ {
				if check.Equal(at(55).Add(-time.Duration(day) * 24 * time.Hour)) {
					return 49
				}
			}
			return 50
		},
		p95At: func(check time.Time) float64 {
			switch check {
			case at(13), at(14), at(55):
				return 400
			case at(17):
				return 49
			}
			if check.Before(start) && check.Hour() == start.Hour() && check.Minute() == 17 {
				return 10
			}
			return 100
		},
	}
	config := alerts.AnomalyPreviewComparison{Metric: "latency_p95_ms", Comparison: api.AlertComparisonAboveBaseline, Threshold: 3}
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), fake, "orders", "5m", config, start, 15)
	if err != nil || len(fake.ranges) != 16 || got.Evaluated != 117 || got.Skipped != 3 ||
		len(got.Breaches) != 1 || len(got.SkippedWindows) != 3 {
		t.Fatalf("p95 replay=%+v reads=%d err=%v, want one qualifying breach and three skipped checks", got, len(fake.ranges), err)
	}
	breach := got.Breaches[0]
	if !breach.EvaluatedAt.Equal(at(13)) || breach.Observed != 400 || breach.Baseline != 100 || breach.TriggerValue != 300 ||
		breach.EligibleRequests == nil || *breach.EligibleRequests != 50 {
		t.Fatalf("breach=%+v, want 400 ms with 50 successful requests against a 100 ms baseline", breach)
	}
	low, missing, history := got.SkippedWindows[0], got.SkippedWindows[1], got.SkippedWindows[2]
	if !low.EvaluatedAt.Equal(at(14)) || low.Reason != api.AlertAnomalyInsufficientTraffic ||
		low.EligibleRequests == nil || *low.EligibleRequests != 49 || low.RequiredRequests != 50 ||
		missing.Reason != api.AlertAnomalyTelemetryMissing || history.Reason != api.AlertAnomalyInsufficientHistory {
		t.Fatalf("skips=%+v, want distinct low-traffic, missing telemetry and incomplete history checks", got.SkippedWindows)
	}
	if got.IncidentReplay == nil || len(got.IncidentReplay.Incidents) != 1 {
		t.Fatalf("incidents=%+v, want one p95 incident", got.IncidentReplay)
	}
	incident := got.IncidentReplay.Incidents[0]
	if !incident.OpenedAt.Equal(at(13)) || incident.EndedAt == nil || !incident.EndedAt.Equal(at(16)) {
		t.Fatalf("incident=%+v, want recovery after the skipped checks", incident)
	}
}

func TestReplayErrorRateAnomalyZeroBaselineFallback(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(minute int) time.Time { return start.Add(time.Duration(minute) * time.Minute) }
	type requestTally struct{ requests, failures float64 }
	counts := map[time.Time]requestTally{
		at(13): {100, 10},
		at(14): {50, 4},
		at(15): {200, 5},
		at(19): {100, 5},
		at(21): {100, 3},
		at(31): {40, 10},
		at(41): {100, 10},
	}
	fake := &incidentReplayProm{
		countAt: func(check time.Time) float64 {
			if count, ok := counts[check]; ok {
				return count.requests
			}
			for day := 1; day <= 3; day++ {
				if check.Equal(at(41).Add(-time.Duration(day) * 24 * time.Hour)) {
					return 10
				}
			}
			return 100
		},
		rateAt: func(check time.Time) float64 {
			if count, ok := counts[check]; ok {
				return 100 * count.failures / count.requests
			}
			if check.Before(start) && check.Hour() == start.Hour() && check.Minute() == 21 {
				return 1
			}
			return 0
		},
		failedAt: func(check time.Time) float64 { return counts[check].failures },
	}
	config := alerts.AnomalyPreviewComparison{
		Metric: "error_rate_pct", Comparison: api.AlertComparisonAboveBaseline, Threshold: 3,
		ZeroBaselineFallback: true,
	}
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), fake, "orders", "5m", config, start, 15)
	if err != nil || len(fake.ranges) != 17 || got.Candidate == nil || got.Candidate.ZeroBaselineFallback == nil ||
		len(got.Breaches) != 1 || len(got.Candidate.Breaches) != 3 || got.IncidentReplay == nil {
		t.Fatalf("replay=%+v reads=%d err=%v, want one normal breach and two additional fallback breaches", got, len(fake.ranges), err)
	}
	if want := (incidentReplayRange{start, time.Minute, 120}); fake.ranges[16] != want {
		t.Fatalf("failure range=%+v, want current checks only: %+v", fake.ranges[16], want)
	}
	if fallback := got.Candidate.ZeroBaselineFallback; fallback.MinimumErrorRatePct != 5 || fallback.MinimumFailedRequests != 5 || got.Candidate.Threshold != 3 {
		t.Fatalf("candidate=%+v, want a 5%% and five-failure fallback with the original multiplier", got.Candidate)
	}
	for i, minute := range []int{13, 19} {
		breach := got.Candidate.Breaches[i]
		if !breach.EvaluatedAt.Equal(at(minute)) || !breach.ZeroBaselineFallback || breach.TriggerValue != 5 ||
			breach.Baseline != 0 || breach.FailedRequests == nil || float64(*breach.FailedRequests) != counts[at(minute)].failures {
			t.Errorf("fallback breach at minute %d=%+v", minute, breach)
		}
	}
	normal := got.Candidate.Breaches[2]
	if primary := got.Breaches[0]; !primary.EvaluatedAt.Equal(normal.EvaluatedAt) ||
		primary.Observed != normal.Observed || primary.Baseline != normal.Baseline || primary.TriggerValue != normal.TriggerValue {
		t.Fatalf("regular breach=%+v candidate=%+v, want the same multiplier decision at a nonzero baseline", primary, normal)
	}
	if normal.ZeroBaselineFallback || !normal.EvaluatedAt.Equal(at(21)) || normal.TriggerValue != 3 ||
		len(got.IncidentReplay.CandidateIncidents) != 3 || !got.IncidentReplay.CandidateIncidents[2].OpenedAt.Equal(at(13)) {
		t.Fatalf("regular breach=%+v incidents=%+v, want the candidate's first incident to open on the first qualifying fallback", normal, got.IncidentReplay.CandidateIncidents)
	}
	if got.Skipped != 2 || got.SkippedWindows[0].Reason != api.AlertAnomalyInsufficientTraffic ||
		got.SkippedWindows[1].Reason != api.AlertAnomalyInsufficientHistory {
		t.Fatalf("skips=%+v, want the traffic and history requirements to apply to the candidate", got.SkippedWindows)
	}

	counts[at(60)] = requestTally{100, 10}
	fake.ranges = nil
	overview, err := alerts.PreviewAnomaly(t.Context(), fake, "orders", "5m", config, at(120))
	if err != nil || len(fake.ranges) != 3 || overview.Candidate == nil ||
		len(overview.Candidate.Breaches) != 1 || !overview.Candidate.Breaches[0].EvaluatedAt.Equal(at(60)) {
		t.Fatalf("hourly preview=%+v ranges=%v err=%v, want an hourly fallback breach", overview, fake.ranges, err)
	}
	if want := (incidentReplayRange{overview.FirstCheckAt, time.Hour, 168}); fake.ranges[2] != want {
		t.Errorf("hourly failure range=%+v, want the displayed checkpoints only: %+v", fake.ranges[2], want)
	}
}

func TestReplayErrorRateAnomalyUsesLiveCoverageAndFloor(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(minute int) time.Time { return start.Add(time.Duration(minute) * time.Minute) }
	fake := &incidentReplayProm{
		countAt: func(check time.Time) float64 {
			switch check {
			case at(14):
				return 10
			case at(15):
				return -1
			case at(17):
				return 0
			}
			for day := 1; day <= 3; day++ {
				if check.Equal(at(55).Add(-time.Duration(day) * 24 * time.Hour)) {
					return 10
				}
			}
			return 100
		},
		rateAt: func(check time.Time) float64 {
			switch check {
			case at(13), at(21), at(55):
				return 10
			case at(14):
				return 20
			case at(20):
				return 0.8
			}
			if check.Before(start) && check.Hour() == start.Hour() && check.Minute() == 20 {
				return 0.2
			}
			if check.Before(start) && check.Hour() == start.Hour() && check.Minute() == 21 {
				return 0
			}
			return 1
		},
	}
	config := alerts.AnomalyPreviewComparison{Metric: "error_rate_pct", Comparison: api.AlertComparisonAboveBaseline, Threshold: 3}
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), fake, "orders", "5m", config, start, 15)
	if err != nil || len(fake.ranges) != 16 || got.Evaluated != 116 || got.Skipped != 4 || len(got.Breaches) != 1 {
		t.Fatalf("replay=%+v reads=%d err=%v, want one eligible error-rate breach and four skips", got, len(fake.ranges), err)
	}
	breach := got.Breaches[0]
	if !breach.EvaluatedAt.Equal(at(13)) || breach.EligibleRequests == nil || *breach.EligibleRequests != 100 ||
		breach.Observed != 10 || breach.Baseline != 1 || breach.TriggerValue != 3 {
		t.Fatalf("breach=%+v, want 10%% over 100 eligible requests against a 1%% baseline", breach)
	}
	for i, want := range []struct {
		minute   int
		reason   api.AlertAnomalyEvaluationReason
		count    int64
		required int64
	}{
		{14, api.AlertAnomalyInsufficientTraffic, 10, 50},
		{15, api.AlertAnomalyTelemetryMissing, -1, 0},
		{17, api.AlertAnomalyInsufficientTraffic, 0, 50},
		{55, api.AlertAnomalyInsufficientHistory, 100, 0},
	} {
		skipped := got.SkippedWindows[i]
		count := int64(-1)
		if skipped.EligibleRequests != nil {
			count = *skipped.EligibleRequests
		}
		if !skipped.EvaluatedAt.Equal(at(want.minute)) || skipped.Reason != want.reason ||
			count != want.count || skipped.RequiredRequests != want.required {
			t.Errorf("skip at minute %d = %+v with count %d, want %+v", want.minute, skipped, count, want)
		}
	}
	if got.IncidentReplay == nil || len(got.IncidentReplay.Incidents) != 1 {
		t.Fatalf("incident replay=%+v, want one error-rate incident", got.IncidentReplay)
	}
	incident := got.IncidentReplay.Incidents[0]
	if !incident.OpenedAt.Equal(at(13)) || incident.EndedAt == nil || !incident.EndedAt.Equal(at(16)) {
		t.Fatalf("incident=%+v, want recovery on the first evaluated healthy check after the skipped checks", incident)
	}
}

func TestReplayRequestCountAnomalyIncidents(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	prom := &incidentReplayProm{countAt: func(at time.Time) float64 {
		switch elapsed := at.Sub(start); {
		case elapsed == 12*time.Minute:
			return 40
		case elapsed == 18*time.Minute:
			return -1
		case elapsed >= 13*time.Minute && elapsed < 21*time.Minute:
			return 0
		default:
			return 100
		}
	}}
	candidate := 0.5
	config := alerts.AnomalyPreviewComparison{Comparison: api.AlertComparisonBelowBaseline, Threshold: 0.25, CandidateThreshold: &candidate}
	overview, err := alerts.PreviewAnomaly(t.Context(), prom, "orders", "5m", config, start.Add(2*time.Hour))
	if err != nil || len(prom.ranges) != 1 || overview.IncidentReplay != nil || len(overview.Breaches) != 0 || len(overview.Candidate.Breaches) != 0 {
		t.Fatalf("hourly overview=%+v err=%v, want the short outage to fall between checks", overview, err)
	}
	prom.ranges = nil
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), prom, "orders", "5m", config, start, 5)
	if err != nil || got.IncidentReplay == nil || got.StepSeconds != 60 || got.Evaluated != 119 || got.Skipped != 1 ||
		!got.FirstCheckAt.Equal(start) || !got.LastCheckAt.Equal(start.Add(119*time.Minute)) {
		t.Fatalf("incident replay=%+v err=%v, want two hours with one missing sample", got, err)
	}
	if len(prom.ranges) != 8 {
		t.Fatalf("expected one bounded range per day, got %v", prom.ranges)
	}
	for day, r := range prom.ranges {
		if want := (incidentReplayRange{start.Add(-time.Duration(day) * 24 * time.Hour), time.Minute, 120}); r != want {
			t.Errorf("day %d range=%+v, want %+v", day, r, want)
		}
	}
	if len(got.SkippedWindows) != 1 || got.SkippedWindows[0].Reason != api.AlertAnomalyTelemetryMissing {
		t.Fatalf("skipped=%+v, want a telemetry gap", got.SkippedWindows)
	}
	for _, want := range []struct {
		incidents      []api.AlertObservedIncident
		opened, repeat int
	}{
		{got.IncidentReplay.Incidents, 13, 19},
		{got.IncidentReplay.CandidateIncidents, 12, 17},
	} {
		if len(want.incidents) != 1 {
			t.Fatalf("incidents=%+v, want one incident across the telemetry gap", want.incidents)
		}
		incident := want.incidents[0]
		if !incident.OpenedAt.Equal(start.Add(time.Duration(want.opened)*time.Minute)) ||
			incident.EndedAt == nil || !incident.EndedAt.Equal(start.Add(21*time.Minute)) || incident.Outcome != api.AlertObservationRecovered ||
			incident.EligibleNotifications != 2 || !incident.LastEligibleAt.Equal(start.Add(time.Duration(want.repeat)*time.Minute)) {
			t.Errorf("incident=%+v, want one recovery on first healthy check and two eligible notifications", incident)
		}
	}
}

func TestReplayRequestCountAnomalyKeepsAllIncidentsInWindow(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	prom := &incidentReplayProm{countAt: func(at time.Time) float64 {
		elapsed := at.Sub(start)
		if elapsed >= 0 && elapsed < 2*time.Hour && elapsed/time.Minute%2 == 1 {
			return 0
		}
		return 100
	}}
	config := alerts.AnomalyPreviewComparison{Comparison: api.AlertComparisonBelowBaseline, Threshold: 0.25}
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), prom, "orders", "5m", config, start, 15)
	if err != nil || got.IncidentReplay == nil || len(got.IncidentReplay.Incidents) != 60 {
		t.Fatalf("replay=%+v err=%v, want all 60 incidents within the window", got.IncidentReplay, err)
	}
	latest := got.IncidentReplay.Incidents[0]
	if !latest.OpenedAt.Equal(start.Add(119*time.Minute)) || latest.EndedAt != nil {
		t.Fatalf("latest=%+v, want the last incident to remain open at the replay boundary", latest)
	}
	first := got.IncidentReplay.Incidents[59]
	if !first.OpenedAt.Equal(start.Add(time.Minute)) || first.EndedAt == nil ||
		!first.EndedAt.Equal(start.Add(2*time.Minute)) || first.EligibleNotifications != 1 {
		t.Fatalf("first=%+v, want an immediate eligibility and a recovery at the next check", first)
	}
}

func TestReplayRequestCountAnomalyRecoveryConfirmation(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	minute := func(offset int) time.Time { return start.Add(time.Duration(offset) * time.Minute) }
	prom := &incidentReplayProm{countAt: func(at time.Time) float64 {
		switch at {
		case minute(13), minute(15):
			return 0
		case minute(14):
			return 40
		case minute(17):
			return -1
		default:
			return 100
		}
	}}
	candidate := 0.5
	config := alerts.AnomalyPreviewComparison{
		Comparison: api.AlertComparisonBelowBaseline, Threshold: 0.25, CandidateThreshold: &candidate, ConfirmRecovery: true,
	}
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), prom, "orders", "5m", config, start, 5)
	if err != nil || len(prom.ranges) != 8 || got.IncidentReplay == nil || got.IncidentReplay.RecoveryConfirmation == nil {
		t.Fatalf("replay=%+v ranges=%v err=%v, want both variants from the same eight telemetry reads", got, prom.ranges, err)
	}
	replay := got.IncidentReplay
	if len(replay.Incidents) != 2 || len(replay.CandidateIncidents) != 1 {
		t.Fatalf("today's replay=%+v, want the selected rule to recover and reopen after the brief healthy check", replay)
	}
	for i, end := range []int{16, 14} {
		incident := replay.Incidents[i]
		if incident.EndedAt == nil || !incident.EndedAt.Equal(minute(end)) || incident.EligibleNotifications != 1 {
			t.Errorf("incident %d=%+v, want immediate recovery at minute %d and one eligible notification", i, incident, end)
		}
	}
	confirmed := replay.RecoveryConfirmation
	if confirmed.RequiredHealthyChecks != 2 || len(confirmed.Incidents) != 1 || len(confirmed.CandidateIncidents) != 1 {
		t.Fatalf("confirmation=%+v, want one incident for each threshold", confirmed)
	}
	for _, incident := range []api.AlertObservedIncident{confirmed.Incidents[0], confirmed.CandidateIncidents[0]} {
		if !incident.OpenedAt.Equal(minute(13)) || incident.EndedAt == nil || !incident.EndedAt.Equal(minute(19)) ||
			incident.Outcome != api.AlertObservationRecovered || incident.EligibleNotifications != 1 || !incident.LastEligibleAt.Equal(minute(13)) {
			t.Errorf("confirmed incident=%+v, want immediate opening and recovery after two healthy checks following the gap", incident)
		}
	}
}

func TestAnomalySameWeekdayComparison(t *testing.T) {
	sunday := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	outageStart, recovery := sunday.Add(30*time.Minute), sunday.Add(38*time.Minute)
	fallback := sunday.Add(32 * time.Minute)
	sparseDaily := sunday.Add(55 * time.Minute)
	prom := &incidentReplayProm{countAt: func(at time.Time) float64 {
		if !at.Before(outageStart) && at.Before(recovery) {
			return 0
		}
		if at.Equal(fallback.Add(-21 * 24 * time.Hour)) {
			return -1
		}
		for day := 1; day <= 3; day++ {
			if at.Equal(sparseDaily.Add(-time.Duration(day) * 24 * time.Hour)) {
				return -1
			}
		}
		if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
			return 60
		}
		return 240
	}}
	config := alerts.AnomalyPreviewComparison{Comparison: api.AlertComparisonBelowBaseline, Threshold: 0.5, SameWeekday: true}

	overview, err := alerts.PreviewAnomaly(t.Context(), prom, "orders", "5m", config, sunday.Add(2*time.Hour))
	if err != nil || len(prom.ranges) != 3 || len(overview.Breaches) == 0 || overview.Candidate == nil ||
		overview.Candidate.WeekdayBaseline == nil || len(overview.Candidate.Breaches) != 0 || len(overview.Candidate.WeekdayBaseline.FallbackAt) != 0 {
		t.Fatalf("overview=%+v err=%v, want only the daily baseline to flag the quiet weekend", overview, err)
	}
	prom.ranges = nil
	got, err := alerts.ReplayAnomalyIncidents(t.Context(), prom, "orders", "15m", config, sunday, 10)
	if err != nil || len(prom.ranges) != 10 || got.Candidate == nil || got.Candidate.WeekdayBaseline == nil || got.IncidentReplay == nil {
		t.Fatalf("replay=%+v ranges=%v err=%v, want both baselines over the same two hours", got, prom.ranges, err)
	}
	for i, days := range []int{14, 21} {
		want := incidentReplayRange{sunday.Add(-time.Duration(days) * 24 * time.Hour), time.Minute, 120}
		if prom.ranges[8+i] != want {
			t.Errorf("additional history=%+v, want %+v", prom.ranges[8+i], want)
		}
	}
	weekday := got.Candidate.WeekdayBaseline
	if got.Candidate.Threshold != config.Threshold || got.IncidentReplay.CooldownMinutes != 10 ||
		weekday.Evaluated != 120 || len(weekday.SkippedWindows) != 0 || got.Evaluated != 119 ||
		len(got.SkippedWindows) != 1 || !got.SkippedWindows[0].EvaluatedAt.Equal(sparseDaily) ||
		len(weekday.FallbackAt) != 1 || !weekday.FallbackAt[0].Equal(fallback) {
		t.Fatalf("weekday coverage=%+v daily=%+v, want one fallback and an independent weekday check", weekday, got.SkippedWindows)
	}
	daily, candidate := got.IncidentReplay.Incidents, got.IncidentReplay.CandidateIncidents
	if len(daily) != 1 || !daily[0].OpenedAt.Equal(sunday) ||
		len(candidate) != 1 || !candidate[0].OpenedAt.Equal(outageStart) ||
		candidate[0].EndedAt == nil || !candidate[0].EndedAt.Equal(recovery) || candidate[0].EligibleNotifications != 1 {
		t.Fatalf("daily=%+v candidate=%+v, want a weekday incident only during the outage", daily, candidate)
	}
	if len(got.Candidate.Breaches) != 8 || got.Candidate.Breaches[0].Baseline != 60 ||
		got.Candidate.Breaches[2].Baseline != 240 || !got.Candidate.Breaches[2].EvaluatedAt.Equal(fallback) {
		t.Fatalf("weekday breaches=%+v, want 60 normally and 240 at fallback", got.Candidate.Breaches)
	}
}
