package publicstatus

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestComponentsForAlertMapsDaemonsAndFailsWideForUnknownControlPlane(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   []Component
	}{
		{"builder", map[string]string{"component": "builderd"}, []Component{ComponentDeployments}},
		{"public capability", map[string]string{"component": "observability"}, []Component{ComponentObservability}},
		{"public gateway", map[string]string{"daemon": "gatewayd-public"}, []Component{ComponentNetworking}},
		{"generic with daemon", map[string]string{"component": "platform", "daemon": "vmmd"}, []Component{ComponentAppExecution}},
		{"unknown control plane", map[string]string{"component": "controlplane"}, AllComponents()},
		{"irrelevant unknown", map[string]string{"component": "customer-workload"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComponentsForAlert(tt.labels); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ComponentsForAlert() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEvaluateUsesWorstStateAndNeverInventsMajorOutage(t *testing.T) {
	states := Evaluate([]Alert{
		{Severity: "warn", Labels: map[string]string{"component": "builderd"}},
		{Severity: "page", Labels: map[string]string{"component": "builderd"}},
		{Severity: "page", Labels: map[string]string{"component": "gatewayd-internal"}},
	}, []Overlay{
		{State: StateMaintenance, Components: []Component{ComponentDeployments}},
		{State: StateMajorOutage, Components: []Component{ComponentAPIConsole}},
	})
	if states[ComponentDeployments] != StatePartialOutage {
		t.Fatalf("deployments = %q, want partial_outage", states[ComponentDeployments])
	}
	if states[ComponentNetworking] != StatePartialOutage {
		t.Fatalf("networking = %q, want partial_outage", states[ComponentNetworking])
	}
	if states[ComponentAPIConsole] != StateMajorOutage {
		t.Fatalf("api_console = %q, want operator major_outage", states[ComponentAPIConsole])
	}
	if states[ComponentObservability] != StateOperational {
		t.Fatalf("observability = %q, want operational", states[ComponentObservability])
	}
}

func TestApplyOverlaysDoesNotMutateCachedTelemetry(t *testing.T) {
	telemetry := Evaluate(nil, nil)
	overlaid := ApplyOverlays(telemetry, []Overlay{{
		State:      StateMajorOutage,
		Components: []Component{ComponentAPIConsole},
	}})
	if overlaid[ComponentAPIConsole] != StateMajorOutage {
		t.Fatalf("overlaid api_console = %q, want major_outage", overlaid[ComponentAPIConsole])
	}
	if telemetry[ComponentAPIConsole] != StateOperational {
		t.Fatalf("cached telemetry mutated to %q", telemetry[ComponentAPIConsole])
	}
}

func TestSummarizeDayAndThirtyDayUptimeRespectCoverage(t *testing.T) {
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	buckets := make([]Bucket, 0, 10)
	for i := 0; i < 10; i++ {
		b := Bucket{At: day.Add(time.Duration(i) * 5 * time.Minute), State: StateOperational, HasTelemetry: i < 8}
		if i == 2 {
			b.State = StateDegraded
		}
		buckets = append(buckets, b)
	}
	obs := SummarizeDay(day, buckets, 10)
	if obs.State != StateDegraded || obs.CoveragePct != 80 || obs.UptimePct == nil || math.Abs(*obs.UptimePct-87.5) > 0.001 {
		t.Fatalf("observation = %#v, want degraded, 80%% coverage, 87.5%% uptime", obs)
	}

	low := SummarizeDay(day, buckets[:7], 10)
	if low.State != StateUnknown || low.UptimePct != nil || low.CoveragePct != 70 {
		t.Fatalf("low-coverage observation = %#v, want unknown/no uptime/70%%", low)
	}

	uptime, coverage, ok := ThirtyDayUptime([]DailyObservation{obs, low})
	if ok || uptime != 0 || coverage != 75 {
		t.Fatalf("ThirtyDayUptime = (%v,%v,%v), want withheld with 75%% coverage", uptime, coverage, ok)
	}

	full := DailyObservation{CoveragePct: 100, UptimePct: floatPointer(100), Expected: 288, Observed: 288, Up: 288}
	partial := DailyObservation{CoveragePct: 70, Expected: 288, Observed: 202, Up: 202}
	uptime, coverage, ok = ThirtyDayUptime([]DailyObservation{full, partial})
	if !ok || uptime != 100 || math.Abs(coverage-(490.0/576.0*100)) > 0.001 {
		t.Fatalf("ThirtyDayUptime with unequal raw coverage = (%v,%v,%v), want count-weighted 100%% uptime", uptime, coverage, ok)
	}
}

func TestThirtyDayUptimeWeightsPartialCurrentDayByExpectedBuckets(t *testing.T) {
	fullDay := DailyObservation{Expected: 288, Observed: 288, Up: 288, CoveragePct: 100, UptimePct: floatPointer(100)}
	currentDay := DailyObservation{Expected: 12, Observed: 12, Up: 6, CoveragePct: 100, UptimePct: floatPointer(50)}
	uptime, coverage, ok := ThirtyDayUptime([]DailyObservation{fullDay, currentDay})
	if !ok || coverage != 100 || math.Abs(uptime-98) > 0.001 {
		t.Fatalf("ThirtyDayUptime = (%v,%v,%v), want raw-bucket weighted 98%%", uptime, coverage, ok)
	}
}

func TestSummarizeDayDoesNotCreateSyntheticGreenHistory(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	obs := SummarizeDay(day, nil, 288)
	if obs.State != StateUnknown || obs.UptimePct != nil || obs.CoveragePct != 0 {
		t.Fatalf("empty day = %#v, want explicit no-data observation", obs)
	}
}

func floatPointer(value float64) *float64 { return &value }
