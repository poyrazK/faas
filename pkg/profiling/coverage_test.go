package profiling

import (
	"context"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProfileCoverageUnionsWorkersAndClipsWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	q := api.ProfileQuery{Start: now, End: now.Add(30 * time.Second)}
	var parts []*profile.Profile
	for _, row := range []struct{ start, duration int }{{-5, 15}, {5, 15}, {25, 10}} {
		p := cpuFixture(now.Add(time.Duration(row.start)*time.Second), 1)
		p.DurationNanos = int64(time.Duration(row.duration) * time.Second)
		parts = append(parts, receivedProfile(p, sampleIdentity{Collector: uuid.NewString(), ReceivedAt: now.Add(time.Minute)}))
	}
	p, err := profile.Merge(parts)
	if err != nil {
		t.Fatal(err)
	}
	out, err := coverageView(p, q)
	if err != nil || !out.Available || out.ReceivedProfiles != 3 || out.ContributingCollectors != 3 || out.CoveredSeconds != 25 || out.GapSeconds != 5 || out.LastReceivedAt == nil || out.FailuresComplete {
		t.Fatalf("coverage = %+v, %v", out, err)
	}
}

func TestProfileCoverageMissingTruncatedAndFailureEvidence(t *testing.T) {
	now := time.Now()
	q := api.ProfileQuery{Start: now, End: now.Add(time.Minute)}
	out, err := coverageView(nil, q)
	if err != nil || !out.Available || out.GapSeconds != 60 || out.LastReceivedAt != nil || out.ReceivedProfiles != 0 {
		t.Fatal(out, err)
	}
	p := coverageProfile("failed", now.UnixNano())
	p.Sample[0].Value[0] = 2
	out, err = coverageView(p, q)
	if err != nil || out.RecordedFailedUploads != 2 || out.FailuresComplete {
		t.Fatal(out, err)
	}
	p.Function[0].Name = "[other]"
	if _, err := coverageView(p, q); err == nil {
		t.Fatal("truncated coverage presented as exact")
	}
	p = coverageProfile("failed", now.UnixNano())
	p.SampleType[0].Unit = "nanoseconds"
	if _, err := coverageView(p, q); err == nil {
		t.Fatal("CPU time presented as collection counts")
	}
}

type failingCoverageBackend struct {
	captureBackend
	records int
}

func (b *failingCoverageBackend) RecordFailure(context.Context, Principal, time.Time) error {
	b.records++
	return nil
}

func TestProfileFailureEvidenceIsBoundedAndDoesNotSuppressRetries(t *testing.T) {
	now := time.Now().Add(-time.Second)
	backend := &failingCoverageBackend{captureBackend: captureBackend{fail: true}}
	s := NewService(backend, nil)
	s.clock = func() time.Time { return now.Add(time.Second) }
	e := Envelope{Principal: principalFixture(now), Upload: Upload{Profile: encoded(t, cpuFixture(now, 1))}}
	for range 3 {
		if err := s.Push(t.Context(), e); err == nil {
			t.Fatal("failed upload succeeded")
		}
	}
	if backend.records != 1 {
		t.Fatal("failure recording was not rate limited", backend.records)
	}
	backend.fail = false
	if err := s.Push(t.Context(), e); err != nil {
		t.Fatal("retry suppressed", err)
	}
	if backend.records != 1 {
		t.Fatal("successful retry added a failure")
	}
	e.Principal.Plan = api.PlanFree
	if err := s.Push(t.Context(), e); err == nil || backend.records != 1 {
		t.Fatal("disabled plan generated backend metadata")
	}
}
