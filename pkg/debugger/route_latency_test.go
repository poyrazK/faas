package debugger

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func latencyTestRow(t *testing.T, weight, ms int64, spans ...StoredSpan) RouteLatencyRow {
	t.Helper()
	body, err := json.Marshal(spans)
	if err != nil {
		t.Fatal(err)
	}
	return RouteLatencyRow{Example: api.RouteHealthInvestigationExample{TelemetryID: fmt.Sprintf("00000000-0000-4000-8000-%012d", ms), Status: 200, LatencyMS: ms, RepresentedRequests: weight}, Spans: body}
}

func latencyTestSpan(id, parent string, start, end int64, typ, kind string) StoredSpan {
	base := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	return StoredSpan{SpanID: id, ParentSpanID: parent, StartTimeUnixNano: uint64(base.Add(time.Duration(start) * time.Millisecond).UnixNano()), EndTimeUnixNano: uint64(base.Add(time.Duration(end) * time.Millisecond).UnixNano()), DurationNanos: uint64((end - start) * int64(time.Millisecond)), Name: "private-db-host@example.test", DBStatement: "SELECT secret", Attributes: map[string]string{"gregale.dependency.type": typ, "gregale.dependency.kind": kind, "private": "password"}}
}

func TestRouteLatencyWeightedDependenciesExclusiveOverlapAndPrivateFields(t *testing.T) {
	root := latencyTestSpan("root", "", 0, 600, "", "")
	first := latencyTestSpan("first", "root", 100, 500, "managed_binding", "postgres")
	second := latencyTestSpan("second", "root", 200, 550, "managed_binding", "postgres")
	first.Status = "ERROR"
	c := []RouteLatencyRow{latencyTestRow(t, 99, 600, root, first, second), latencyTestRow(t, 1, 5, latencyTestSpan("fast", "", 0, 5, "managed_binding", "postgres"))}
	s := []RouteLatencyRow{latencyTestRow(t, 100, 100, latencyTestSpan("stable", "", 0, 40, "managed_binding", "postgres"))}
	d := RouteLatencyDiagnostics(c, s, 2, 1)
	dep := d.Dependencies[0]
	if dep.Type != "managed_binding" || dep.Kind != "postgres" || dep.Status != "compared" || *dep.Candidate.P95MS != 400 || *dep.P95DeltaMS != 360 || dep.Candidate.RepresentedCalls != 199 || dep.Candidate.SpanSamples != 3 || dep.Candidate.ErrorCalls != 99 {
		t.Fatalf("weighted comparison: %+v", dep)
	}
	if *d.Dependencies[1].Candidate.ExclusiveP95MS != 150 || d.Candidate.TimingIncomplete || d.Dependencies[1].P95DeltaMS != nil {
		t.Fatal("overlapping children or one-sided comparison")
	}
	body, _ := json.Marshal(d)
	for _, private := range []string{"private-db-host", "SELECT secret", "password", "attributes", "db_statement"} {
		if strings.Contains(string(body), private) {
			t.Fatal("diagnostic leaked span details")
		}
	}
}

func TestRouteLatencyMissingTimingStagesAndDistinctWakeWeights(t *testing.T) {
	span := latencyTestSpan("legacy", "", 0, 300, "outbound_integration", "http")
	span.StartTimeUnixNano, span.EndTimeUnixNano = 0, 0
	c := []RouteLatencyRow{latencyTestRow(t, 100, 600, span), latencyTestRow(t, 50, 700), latencyTestRow(t, 5, 500)}
	zero, boot := int64(0), int64(250)
	for i := range c {
		c[i].GuestMS, c[i].ColdBoot, c[i].WakeID, c[i].WakeBootMS = &zero, true, "same-wake", &boot
	}
	c[2].Spans = []byte("malformed")
	d := RouteLatencyDiagnostics(c, nil, 3, 0)
	if !d.Candidate.TimingIncomplete || d.Candidate.SpanRows != 1 || d.Candidate.MissingSpanRows != 2 || d.Candidate.SampledRequests != 155 || *d.Candidate.GuestP95MS != 0 || d.Candidate.GuestRequests != 155 || d.Candidate.WakeSamples != 1 || *d.Candidate.WakeBootP95MS != 250 || d.Candidate.ColdBootRequests != 155 {
		t.Fatalf("missing/stage coverage: %+v", d.Candidate)
	}
	if d.Dependencies[0].Candidate.ExclusiveP95MS != nil || *d.Dependencies[0].Candidate.P95MS != 300 || d.Stable.GuestP95MS != nil || d.Stable.WakeBootP95MS != nil {
		t.Fatal("legacy timing or missing evidence presented as measured")
	}
}

func TestRouteLatencyIndependentCapsAndInvalidClassification(t *testing.T) {
	var spans []StoredSpan
	for i := 0; i < api.DebugEvidenceMaxSpans+1; i++ {
		spans = append(spans, latencyTestSpan(fmt.Sprint(i), "", 0, int64(i+1), "managed_binding", fmt.Sprintf("kind_%d", i)))
	}
	spans[0].Attributes["gregale.dependency.type"] = "private-name"
	spans[1].Attributes["gregale.dependency.kind"] = "private@host"
	rows := make([]RouteLatencyRow, api.RouteHealthLatencyEvidenceRowsLimit+1)
	rows[0] = latencyTestRow(t, 2, 200, spans...)
	for i := 1; i < len(rows); i++ {
		rows[i] = latencyTestRow(t, 1, int64(i+200))
	}
	d := RouteLatencyDiagnostics(rows, nil, int64(len(rows)), 0)
	if d.Candidate.SampledRows != 32 || d.Candidate.SampledRequests != 33 || !d.Candidate.SamplesTruncated || d.Candidate.SpanSamples != 100 || !d.Candidate.SpansTruncated || !d.Candidate.TimingIncomplete || !d.DependenciesTruncated || len(d.Dependencies) != 16 {
		t.Fatalf("caps: %+v", d)
	}
	for _, dep := range d.Dependencies {
		if dep.Candidate.ExclusiveP95MS != nil || dep.P95DeltaMS != nil {
			t.Fatal("truncated/one-sided sample certified")
		}
	}
	parsed, _ := ParseSpans(rows[0].Spans)
	for _, span := range parsed {
		if span.DependencyType == "private-name" || span.DependencyKind == "private@host" {
			t.Fatal("invalid dependency classification")
		}
	}
}

func TestRouteLatencyDuplicateSpanIDsSuppressExclusiveTiming(t *testing.T) {
	a := latencyTestSpan("duplicate", "", 0, 100, "managed_binding", "postgres")
	b := latencyTestSpan("duplicate", "", 0, 50, "managed_binding", "postgres")
	d := RouteLatencyDiagnostics([]RouteLatencyRow{latencyTestRow(t, 1, 100, a, b)}, nil, 1, 0)
	if !d.Candidate.TimingIncomplete || d.Dependencies[0].Candidate.ExclusiveP95MS != nil {
		t.Fatal("ambiguous hierarchy presented as complete timing")
	}
}
