package managedpostgres

import (
	"context"
	"testing"
	"time"
)

type provisionInspectingProvider struct {
	fakeProvider
	provisionInspections []string
}

func (p *provisionInspectingProvider) InspectProvision(ctx context.Context, id string) (ObservedDatabase, error) {
	p.provisionInspections = append(p.provisionInspections, id)
	return p.fakeProvider.Inspect(ctx, id)
}

// A provider that finishes a database's setup once its resource is up
// (ADR-1027's native backend) sees a provisioning database through
// InspectProvision; a restore is still inspected as one.
func TestProvisioningReconciliationUsesProvisionInspection(t *testing.T) {
	p := &provisionInspectingProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusPending, inspectStatus: ProviderStatusReady}}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	created, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
	if err != nil || created.State != StateProvisioning || len(p.provisionInspections) != 0 {
		t.Fatalf("create: %+v %v; provision inspections %v", created, err, p.provisionInspections)
	}
	s.now = func() time.Time { return created.RetryAt.Add(time.Second) }
	ready, err := s.Reconcile(t.Context(), created.AccountID, created.ID)
	if err != nil || ready.State != StateReady || len(p.provisionInspections) != 1 || p.provisionInspections[0] != ready.ProviderResourceID || p.inspectCalls != 1 {
		t.Fatalf("reconcile: %+v %v; provision inspections %v, inspections %d", ready, err, p.provisionInspections, p.inspectCalls)
	}

	target, err := s.Restore(t.Context(), RestoreDatabaseRequest{AccountID: "account", SourceDatabaseID: ready.ID, Name: "target", PointInTime: ready.CreatedAt})
	if err != nil || target.State != StateProvisioning {
		t.Fatalf("restore: %+v %v", target, err)
	}
	s.now = func() time.Time { return target.RetryAt.Add(time.Second) }
	if restored, err := s.Reconcile(t.Context(), target.AccountID, target.ID); err != nil || restored.State != StateReady || len(p.provisionInspections) != 1 {
		t.Fatalf("restore reconcile: %+v %v; provision inspections %v", restored, err, p.provisionInspections)
	}
}
