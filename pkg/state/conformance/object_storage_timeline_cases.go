package conformance

import (
	"regexp"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

var timelinePattern = regexp.MustCompile(`^[0-9A-F]{8}$`)

// ADR-20261025111015 amendment: the bootstrap accepts the database's
// current PostgreSQL timeline once and never moves an acceptance; an
// explicit acceptance records the current timeline.
func testObjectStorageTimelineFence(t *testing.T, fx *Fixture) {
	s, ok := fx.Store.(state.ObjectStorageTimelineStore)
	if !ok {
		t.Fatalf("%T does not keep the object storage timeline fence", fx.Store)
	}
	_, current, err := s.ObjectStorageTimelines(fx.Ctx)
	if err != nil || !timelinePattern.MatchString(current) {
		t.Fatalf("current timeline = %q, %v", current, err)
	}
	if err := s.BootstrapObjectStorageTimeline(fx.Ctx); err != nil {
		t.Fatal(err)
	}
	accepted, current, err := s.ObjectStorageTimelines(fx.Ctx)
	if err != nil || accepted != current {
		t.Fatalf("after bootstrap accepted %q current %q (%v), want equal", accepted, current, err)
	}
	if err := s.BootstrapObjectStorageTimeline(fx.Ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.AcceptObjectStorageTimeline(fx.Ctx, "conformance")
	if err != nil || got != current {
		t.Fatalf("accept = %q, %v; want %q", got, err, current)
	}
	if _, err := s.AcceptObjectStorageTimeline(fx.Ctx, " "); err == nil {
		t.Fatal("an acceptance without an author was recorded")
	}
}
