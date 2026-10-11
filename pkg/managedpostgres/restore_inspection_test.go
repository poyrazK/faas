package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mappedRestoreProvider struct {
	fakeProvider
	inspections  int
	missingProof bool
}

func (p *mappedRestoreProvider) InspectRestore(ctx context.Context, id string, request RestoreRequest) (ObservedDatabase, error) {
	p.inspections++
	if id != "restored-"+request.ResourceID || request.SourceResourceID != p.lastRestore.SourceResourceID || !request.PointInTime.Equal(p.lastRestore.PointInTime) || request.Spec != p.lastRestore.Spec {
		return ObservedDatabase{}, ErrConflict
	}
	o, err := p.fakeProvider.Inspect(ctx, id)
	if p.missingProof {
		o.RestoreLineage = nil
	}
	return o, err
}

func TestRestoreReconciliationUsesPinnedInspectionAndStillRequiresProof(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "mapped", true: "unverified"}[missing], func(t *testing.T) {
			p := &mappedRestoreProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady}, missingProof: missing}
			store := NewMemoryStore()
			s := testService(t, testRegistry(t, p, nil), store)
			source, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			p.provisionStatus = ProviderStatusPending
			target, err := s.Restore(t.Context(), RestoreDatabaseRequest{AccountID: "account", SourceDatabaseID: source.ID, Name: "target", PointInTime: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)})
			if err != nil || target.State != StateProvisioning {
				t.Fatalf("restore: %+v %v", target, err)
			}
			s.now = func() time.Time { return target.RetryAt.Add(time.Second) }
			result, err := s.Reconcile(t.Context(), target.AccountID, target.ID)
			if p.inspections != 1 {
				t.Fatal("restore inspection did not receive persisted intent")
			}
			if missing {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("unverified result: %+v %v", result, err)
				}
				held, _ := store.Get(t.Context(), target.AccountID, target.ID)
				if held.State == StateReady {
					t.Fatal("missing proof published")
				}
			} else if err != nil || result.State != StateReady {
				t.Fatalf("mapped result: %+v %v", result, err)
			}
		})
	}
}

type qualificationLineageProvider struct {
	qualificationProvider
	fault string
}

func (p *qualificationLineageProvider) Restore(ctx context.Context, r RestoreRequest) (ObservedDatabase, error) {
	o, e := p.qualificationProvider.Restore(ctx, r)
	switch p.fault {
	case "creation_missing":
		o.RestoreLineage = nil
	case "creation_point":
		o.RestoreLineage.PointInTime = o.RestoreLineage.PointInTime.Add(time.Second)
	case "replay_identity":
		if p.restore > 1 {
			o.ProviderResourceID = "substituted-target"
		}
	case "replay_lineage":
		if p.restore > 1 {
			o.RestoreLineage = nil
		}
	}
	return o, e
}
func (p *qualificationLineageProvider) InspectRestore(ctx context.Context, id string, r RestoreRequest) (ObservedDatabase, error) {
	if r.SourceResourceID != p.resourceID+"/data" || !r.PointInTime.Equal(p.pointInTime) {
		return ObservedDatabase{}, ErrConflict
	}
	o, e := p.qualificationProvider.Inspect(ctx, id)
	switch p.fault {
	case "ready_missing":
		o.RestoreLineage = nil
	case "ready_source":
		o.RestoreLineage.SourceResourceID = "foreign-data"
	}
	return o, e
}

func TestQualificationRejectsLineageFailuresDespiteSuccessfulDataProbe(t *testing.T) {
	for _, fault := range []string{"valid", "creation_missing", "creation_point", "ready_missing", "ready_source", "replay_identity", "replay_lineage"} {
		t.Run(fault, func(t *testing.T) {
			p := &qualificationLineageProvider{qualificationProvider: qualificationProvider{capabilities: testCapabilities()}, fault: fault}
			report, err := QualifyProvider(t.Context(), p, QualificationOptions{ProviderName: "fake", ResourceID: "lineage-qualification", Spec: testSpec(), Mutating: true})
			if fault == "valid" {
				if err != nil {
					t.Fatalf("valid qualification: %+v %v", report, err)
				}
				return
			}
			if !errors.Is(err, ErrQualificationFailed) {
				t.Fatalf("unverified lineage qualified: %+v %v", report, err)
			}
		})
	}
}
