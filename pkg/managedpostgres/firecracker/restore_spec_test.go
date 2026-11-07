package firecracker

import (
	"errors"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHistoricalRestoreMajorUsesCompleteHistory(t *testing.T) {
	p, scheduler, _ := newTestProvider(t)
	source := managedpostgres.RestoreSourceDefinition{Spec: devSpec(), ProviderResourceID: testDatabaseID, DataResourceID: testDatabaseID}
	source.Spec.PostgresMajor = 18
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rt := &scheddpb.DatabaseRuntime{DatabaseId: testDatabaseID, PostgresMajor: 18, State: "parked", MajorHistoryComplete: true, MajorHistory: []*scheddpb.DatabaseMajorSpan{
		{PostgresMajor: 14, Until: timestamppb.New(t0)}, {PostgresMajor: 16, Until: timestamppb.New(t0.Add(time.Hour))}, {PostgresMajor: 17, Until: timestamppb.New(t0.Add(2 * time.Hour))},
	}}
	scheduler.vms[testDatabaseID] = rt
	for _, tc := range []struct {
		point time.Time
		major int
	}{{t0.Add(-time.Hour), 14}, {t0, 16}, {t0.Add(time.Hour), 17}, {t0.Add(2 * time.Hour), 18}} {
		spec, err := p.ResolveRestoreSpec(t.Context(), source, tc.point)
		if err != nil || spec.PostgresMajor != tc.major {
			t.Fatalf("major at %v: %+v %v", tc.point, spec, err)
		}
	}
	if len(scheduler.restores) != 0 || len(scheduler.provision) != 0 {
		t.Fatal("read-only planning created a resource")
	}
	// An N-1 scheduler can establish an unchanged major, but an upgraded
	// source needs complete history before any target is reserved.
	rt.MajorHistoryComplete = false
	rt.MajorHistory = nil
	rt.Upgrade = nil
	if spec, err := p.ResolveRestoreSpec(t.Context(), source, t0); err != nil || spec.PostgresMajor != 18 {
		t.Fatal(spec, err)
	}
	rt.Upgrade = &scheddpb.DatabaseUpgrade{FromMajor: 17, ToMajor: 18, State: "ok"}
	if _, err := p.ResolveRestoreSpec(t.Context(), source, t0); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatal("old scheduler's historical major was guessed", err)
	}
	rt.MajorHistoryComplete = true
	for _, history := range [][]*scheddpb.DatabaseMajorSpan{{nil}, {{PostgresMajor: 17}}, {{PostgresMajor: 18, Until: timestamppb.New(t0)}}, {{PostgresMajor: 16, Until: timestamppb.New(t0)}, {PostgresMajor: 17, Until: timestamppb.New(t0)}}} {
		rt.MajorHistory = history
		if _, err := p.ResolveRestoreSpec(t.Context(), source, t0); err == nil {
			t.Fatal("invalid major history was accepted", history)
		}
	}
}
